/*
Copyright 2026 Keiailab.
*/

// auth_test.go — F24-F26 + F29-F31 (cycle 4) 단위 회귀 가드.

package auth

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// --- LDAP ---

func TestLDAPMongodArgs_NilOrEmpty(t *testing.T) {
	t.Parallel()
	if got := LDAPMongodArgs(nil); got != nil {
		t.Errorf("nil spec: got %v, want nil", got)
	}
	if got := LDAPMongodArgs(&mongodbv1alpha1.LDAPSpec{Servers: ""}); got != nil {
		t.Errorf("empty servers: got %v, want nil", got)
	}
}

func TestLDAPMongodArgs_FullSpec(t *testing.T) {
	t.Parallel()
	spec := &mongodbv1alpha1.LDAPSpec{
		Servers:                    "ldap1.example.com:389,ldap2.example.com:389",
		BindMethod:                 "simple",
		TLS:                        true,
		UserToDNMapping:            `[{"match":"(.+)","substitution":"cn={0},ou=Users,dc=ex,dc=com"}]`,
		AuthorizationQueryTemplate: "{USER}?memberOf?base",
	}
	args := LDAPMongodArgs(spec)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--ldapServers=ldap1.example.com:389,ldap2.example.com:389",
		"--ldapBindMethod=simple",
		"--ldapTransportSecurity=tls",
		"--ldapUserToDNMapping=",
		"--ldapAuthzQueryTemplate={USER}?memberOf?base",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args must contain %q, got: %v", want, args)
		}
	}
}

func TestLDAPMongodArgs_TLSDisabled(t *testing.T) {
	t.Parallel()
	args := LDAPMongodArgs(&mongodbv1alpha1.LDAPSpec{Servers: "ldap:389", TLS: false})
	if !contains(args, "--ldapTransportSecurity=none") {
		t.Errorf("TLS off must emit transportSecurity=none, got %v", args)
	}
}

func TestLDAPMongodArgs_BindMethodDefault(t *testing.T) {
	t.Parallel()
	args := LDAPMongodArgs(&mongodbv1alpha1.LDAPSpec{Servers: "ldap:389"})
	if !contains(args, "--ldapBindMethod=simple") {
		t.Errorf("default bindMethod must be 'simple', got %v", args)
	}
}

func TestValidateLDAPSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    *mongodbv1alpha1.LDAPSpec
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"empty servers reject", &mongodbv1alpha1.LDAPSpec{Servers: ""}, true},
		{"whitespace servers reject", &mongodbv1alpha1.LDAPSpec{Servers: "   "}, true},
		{"invalid bindMethod reject", &mongodbv1alpha1.LDAPSpec{Servers: "ldap:389", BindMethod: "kerberos"}, true},
		{"cleartext bind reject", &mongodbv1alpha1.LDAPSpec{Servers: "ldap:389", TLS: false, BindCredentialsSecretRef: &corev1.LocalObjectReference{Name: "s"}}, true},
		{"tls + bind ok", &mongodbv1alpha1.LDAPSpec{Servers: "ldap:389", TLS: true, BindCredentialsSecretRef: &corev1.LocalObjectReference{Name: "s"}}, false},
		{"simple + tls ok", &mongodbv1alpha1.LDAPSpec{Servers: "ldap:389", BindMethod: "simple", TLS: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateLDAPSpec(tc.spec)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("ValidateLDAPSpec err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// --- OIDC ---

func TestOIDCMongodSetParameter_NilEmpty(t *testing.T) {
	t.Parallel()
	got, err := OIDCMongodSetParameter(nil)
	if err != nil || got != "" {
		t.Errorf("nil → ('', nil), got (%q, %v)", got, err)
	}
}

func TestOIDCMongodSetParameter_Keycloak(t *testing.T) {
	t.Parallel()
	spec := &mongodbv1alpha1.OIDCSpec{
		IssuerURL:        "https://keycloak.example.com/realms/mongodb",
		ClientID:         "mongodb-prod",
		UserClaim:        "preferred_username",
		RolesClaim:       "groups",
		IdentityProvider: "keycloak",
	}
	got, err := OIDCMongodSetParameter(spec)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	for _, want := range []string{
		`"oidcIdentityProviders"`,
		`"issuer":"https://keycloak.example.com/realms/mongodb"`,
		`"clientId":"mongodb-prod"`,
		`"principalName":"preferred_username"`,
		`"authorizationClaim":"groups"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("OIDC param must contain %q, got: %s", want, got)
		}
	}
}

func TestOIDCMongodSetParameter_Defaults(t *testing.T) {
	t.Parallel()
	got, err := OIDCMongodSetParameter(&mongodbv1alpha1.OIDCSpec{
		IssuerURL: "https://idp.example.com",
		ClientID:  "mdb",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, `"principalName":"sub"`) {
		t.Errorf("default UserClaim=sub, got: %s", got)
	}
	if !strings.Contains(got, `"authorizationClaim":"groups"`) {
		t.Errorf("default RolesClaim=groups, got: %s", got)
	}
}

func TestValidateOIDCSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    *mongodbv1alpha1.OIDCSpec
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"empty issuer", &mongodbv1alpha1.OIDCSpec{IssuerURL: "", ClientID: "c"}, true},
		{"http issuer reject", &mongodbv1alpha1.OIDCSpec{IssuerURL: "http://idp.example.com", ClientID: "c"}, true},
		{"empty clientID", &mongodbv1alpha1.OIDCSpec{IssuerURL: "https://idp.example.com"}, true},
		{"unknown idp", &mongodbv1alpha1.OIDCSpec{IssuerURL: "https://idp", ClientID: "c", IdentityProvider: "azure"}, true},
		{"keycloak ok", &mongodbv1alpha1.OIDCSpec{IssuerURL: "https://idp", ClientID: "c", IdentityProvider: "keycloak"}, false},
		{"okta ok", &mongodbv1alpha1.OIDCSpec{IssuerURL: "https://idp", ClientID: "c", IdentityProvider: "okta"}, false},
		{"generic ok", &mongodbv1alpha1.OIDCSpec{IssuerURL: "https://idp", ClientID: "c"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateOIDCSpec(tc.spec)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("ValidateOIDCSpec err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func contains(s []string, sub string) bool {
	for _, x := range s {
		if x == sub {
			return true
		}
	}
	return false
}
