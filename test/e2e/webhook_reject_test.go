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
		// 이 Describe 는 *admission webhook* 이 실제로 등록돼 있을 때만 의미가 있다.
		// e2e 는 `make deploy`(kustomize) 로 배포하는데 그 경로에는 웹훅이 없다:
		//   - config/default/kustomization.yaml 이 ../webhook 을 포함하지 않고
		//   - config/manager/manager.yaml 이 --enable-webhooks 를 넘기지 않는다
		//     (cmd/main.go 기본값 false — cert-manager 의존 때문에 chart 게이트로만 활성).
		// 실제로 웹훅을 켜는 것은 Helm chart 뿐이다(deployment.yaml --enable-webhooks=true).
		// 따라서 kustomize 배포 위에서 이 spec 을 돌리면 **영원히 실패**한다 — 검증
		// 로직(ValidateLDAPSpec/ValidateOIDCSpec)이 정상인데도. 없으면 사유를 밝히고 skip.
		out, err := utils.Run(exec.Command("kubectl", "get", "validatingwebhookconfiguration",
			"-o", "jsonpath={.items[*].metadata.name}"))
		if err != nil || !strings.Contains(out, "mongodb") {
			Skip("admission webhook 미배포 — `make deploy`(kustomize) 경로는 config/default 에 " +
				"../webhook 이 없고 --enable-webhooks 도 켜지 않는다. Helm chart 배포에서만 유효한 spec.")
		}
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
		// ValidateLDAPSpec 의 실제 메시지는 "ldap.tls must be true ... plaintext LDAP
		// exposes credentials ..." 다 — "cleartext" 라는 단어는 쓰지 않는다.
		Expect(out+err.Error()).To(ContainSubstring("ldap.tls must be true"),
			"reject reason should name the tls requirement")
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
