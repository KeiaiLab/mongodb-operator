/*
Copyright 2026 Keiailab.
*/

package encryption

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// vaultMockServer 는 Vault Transit API mock — encrypt/decrypt/health.
func vaultMockServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	const token = "test-token-12345"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sys/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sealed":false,"initialized":true,"standby":false}`))
	})
	mux.HandleFunc("/v1/transit/encrypt/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var body struct {
			Plaintext string `json:"plaintext"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]string{
				"ciphertext": "vault:v1:mock-" + body.Plaintext,
			},
		})
	})
	mux.HandleFunc("/v1/transit/decrypt/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var body struct {
			Ciphertext string `json:"ciphertext"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// strip "vault:v1:mock-" prefix
		prefix := "vault:v1:mock-"
		if !strings.HasPrefix(body.Ciphertext, prefix) {
			http.Error(w, "invalid ciphertext", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]string{
				"plaintext": strings.TrimPrefix(body.Ciphertext, prefix),
			},
		})
	})
	srv := httptest.NewServer(mux)
	return srv, token
}

func TestVaultClient_Health(t *testing.T) {
	t.Parallel()
	srv, token := vaultMockServer(t)
	defer srv.Close()
	client, err := NewVaultClientFromSpec(&mongodbv1alpha1.VaultKMSConfig{
		Address: srv.URL, KeyName: "k",
	}, token, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Health(context.Background()); err != nil {
		t.Errorf("Health failed: %v", err)
	}
}

func TestVaultClient_EncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	srv, token := vaultMockServer(t)
	defer srv.Close()
	client, err := NewVaultClientFromSpec(&mongodbv1alpha1.VaultKMSConfig{
		Address: srv.URL, KeyName: "mongodb-key",
	}, token, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	plaintext := []byte("super-secret-keyfile-data")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ct, err := client.Encrypt(ctx, "mongodb-key", plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(ct, "vault:v1:") {
		t.Errorf("ciphertext format unexpected: %s", ct)
	}
	decrypted, err := client.Decrypt(ctx, "mongodb-key", ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	// Mock server stores base64 encoded plaintext directly.
	wantB64 := base64.StdEncoding.EncodeToString(plaintext)
	gotB64 := string(decrypted)
	// decrypt returns base64-decoded already, so compare raw plaintext.
	wantBytes, _ := base64.StdEncoding.DecodeString(wantB64)
	if string(wantBytes) != string(decrypted) {
		t.Errorf("decrypt mismatch — want %q got %q (b64 raw: %s)", string(wantBytes), string(decrypted), gotB64)
	}
}

func TestVaultClient_BadToken(t *testing.T) {
	t.Parallel()
	srv, _ := vaultMockServer(t)
	defer srv.Close()
	client, err := NewVaultClientFromSpec(&mongodbv1alpha1.VaultKMSConfig{
		Address: srv.URL, KeyName: "k",
	}, "wrong-token", nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Encrypt(context.Background(), "k", []byte("x"))
	if err == nil {
		t.Errorf("bad token must reject")
	}
}

func TestNewVaultClientFromSpec_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    *mongodbv1alpha1.VaultKMSConfig
		token   string
		wantErr bool
	}{
		{"nil spec", nil, "t", true},
		{"empty address", &mongodbv1alpha1.VaultKMSConfig{}, "t", true},
		{"empty token", &mongodbv1alpha1.VaultKMSConfig{Address: "https://v"}, "", true},
		{"ok", &mongodbv1alpha1.VaultKMSConfig{Address: "https://v"}, "t", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewVaultClientFromSpec(tc.spec, tc.token, nil)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestProbeKMS_DisabledNoop(t *testing.T) {
	t.Parallel()
	if err := ProbeKMS(context.Background(), nil, "", nil); err != nil {
		t.Errorf("nil spec must noop, got %v", err)
	}
	if err := ProbeKMS(context.Background(), &mongodbv1alpha1.EncryptionSpec{Enabled: false}, "", nil); err != nil {
		t.Errorf("disabled must noop, got %v", err)
	}
	if err := ProbeKMS(context.Background(), &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyProvider: "secret"}, "", nil); err != nil {
		t.Errorf("secret keyProvider noop, got %v", err)
	}
}

func TestProbeKMS_VaultReachable(t *testing.T) {
	t.Parallel()
	srv, token := vaultMockServer(t)
	defer srv.Close()
	err := ProbeKMS(context.Background(), &mongodbv1alpha1.EncryptionSpec{
		Enabled:     true,
		KeyProvider: "vault",
		KMSConfig: &mongodbv1alpha1.KMSConfigSpec{
			Vault: &mongodbv1alpha1.VaultKMSConfig{Address: srv.URL, KeyName: "k"},
		},
	}, token, nil)
	if err != nil {
		t.Errorf("vault probe failed: %v", err)
	}
}

func TestProbeKMS_UnknownProviderReject(t *testing.T) {
	t.Parallel()
	err := ProbeKMS(context.Background(), &mongodbv1alpha1.EncryptionSpec{
		Enabled: true, KeyProvider: "exotic-hsm",
	}, "", nil)
	if err == nil {
		t.Errorf("unknown provider must error")
	}
}
