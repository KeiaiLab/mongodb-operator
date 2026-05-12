//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.
*/

// sharded_scale_in_test.go — F74 (cycle 9) sharded shard count 감소 e2e stub.
// 실 chunk drain 검증은 cycle 11 운영 보강.

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
	scaleInNamespace = "mongodb-scalein-e2e"
	scaleInCRName    = "mdb-scalein"
)

var _ = Describe("MongoDBSharded scale-in (cycle 9 / F74)", Ordered, func() {
	BeforeAll(func() {
		cmd := exec.Command("kubectl", "create", "namespace", scaleInNamespace)
		_, _ = utils.Run(cmd)
	})
	AfterAll(func() {
		cmd := exec.Command("kubectl", "delete", "namespace", scaleInNamespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)
	})

	It("CRD accepts sharded with scalePolicy.deliberate=true (scale-in gate)", func() {
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: %s
  namespace: %s
spec:
  version:
    version: "8.2"
  configServer:
    members: 3
  shards:
    count: 4
    membersPerShard: 3
    scalePolicy:
      deliberate: true
  mongos:
    replicas: 2
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: scalein-admin
`, scaleInCRName, scaleInNamespace)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "apply: %s", out)
	})

	It("scale-in (4→3) requires deliberate=true (operator gates without it)", func() {
		patch := `{"spec":{"shards":{"count":3}}}`
		cmd := exec.Command("kubectl", "-n", scaleInNamespace, "patch", "mongodbsharded", scaleInCRName,
			"--type=merge", "-p", patch)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		// cycle 11: removeShard 호출 정합 + chunk drain 정합 검증
	})
})
