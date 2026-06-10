//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.
*/

// webhook_reject_test.go — cycle 18 webhook validators 가 *실 cluster* 에서
// invalid spec apply 시 *진본 reject* 함을 검증.

package e2e

import (
	"fmt"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/keiailab/mongodb-operator/test/utils"
)

const webhookRejectNS = "mongodb-webhook-reject"

var _ = Describe("Webhook Validators Real Reject (cycle 18)", Ordered, func() {
	BeforeAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "create", "namespace", webhookRejectNS))
		ensureAdminSecret(webhookRejectNS)
	})
	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "namespace", webhookRejectNS,
			"--ignore-not-found", "--wait=false"))
	})

	// LDAP cleartext bind reject — operator webhook should reject
	// (tls=false + bindCredentialsSecretRef set = cleartext credential transmission).
	It("Webhook rejects LDAP cleartext+credentials", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: reject-ldap-cleartext
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
      name: mdb-admin
    ldap:
      servers: "ldap.example.com:389"
      tls: false
      bindCredentialsSecretRef:
        name: ldap-bind-secret
`, webhookRejectNS)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).To(HaveOccurred(), "webhook must reject cleartext LDAP bind credentials")
		Expect(out+err.Error()).To(ContainSubstring("cleartext"), "reject reason should mention cleartext")
	})

	// OIDC http issuer reject — operator webhook https-only enforcement.
	It("Webhook rejects OIDC http issuer", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: reject-oidc-http
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
      name: mdb-admin
    oidc:
      issuerURL: "http://insecure.example.com"
      clientID: "mdb"
`, webhookRejectNS)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).To(HaveOccurred(), "webhook must reject OIDC http issuer")
		Expect(out+err.Error()).To(Or(
			ContainSubstring("https"),
			ContainSubstring("Invalid value"),
		), "reject reason should mention https requirement")
	})

	// Encryption KMS provider mismatch — secret keyProvider without secretRef.
	It("Webhook rejects encryption.keyProvider=secret without secretRef", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: reject-encryption-no-secret
  namespace: %s
spec:
  members: 3
  version:
    version: "8.2"
  storage:
    size: 1Gi
    encryption:
      enabled: true
      keyProvider: secret
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mdb-admin
`, webhookRejectNS)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).To(HaveOccurred(), "webhook must reject encryption secret provider without secretRef")
		Expect(out + err.Error()).To(Or(
			ContainSubstring("secretRef"),
			ContainSubstring("Invalid"),
		))
	})

	// Audit forwarder bad URL reject.
	It("Webhook rejects audit forwarder URL without http(s)", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: reject-audit-bad-url
  namespace: %s
spec:
  members: 3
  version:
    version: "8.2"
  storage:
    size: 1Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mdb-admin
  auditLog:
    enabled: true
    destination: file
    centralForwarder:
      type: loki
      url: "loki-no-scheme"
`, webhookRejectNS)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).To(HaveOccurred(), "webhook must reject bad forwarder URL")
		Expect(out + err.Error()).To(Or(
			ContainSubstring("http"),
			ContainSubstring("Invalid"),
		))
	})

	// Major skip upgrade reject — IsValidUpgradePath enforcement.
	It("Webhook rejects MongoDB version major skip upgrade", func() {
		base := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: reject-version-skip
  namespace: %s
spec:
  members: 3
  version:
    version: "8.0"
  storage:
    size: 1Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mdb-admin
`, webhookRejectNS)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(base)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "8.0 create OK")

		// Now patch to 8.3 (skip 8.2) — must reject.
		patchCmd := exec.Command("kubectl", "-n", webhookRejectNS, "patch", "mongodb",
			"reject-version-skip", "--type=merge",
			"-p", `{"spec":{"version":{"version":"8.3"}}}`)
		out, err := utils.Run(patchCmd)
		Expect(err).To(HaveOccurred(), "webhook must reject 8.0 → 8.3 minor skip")
		Expect(out + err.Error()).To(Or(
			ContainSubstring("skip"),
			ContainSubstring("upgrade"),
			ContainSubstring("Invalid"),
		))
	})
})
