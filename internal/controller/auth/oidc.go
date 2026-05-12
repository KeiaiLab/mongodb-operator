/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// oidc.go — F29-F31 (cycle 4) MongoDB-OIDC authentication helper.
//
// MongoDB 7.0+ 가 native OIDC bind 지원. 본 cycle 의 acceptance 는 mongod
// `oidcIdentityProviders` setParameter JSON 생성 + spec validation 까지.
// 실제 IdP 호환 검증 (Keycloak / Okta / Auth0) 은 cycle 8 단계에서 보강.

package auth

import (
	"encoding/json"
	"fmt"
	"strings"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// OIDCMongodSetParameter 는 OIDCSpec 으로부터 mongod 의 oidcIdentityProviders
// setParameter JSON 을 생성. 형식:
//
//	{
//	  "oidcIdentityProviders": [
//	    {
//	      "issuer": "https://keycloak.example.com/realms/mongodb",
//	      "clientId": "mongodb-prod",
//	      "principalName": "sub",
//	      "authorizationClaim": "groups",
//	      "supportsHumanFlows": true
//	    }
//	  ]
//	}
//
// 호출자는 본 return string 을 `--setParameter oidcIdentityProviders=<json>`
// 형태로 mongod args 에 append.
func OIDCMongodSetParameter(spec *mongodbv1alpha1.OIDCSpec) (string, error) {
	if spec == nil {
		return "", nil
	}
	provider := map[string]interface{}{
		"issuer":             spec.IssuerURL,
		"clientId":           spec.ClientID,
		"principalName":      defaultStr(spec.UserClaim, "sub"),
		"authorizationClaim": defaultStr(spec.RolesClaim, "groups"),
		"supportsHumanFlows": true,
	}
	wrapper := map[string]interface{}{
		"oidcIdentityProviders": []interface{}{provider},
	}
	b, err := json.Marshal(wrapper)
	if err != nil {
		return "", fmt.Errorf("marshal oidc providers: %w", err)
	}
	return string(b), nil
}

// ValidateOIDCSpec 은 webhook 검증 hook.
func ValidateOIDCSpec(spec *mongodbv1alpha1.OIDCSpec) error {
	if spec == nil {
		return nil
	}
	if strings.TrimSpace(spec.IssuerURL) == "" {
		return fmt.Errorf("oidc.issuerURL is required")
	}
	if !strings.HasPrefix(spec.IssuerURL, "https://") {
		return fmt.Errorf("oidc.issuerURL must use https:// (got %q)", spec.IssuerURL)
	}
	if strings.TrimSpace(spec.ClientID) == "" {
		return fmt.Errorf("oidc.clientID is required")
	}
	switch spec.IdentityProvider {
	case "", "generic", "keycloak", "okta", "auth0", "google":
		// supported
	default:
		return fmt.Errorf("oidc.identityProvider unknown: %q", spec.IdentityProvider)
	}
	return nil
}
