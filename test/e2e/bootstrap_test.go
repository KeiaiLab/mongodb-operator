//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// bootstrap_test.go — Phase 1 M2 의 첫 e2e 시나리오.
// MongoDB ReplicaSet (3 members) 가 Phase=Running 까지 도달하는지 검증.

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/keiailab/mongodb-operator/test/utils"
)

const (
	bootstrapNamespace = "mongodb-bootstrap-e2e"
	bootstrapCRName    = "mdb-bootstrap-test"
)

var _ = Describe("MongoDB ReplicaSet Bootstrap (Phase 1 M2)", Ordered, func() {
	BeforeAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", bootstrapNamespace))
		ensureAdminSecret(bootstrapNamespace)

		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: %s
  namespace: %s
spec:
  members: 3
  version:
    version: "8.3"
  storage:
    size: 1Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mdb-admin
`, bootstrapCRName, bootstrapNamespace)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "MongoDB CR apply (3 members)")
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "mongodb",
			bootstrapCRName, "-n", bootstrapNamespace, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns",
			bootstrapNamespace, "--ignore-not-found"))
	})

	Context("초기 부트스트랩 — ReplicaSet 3 members", func() {
		It("Phase=Running + STS image=mongo:8.3.x + 3 pods Ready", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					bootstrapCRName, "-n", bootstrapNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 5*time.Second).Should(Equal("Running"))

			// STS image 검증 — version 화이트리스트 (iteration 9 a8db040) 정합.
			stsImage, err := utils.Run(exec.Command("kubectl", "get", "sts",
				bootstrapCRName, "-n", bootstrapNamespace,
				"-o", "jsonpath={.spec.template.spec.containers[0].image}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(stsImage).To(HavePrefix("mongo:8.3"),
				"STS image 가 mongo:8.3.x 로 시작해야 함 (IsSupportedMongoDBVersion 8.3 매칭)")

			// 3 pods Ready 검증.
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					bootstrapCRName, "-n", bootstrapNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(Equal("3"))
		})
	})
})
