//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// failover_test.go — Phase 1 M2 의 두 번째 e2e 시나리오 (iteration 11).
// MongoDB ReplicaSet 의 primary pod 강제 삭제 → 자동 step-down + 새 primary
// 선출 + 새 primary Ready + rs.status() 검증.

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
	failoverNamespace = "mongodb-failover-e2e"
	failoverCRName    = "mdb-failover-test"
)

var _ = Describe("MongoDB ReplicaSet Failover (Phase 1 M2 / iteration 11)", Ordered, func() {
	BeforeAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", failoverNamespace))
		ensureAdminSecret(failoverNamespace)

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
`, failoverCRName, failoverNamespace)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "MongoDB CR apply (3 members)")
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "mongodb",
			failoverCRName, "-n", failoverNamespace, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns",
			failoverNamespace, "--ignore-not-found"))
	})

	Context("초기 부트스트랩", func() {
		It("Phase=Running + Status.CurrentPrimary=pod-0", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					failoverCRName, "-n", failoverNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 5*time.Second).Should(Equal("Running"))

			out, err := utils.Run(exec.Command("kubectl", "get", "mongodb",
				failoverCRName, "-n", failoverNamespace,
				"-o", "jsonpath={.status.currentPrimary}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(out)).To(Equal(failoverCRName + "-0"),
				"초기 primary 는 pod-0 (replicaset bootstrap convention)")
		})

		It("3 pod 모두 Ready", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					failoverCRName, "-n", failoverNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return out
			}, 3*time.Minute, 5*time.Second).Should(Equal("3"))
		})
	})

	Context("Primary kill → 자동 failover", func() {
		It("primary pod (pod-0) 강제 삭제", func() {
			cmd := exec.Command("kubectl", "delete", "pod",
				failoverCRName+"-0", "-n", failoverNamespace,
				"--force", "--grace-period=0")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Status.CurrentPrimary 가 pod-1 또는 pod-2 로 변경", func() {
			// MongoDB replica set election 은 통상 12-30초. controller reconcile
			// interval (~10s) + status update lag 추가 마진.
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					failoverCRName, "-n", failoverNamespace,
					"-o", "jsonpath={.status.currentPrimary}"))
				return strings.TrimSpace(out)
			}, 3*time.Minute, 10*time.Second).ShouldNot(Equal(failoverCRName + "-0"),
				"primary 는 pod-0 외 다른 pod 로 변경되어야 함")
		})

		It("새 primary pod Ready", func() {
			out, err := utils.Run(exec.Command("kubectl", "get", "mongodb",
				failoverCRName, "-n", failoverNamespace,
				"-o", "jsonpath={.status.currentPrimary}"))
			Expect(err).NotTo(HaveOccurred())
			newPrimary := strings.TrimSpace(out)
			Expect(newPrimary).NotTo(BeEmpty(), "newPrimary pod name 가 비어있으면 안됨")

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "pod",
					newPrimary, "-n", failoverNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}"))
				return out
			}, 2*time.Minute, 5*time.Second).Should(Equal("True"))
		})

		It("rs.status() 가 새 primary 를 PRIMARY state 로 인식", func() {
			out, err := utils.Run(exec.Command("kubectl", "get", "mongodb",
				failoverCRName, "-n", failoverNamespace,
				"-o", "jsonpath={.status.currentPrimary}"))
			Expect(err).NotTo(HaveOccurred())
			newPrimary := strings.TrimSpace(out)

			// mongo shell 또는 mongosh 호출. mongo 8.x 는 mongosh 만 제공.
			// rs.status().members[?] 가 PRIMARY (state=1) 인지 검증.
			// cycle 14: auth required — admin credentials 명시.
			rsStatus, err := utils.Run(exec.Command("kubectl", "exec", "-n",
				failoverNamespace, newPrimary, "--",
				"mongosh", "--quiet",
				"-u", "admin", "-p", "changeme123",
				"--authenticationDatabase", "admin",
				"--eval",
				"JSON.stringify(rs.status().members.find(m => m.stateStr === 'PRIMARY'))"))
			Expect(err).NotTo(HaveOccurred(),
				"mongosh rs.status() 호출 실패 — pod 내 mongosh 부재 또는 인증 실패 가능")
			Expect(rsStatus).To(ContainSubstring(newPrimary),
				"PRIMARY member name 이 currentPrimary 와 일치해야 함")
		})
	})
})
