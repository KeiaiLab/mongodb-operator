//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// version_upgrade_test.go — Phase 1 M2 의 다섯 번째 (마지막) e2e 시나리오
// (iteration 14). MongoDB version upgrade rolling 회귀 가드.
//
// 목표:
//   - 8.0 → 8.2 → 8.3 rolling upgrade 시 STS template image 가 propagate
//     + Pod 가 새 image 로 재생성 + Phase=Running 복귀.
//   - iteration 9 (a8db040) 의 IsSupportedMongoDBVersion 화이트리스트 (8.0/8.2/8.3)
//     모두 admission 통과.
//   - mixed-version replica set 가 정상 동작 (rolling 중 일부 pod 8.0, 일부 8.2).
//
// valkey-operator iteration 7 의 version_upgrade_test.go 패턴 차용 (검증 — 가설
// A/B/C 회귀 가드 모두 적용).

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
	versionUpgradeNamespace = "mongodb-version-upgrade-e2e"
	versionUpgradeCRName    = "mdb-upgrade-test"
)

var _ = Describe("MongoDB Version Upgrade Rolling (Phase 1 M2 / iteration 14)", Ordered, func() {
	BeforeAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", versionUpgradeNamespace))
		ensureAdminSecret(versionUpgradeNamespace)

		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: %s
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
`, versionUpgradeCRName, versionUpgradeNamespace)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "MongoDB CR apply (8.0)")
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "mongodb",
			versionUpgradeCRName, "-n", versionUpgradeNamespace, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns",
			versionUpgradeNamespace, "--ignore-not-found"))
	})

	Context("초기 부트스트랩 8.0", func() {
		It("Phase=Running + STS image=mongo:8.0.x", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 5*time.Second).Should(Equal("Running"))

			stsImage, err := utils.Run(exec.Command("kubectl", "get", "sts",
				versionUpgradeCRName, "-n", versionUpgradeNamespace,
				"-o", "jsonpath={.spec.template.spec.containers[0].image}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(stsImage).To(HavePrefix("mongo:8.0"),
				"STS image 가 mongo:8.0.x prefix 여야 함 (8.0 화이트리스트 매칭)")
		})
	})

	Context("Patch 8.0 → 8.2 (rolling upgrade #1)", func() {
		It("spec.version.version=8.2 patch", func() {
			patch := `{"spec":{"version":{"version":"8.2"}}}`
			_, err := utils.Run(exec.Command("kubectl", "patch", "mongodb",
				versionUpgradeCRName, "-n", versionUpgradeNamespace,
				"--type=merge", "-p", patch))
			Expect(err).NotTo(HaveOccurred(), "8.0 → 8.2 patch")
		})

		// 가설 A 회귀 가드 — STS image field 가 새 tag 로 갱신.
		It("STS image 가 8.2 prefix 로 propagate", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}"))
				return out
			}, 90*time.Second, 5*time.Second).Should(HavePrefix("mongo:8.2"),
				"STS image 가 mongo:8.2.x 로 propagate (가설 A 회귀 가드)")
		})

		// 가설 C 회귀 가드 — Pod 가 실제로 새 image 로 재생성 (rolling, 마지막은 pod-0).
		It("모든 Pod 가 8.2 image 로 재생성 (rolling 완료)", func() {
			// rolling 은 ordinal 역순 (pod-2 → pod-1 → pod-0). 마지막 pod-0 이 8.2 면 완료.
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "pod",
					versionUpgradeCRName+"-0", "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.spec.containers[0].image}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(HavePrefix("mongo:8.2"),
				"pod-0 (rolling 마지막) 이 8.2 image 로 재생성 (가설 C 회귀 가드)")
		})

		// 가설 B 회귀 가드 — webhook / defaulter 가 spec 을 8.0 으로 되돌리지 않음.
		It("CR spec.version.version 이 8.2 보존", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.spec.version.version}"))
				return out
			}, 30*time.Second, 5*time.Second).Should(Equal("8.2"),
				"CR spec.version.version 이 8.0 으로 reverting 되지 않음 (가설 B 회귀 가드)")
		})

		It("Phase=Running 복귀 + 3 pods Ready", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(Equal("Running"))

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return strings.TrimSpace(out)
			}, 3*time.Minute, 5*time.Second).Should(Equal("3"))
		})
	})

	Context("Patch 8.2 → 8.3 (rolling upgrade #2)", func() {
		It("spec.version.version=8.3 patch", func() {
			patch := `{"spec":{"version":{"version":"8.3"}}}`
			_, err := utils.Run(exec.Command("kubectl", "patch", "mongodb",
				versionUpgradeCRName, "-n", versionUpgradeNamespace,
				"--type=merge", "-p", patch))
			Expect(err).NotTo(HaveOccurred(), "8.2 → 8.3 patch")
		})

		It("STS image 8.3 + 모든 Pod 8.3 + Phase=Running", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}"))
				return out
			}, 90*time.Second, 5*time.Second).Should(HavePrefix("mongo:8.3"))

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "pod",
					versionUpgradeCRName+"-0", "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.spec.containers[0].image}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(HavePrefix("mongo:8.3"))

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(Equal("Running"))
		})
	})

	Context("Unsupported version reject (iteration 9 화이트리스트 회귀)", func() {
		It("spec.version.version=7.0 patch — IsSupportedMongoDBVersion 거부 기대", func() {
			// iteration 9 의 IsSupportedMongoDBVersion 가 controller 또는 webhook
			// 단계에서 7.0 reject. webhook server 부재 시 controller condition 으로
			// error 표시 (M1 의 *pure validation function* 패턴).
			patch := `{"spec":{"version":{"version":"7.0"}}}`
			_, _ = utils.Run(exec.Command("kubectl", "patch", "mongodb",
				versionUpgradeCRName, "-n", versionUpgradeNamespace,
				"--type=merge", "-p", patch))

			// CR 의 status condition 또는 events 에 "unsupported version" 또는
			// 유사한 거부 메시지 — 또는 controller 가 reject 하여 STS image 변경 안 됨.
			// webhook server 부트스트랩 후 본 기대값 명시 (현재는 spec patch 자체가
			// CRD pattern 통과하므로 controller condition 검증).
			Consistently(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					versionUpgradeCRName, "-n", versionUpgradeNamespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}"))
				return out
			}, 30*time.Second, 5*time.Second).Should(HavePrefix("mongo:8.3"),
				"7.0 patch 에도 STS image 는 8.3 유지 (controller 거부)")
		})
	})
})
