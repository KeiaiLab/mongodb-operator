//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.
*/

// auth_ldap_test.go — F27 (cycle 4) LDAP auth e2e stub.
//
// 본 cycle 4 의 acceptance: API path (AuthSpec.LDAP) 가 클러스터에서 인식되고
// mongod 가 ldapServers 인자로 기동되는지 검증. 실제 LDAP server (OpenLDAP /
// 389-DS) bind 검증은 cycle 8 ClusterGroup 단계에서 보강.

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
	authLDAPNamespace = "mongodb-ldap-e2e"
	authLDAPCRName    = "mdb-ldap"
)

var _ = Describe("MongoDB AuthSpec.LDAP (cycle 4 / F23-F27)", Ordered, func() {
	BeforeAll(func() {
		cmd := exec.Command("kubectl", "create", "namespace", authLDAPNamespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		cmd := exec.Command("kubectl", "delete", "namespace", authLDAPNamespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)
	})

	It("CRD accepts MongoDB.spec.auth.ldap config", func() {
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
    mechanism: PLAIN
    adminCredentialsSecretRef:
      name: mdb-ldap-admin
    ldap:
      servers: "ldap-stub.svc:389"
      bindMethod: simple
      tls: true
      authorizationQueryTemplate: "{USER}?memberOf?base"
`, authLDAPCRName, authLDAPNamespace)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "apply output: %s", out)
	})

	// cycle 8+: 실제 LDAP server bind round-trip 검증
})
