/*
Copyright 2026 Keiailab.
*/

package encryption

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestMongodArgs_DisabledOrNil(t *testing.T) {
	t.Parallel()
	if got := MongodArgs(nil); got != nil {
		t.Errorf("nil: got %v want nil", got)
	}
	if got := MongodArgs(&mongodbv1alpha1.EncryptionSpec{Enabled: false}); got != nil {
		t.Errorf("disabled: got %v want nil", got)
	}
}

func TestMongodArgs_SecretProvider(t *testing.T) {
	t.Parallel()
	args := MongodArgs(&mongodbv1alpha1.EncryptionSpec{
		Enabled:     true,
		KeyProvider: "secret",
		CipherMode:  "AES256-GCM",
	})
	joined := strings.Join(args, " ")
	for _, w := range []string{"--enableEncryption", "--encryptionCipherMode=AES256-GCM", "/etc/mongodb-encryption/keyfile"} {
		if !strings.Contains(joined, w) {
			t.Errorf("must contain %q, got: %v", w, args)
		}
	}
}

func TestMongodArgs_KMSProviderUsesKMIP(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"vault", "aws-kms", "gcp-kms", "azure-kv"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			args := MongodArgs(&mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyProvider: p})
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--kmipServerName=localhost") {
				t.Errorf("%s must use KMIP proxy, got: %v", p, args)
			}
		})
	}
}

func TestMongodArgs_DefaultCipher(t *testing.T) {
	t.Parallel()
	args := MongodArgs(&mongodbv1alpha1.EncryptionSpec{Enabled: true})
	if !strings.Contains(strings.Join(args, " "), "AES256-GCM") {
		t.Errorf("default cipher must be AES256-GCM, got: %v", args)
	}
}

func TestValidateEncryptionSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    *mongodbv1alpha1.EncryptionSpec
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"disabled ok", &mongodbv1alpha1.EncryptionSpec{Enabled: false}, false},
		{"secret without ref reject", &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyProvider: "secret"}, true},
		{"secret with ref ok", &mongodbv1alpha1.EncryptionSpec{
			Enabled: true, KeyProvider: "secret",
			KMSConfig: &mongodbv1alpha1.KMSConfigSpec{Secret: &mongodbv1alpha1.SecretKMSConfig{
				SecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "k"}, Key: "key"},
			}},
		}, false},
		{"vault without config reject", &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyProvider: "vault"}, true},
		{"vault complete ok", &mongodbv1alpha1.EncryptionSpec{
			Enabled: true, KeyProvider: "vault",
			KMSConfig: &mongodbv1alpha1.KMSConfigSpec{Vault: &mongodbv1alpha1.VaultKMSConfig{
				Address: "https://vault.example.com:8200", KeyName: "mongodb-prod",
				AuthSecretRef: corev1.LocalObjectReference{Name: "vault-auth"},
			}},
		}, false},
		{"aws-kms bad arn reject", &mongodbv1alpha1.EncryptionSpec{
			Enabled: true, KeyProvider: "aws-kms",
			KMSConfig: &mongodbv1alpha1.KMSConfigSpec{AWSKMS: &mongodbv1alpha1.AWSKMSConfig{KeyARN: "not-arn", Region: "us-east-1"}},
		}, true},
		{"gcp-kms partial reject", &mongodbv1alpha1.EncryptionSpec{
			Enabled: true, KeyProvider: "gcp-kms",
			KMSConfig: &mongodbv1alpha1.KMSConfigSpec{GCPKMS: &mongodbv1alpha1.GCPKMSConfig{ProjectID: "p"}},
		}, true},
		{"unknown provider reject", &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyProvider: "fancy-hsm"}, true},
		{"negative rotation reject", &mongodbv1alpha1.EncryptionSpec{
			Enabled: true, KeyProvider: "secret",
			KMSConfig: &mongodbv1alpha1.KMSConfigSpec{Secret: &mongodbv1alpha1.SecretKMSConfig{
				SecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "k"}, Key: "key"},
			}},
			KeyRotationDays: -1,
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateEncryptionSpec(tc.spec)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("ValidateEncryptionSpec err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestNeedsKeyRotation(t *testing.T) {
	t.Parallel()
	secondsPerDay := int64(24 * 60 * 60)
	cases := []struct {
		name string
		last int64
		now  int64
		spec *mongodbv1alpha1.EncryptionSpec
		want bool
	}{
		{"disabled false", 0, secondsPerDay * 365, &mongodbv1alpha1.EncryptionSpec{Enabled: false, KeyRotationDays: 30}, false},
		{"zero rotation false", 0, secondsPerDay * 365, &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyRotationDays: 0}, false},
		{"not due", secondsPerDay * 10, secondsPerDay * 11, &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyRotationDays: 30}, false},
		{"due exactly", 0, secondsPerDay * 30, &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyRotationDays: 30}, true},
		{"overdue", 0, secondsPerDay * 365, &mongodbv1alpha1.EncryptionSpec{Enabled: true, KeyRotationDays: 30}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NeedsKeyRotation(tc.last, tc.now, tc.spec); got != tc.want {
				t.Errorf("NeedsKeyRotation: got %v want %v", got, tc.want)
			}
		})
	}
}
