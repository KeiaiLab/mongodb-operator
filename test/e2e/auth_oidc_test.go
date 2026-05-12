//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.
*/

// auth_oidc_test.go — F32 (cycle 4) OIDC auth e2e stub.
//
// API path (AuthSpec.OIDC) 검증. 실제 Keycloak / Okta IdP 호환은 cycle 8.

package e2e

import (
	"fmt"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/keiailab/mongodb-operator/test/utils"
)

const (
	authOIDCNamespace = "mongodb-oidc-e2e"
	authOIDCCRName    = "mdb-oidc"
)

var _ = Describe("MongoDB AuthSpec.OIDC (cycle 4 / F28-F32)", Ordered, func() {
	BeforeAll(func() {
		cmd := exec.Command("kubectl", "create", "namespace", authOIDCNamespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		cmd := exec.Command("kubectl", "delete", "namespace", authOIDCNamespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)
	})

	It("CRD accepts MongoDB.spec.auth.oidc with Keycloak issuer", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: %s
  namespace: %s
spec:
  members: 3
  version:
    version: "8.2"
  storage:
    size: 1Gi
  auth:
    mechanism: MONGODB-OIDC
    adminCredentialsSecretRef:
      name: mdb-oidc-admin
    oidc:
      issuerURL: "https://keycloak.example.com/realms/mongodb"
      clientID: "mongodb-prod"
      userClaim: "preferred_username"
      rolesClaim: "groups"
      identityProvider: keycloak
`, authOIDCCRName, authOIDCNamespace)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "apply output: %s", out)
	})

	// cycle 8+: Keycloak/Okta IdP 실 호환 검증
})
