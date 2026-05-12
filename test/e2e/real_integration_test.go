//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.
*/

// real_integration_test.go — cycle 17 *실 외부 시스템 round-trip* e2e.
//
// 본 e2e 는 kind cluster 의 external-systems namespace 에 배포된 *실 외부
// 시스템* (Vault dev mode / Keycloak / OpenLDAP) 에 *operator integration
// code* 가 *진본 네트워크 호출* 을 성공시키는지 검증.
//
// kubectl port-forward 로 cluster-internal service 를 host:port 에 노출 후
// operator 의 ProbeOIDC / VaultClient / ProbeLDAP 를 *직접 호출*.

package e2e

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	authpkg "github.com/keiailab/mongodb-operator/internal/controller/auth"
	encryptionpkg "github.com/keiailab/mongodb-operator/internal/controller/encryption"
	"github.com/keiailab/mongodb-operator/test/utils"
)

// portForward 는 백그라운드 kubectl port-forward 를 시작하고 cancel 함수 반환.
// caller 는 deferred cancel 로 cleanup.
func portForward(svcName, namespace string, localPort, remotePort int) (cancel func(), err error) {
	cmd := exec.Command("kubectl", "-n", namespace, "port-forward",
		"svc/"+svcName,
		fmt.Sprintf("%d:%d", localPort, remotePort))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// readiness: TCP dial check up to 10s.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return nil, fmt.Errorf("port-forward to %s/%s not ready within 15s", namespace, svcName)
}

