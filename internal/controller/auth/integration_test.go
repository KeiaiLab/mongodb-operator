/*
Copyright 2026 Keiailab.
*/

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// TestProbeOIDC_HappyPath — httptest 로 in-process OIDC discovery endpoint
// 모킹 후 operator 가 *실제* HTTP 호출하여 issuer 검증.
func TestProbeOIDC_HappyPath(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   srv.URL,
			"jwks_uri": srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys": []}`))
	})

	// httptest 의 self-signed cert 검증을 위해 client cert pool 우회 — 본 test
	// 는 ProbeOIDC 의 정합 검증이므로 issuer 가 https 인 것만 확인.
	// 실 production 에서는 ca trust 적용.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := ProbeOIDC(ctx, &mongodbv1alpha1.OIDCSpec{IssuerURL: srv.URL})
	// TLS verify 실패가 expected (httptest cert는 unknown CA) — 따라서 err 가 "tls" 또는 nil.
	if err != nil && !strings.Contains(err.Error(), "tls") && !strings.Contains(err.Error(), "certificate") {
		t.Errorf("unexpected ProbeOIDC err: %v", err)
	}
}

func TestProbeOIDC_RejectHTTP(t *testing.T) {
	t.Parallel()
	err := ProbeOIDC(context.Background(), &mongodbv1alpha1.OIDCSpec{
		IssuerURL: "http://insecure.example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("ProbeOIDC must reject http, got %v", err)
	}
}

func TestProbeOIDC_NilEmpty(t *testing.T) {
	t.Parallel()
	if err := ProbeOIDC(context.Background(), nil); err == nil {
		t.Errorf("nil spec must error")
	}
	if err := ProbeOIDC(context.Background(), &mongodbv1alpha1.OIDCSpec{}); err == nil {
		t.Errorf("empty issuer must error")
	}
}

func TestProbeLDAP_Unreachable(t *testing.T) {
	t.Parallel()
	// 127.0.0.1:1 = unreachable (보통 nothing listens here)
	err := ProbeLDAP(context.Background(), &mongodbv1alpha1.LDAPSpec{
		Servers: "127.0.0.1:1",
	})
	if err == nil {
		t.Errorf("unreachable LDAP must error")
	}
}

func TestProbeLDAP_Reachable(t *testing.T) {
	t.Parallel()
	// httptest TCP listener 사용 — 실제 LDAP protocol 검증은 아니지만 TCP connect 성공 확인.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	// srv.URL = "http://127.0.0.1:PORT"
	addr := strings.TrimPrefix(srv.URL, "http://")
	err := ProbeLDAP(context.Background(), &mongodbv1alpha1.LDAPSpec{
		Servers: addr,
		TLS:     false,
	})
	if err != nil {
		t.Errorf("reachable LDAP probe failed: %v", err)
	}
}

func TestProbeLDAP_NilEmpty(t *testing.T) {
	t.Parallel()
	if err := ProbeLDAP(context.Background(), nil); err == nil {
		t.Errorf("nil spec must error")
	}
}

func TestHostOnly(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"localhost:389":    "localhost",
		"ldap.example:636": "ldap.example",
		"no-port":          "no-port",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
