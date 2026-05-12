//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// sharded_test.go — Phase 1 M2 의 세 번째 e2e 시나리오 (iteration 12).
// MongoDBSharded CR 의 다음 영역 회귀 가드:
//   - 3 shard + 3 config + 3 mongos 부트스트랩 → Phase=Running
//   - shard scale up (3 → 4): spec.shards.count patch + ScalePolicy.Deliberate
//   - shard scale down (4 → 3): 동일
//   - mongos drift fix 회귀 가드 (HANDOFF iteration 6 — mongos pod restart 후 sharded
//     cluster 가 정상 reconcile)

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
	shardedNamespace = "mongodb-sharded-e2e"
	shardedCRName    = "mdb-sharded-test"
)

var _ = Describe("MongoDBSharded Topology (Phase 1 M2 / iteration 12)", Ordered, func() {
	BeforeAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", shardedNamespace))
		ensureAdminSecret(shardedNamespace)

		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: %s
  namespace: %s
spec:
  version:
    version: "8.3"
  configServer:
    members: 3
    storage:
      size: 1Gi
  shards:
    count: 3
    membersPerShard: 3
    storage:
      size: 1Gi
    scalePolicy:
      deliberate: true
  mongos:
    replicas: 3
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mdb-admin
`, shardedCRName, shardedNamespace)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "MongoDBSharded CR apply (3 shard + 3 cfg + 3 mongos)")
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "mongodbsharded",
			shardedCRName, "-n", shardedNamespace, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns",
			shardedNamespace, "--ignore-not-found"))
	})

	Context("초기 부트스트랩 — 3 shard + 3 cfg + 3 mongos", func() {
		It("Phase=Running 도달", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbsharded",
					shardedCRName, "-n", shardedNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 10*time.Minute, 10*time.Second).Should(Equal("Running"),
				"sharded cluster bootstrap 은 cfg 3 + shard 3x3 + mongos 3 = 15 pod, 10 분 timeout")
		})

		It("ConfigServer STS Ready (3 replicas)", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					shardedCRName+"-cfg", "-n", shardedNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return strings.TrimSpace(out)
			}, 5*time.Minute, 10*time.Second).Should(Equal("3"))
		})

		It("3 Shard STS 가 각각 3 replicas Ready", func() {
			for i := 0; i < 3; i++ {
				stsName := fmt.Sprintf("%s-shard-%d", shardedCRName, i)
				Eventually(func() string {
					out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
						stsName, "-n", shardedNamespace,
						"-o", "jsonpath={.status.readyReplicas}"))
					return strings.TrimSpace(out)
				}, 5*time.Minute, 10*time.Second).Should(Equal("3"),
					fmt.Sprintf("%s 의 readyReplicas=3 기대", stsName))
			}
		})

		It("Mongos Deployment Ready (3 replicas)", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "deploy",
					shardedCRName+"-mongos", "-n", shardedNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return strings.TrimSpace(out)
			}, 5*time.Minute, 10*time.Second).Should(Equal("3"))
		})
	})

	Context("Shard scale up 3 → 4", func() {
		It("spec.shards.count patch", func() {
			patch := `{"spec":{"shards":{"count":4}}}`
			_, err := utils.Run(exec.Command("kubectl", "patch", "mongodbsharded",
				shardedCRName, "-n", shardedNamespace,
				"--type=merge", "-p", patch))
			Expect(err).NotTo(HaveOccurred(), "scale-up patch")
		})

		It("새 shard STS (shard-3) Ready (3 replicas)", func() {
			stsName := shardedCRName + "-shard-3"
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					stsName, "-n", shardedNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return strings.TrimSpace(out)
			}, 5*time.Minute, 10*time.Second).Should(Equal("3"))
		})

		It("Phase 다시 Running (drift 해소)", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbsharded",
					shardedCRName, "-n", shardedNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(Equal("Running"))
		})
	})

	Context("Shard scale down 4 → 3", func() {
		It("spec.shards.count patch", func() {
			patch := `{"spec":{"shards":{"count":3}}}`
			_, err := utils.Run(exec.Command("kubectl", "patch", "mongodbsharded",
				shardedCRName, "-n", shardedNamespace,
				"--type=merge", "-p", patch))
			Expect(err).NotTo(HaveOccurred(), "scale-down patch")
		})

		It("shard-3 STS 제거됨", func() {
			stsName := shardedCRName + "-shard-3"
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					stsName, "-n", shardedNamespace,
					"-o", "jsonpath={.metadata.name}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(BeEmpty(),
				"shard-3 STS 가 cascade delete 되어야 함")
		})

		It("Mongos drift fix 회귀 가드 — Pod 재생성 후에도 Phase=Running 유지", func() {
			// HANDOFF iteration 6: mongos pod 강제 삭제 → controller 가 즉시
			// 재생성하고 sharded cluster 가 Phase=Running 유지해야 함 (drift fix).
			cmd := exec.Command("kubectl", "delete", "pod",
				"-l", "app.kubernetes.io/component=mongos",
				"-n", shardedNamespace,
				"--force", "--grace-period=0")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "deploy",
					shardedCRName+"-mongos", "-n", shardedNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return strings.TrimSpace(out)
			}, 3*time.Minute, 10*time.Second).Should(Equal("3"))

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbsharded",
					shardedCRName, "-n", shardedNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 3*time.Minute, 10*time.Second).Should(Equal("Running"))
		})
	})
})