var _ = Describe("Real External System Round-Trip (cycle 17)", Ordered, func() {
	const externalNS = "external-systems"

	Context("Vault Transit (HashiCorp Vault dev mode)", func() {
		var cancel func()
		BeforeAll(func() {
			By("waiting for vault pod")
			out, _ := utils.Run(exec.Command("kubectl", "-n", externalNS, "wait", "--for=condition=ready",
				"pod", "-l", "app=vault", "--timeout=120s"))
			Expect(out).To(ContainSubstring("met"))
			By("port-forward 127.0.0.1:48200 → vault:8200")
			var err error
			cancel, err = portForward("vault", externalNS, 48200, 8200)
			Expect(err).NotTo(HaveOccurred())
		})
		AfterAll(func() {
			if cancel != nil {
				cancel()
			}
		})

		It("VaultClient.Health() succeeds against real Vault", func() {
			client, err := encryptionpkg.NewVaultClientFromSpec(
				&mongodbv1alpha1.VaultKMSConfig{Address: "http://127.0.0.1:48200", KeyName: "test"},
				"root-token-12345", nil)
			Expect(err).NotTo(HaveOccurred())
			ctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer ccancel()
			err = client.Health(ctx)
			Expect(err).NotTo(HaveOccurred(), "Vault Health real round-trip")
		})

		It("ProbeKMS(vault) succeeds against real Vault", func() {
			ctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer ccancel()
			err := encryptionpkg.ProbeKMS(ctx, &mongodbv1alpha1.EncryptionSpec{
				Enabled:     true,
				KeyProvider: "vault",
				KMSConfig: &mongodbv1alpha1.KMSConfigSpec{
					Vault: &mongodbv1alpha1.VaultKMSConfig{Address: "http://127.0.0.1:48200", KeyName: "mongo"},
				},
			}, "root-token-12345", nil)
			Expect(err).NotTo(HaveOccurred(), "ProbeKMS real round-trip")
		})

		It("VaultClient Encrypt-Decrypt round-trip (with transit engine enabled)", func() {
			By("enable transit engine + create key (test fixture)")
			cmd := exec.Command("kubectl", "-n", externalNS, "exec",
				"deployment/vault", "--",
				"sh", "-c",
				"VAULT_TOKEN=root-token-12345 vault secrets enable transit 2>/dev/null || true && VAULT_TOKEN=root-token-12345 vault write -f transit/keys/mongo-key 2>/dev/null || true")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			client, err := encryptionpkg.NewVaultClientFromSpec(
				&mongodbv1alpha1.VaultKMSConfig{Address: "http://127.0.0.1:48200", KeyName: "mongo-key"},
				"root-token-12345", nil)
			Expect(err).NotTo(HaveOccurred())

			ctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer ccancel()
			plaintext := []byte("real-mongodb-keyfile-data-32-bytes!!")
			ct, err := client.Encrypt(ctx, "mongo-key", plaintext)
			Expect(err).NotTo(HaveOccurred(), "Encrypt against real Vault")
			Expect(ct).To(HavePrefix("vault:v1:"), "Vault ciphertext format")

			decrypted, err := client.Decrypt(ctx, "mongo-key", ct)
			Expect(err).NotTo(HaveOccurred(), "Decrypt against real Vault")
			Expect(string(decrypted)).To(Equal(string(plaintext)), "round-trip data integrity")
		})
	})

	Context("OpenLDAP (real LDAP server, port 389)", func() {
		var cancel func()
		BeforeAll(func() {
			By("waiting for openldap pod")
			out, _ := utils.Run(exec.Command("kubectl", "-n", externalNS, "wait", "--for=condition=ready",
				"pod", "-l", "app=openldap", "--timeout=120s"))
			Expect(out).To(ContainSubstring("met"))
			By("port-forward 127.0.0.1:43890 → openldap:389")
			var err error
			cancel, err = portForward("openldap", externalNS, 43890, 389)
			Expect(err).NotTo(HaveOccurred())
		})
		AfterAll(func() {
			if cancel != nil {
				cancel()
			}
		})

		It("ProbeLDAP succeeds against real OpenLDAP", func() {
			ctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer ccancel()
			err := authpkg.ProbeLDAP(ctx, &mongodbv1alpha1.LDAPSpec{
				Servers: "127.0.0.1:43890",
				TLS:     false, // ldap port 389 plain
			})
			Expect(err).NotTo(HaveOccurred(), "real LDAP TCP connect")
		})
	})

	Context("Keycloak (real OIDC IdP)", func() {
		var cancel func()
		BeforeAll(func() {
			By("waiting for keycloak pod (long startup ~30-60s)")
			out, _ := utils.Run(exec.Command("kubectl", "-n", externalNS, "wait", "--for=condition=ready",
				"pod", "-l", "app=keycloak", "--timeout=180s"))
			Expect(out).To(ContainSubstring("met"))
			By("port-forward 127.0.0.1:48080 → keycloak:8080")
			var err error
			cancel, err = portForward("keycloak", externalNS, 48080, 8080)
			Expect(err).NotTo(HaveOccurred())
		})
		AfterAll(func() {
			if cancel != nil {
				cancel()
			}
		})

		It("Keycloak master realm OIDC discovery is reachable (HTTP, no TLS)", func() {
			// ProbeOIDC 는 https-only — 본 test 는 HTTP 모드 Keycloak 이므로 ProbeOIDC reject 됨.
			// 대신 raw HTTP GET 으로 discovery endpoint 도달성 검증.
			ctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer ccancel()
			cmd := exec.CommandContext(ctx, "curl", "-sS",
				"http://127.0.0.1:48080/realms/master/.well-known/openid-configuration")
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Keycloak OIDC discovery")
			Expect(out).To(ContainSubstring(`"issuer"`), "OIDC discovery JSON has issuer field")
			Expect(out).To(ContainSubstring(`"jwks_uri"`), "OIDC discovery JSON has jwks_uri")
			Expect(out).To(ContainSubstring(`"authorization_endpoint"`), "OIDC discovery JSON has auth endpoint")
		})

		It("ProbeOIDC rejects HTTP (defense in depth)", func() {
			ctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer ccancel()
			err := authpkg.ProbeOIDC(ctx, &mongodbv1alpha1.OIDCSpec{
				IssuerURL: "http://127.0.0.1:48080/realms/master",
			})
			Expect(err).To(HaveOccurred(), "ProbeOIDC must reject http")
			Expect(err.Error()).To(ContainSubstring("https"))
		})
	})

	_ = strings.Contains // utility import retention
})
