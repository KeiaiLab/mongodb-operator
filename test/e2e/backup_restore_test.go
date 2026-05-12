//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// backup_restore_test.go — Phase 1 M2 의 네 번째 e2e 시나리오 (iteration 13).
//
// 현재 mongodb-operator 의 backup 표면:
//   - MongoDBBackup CR — Spec.{ClusterRef, Storage, Type, Compression*}
//   - Storage.Type=s3|pvc — 본 e2e 는 PVC 만 (S3 minio sidecar 는 별 iteration)
//   - Status.Phase enum: Pending|Running|Completed|Failed
//
// MongoDBRestore CR 은 *현재 부재* — restore 시나리오는 향후 ROADMAP 에 등록.
// 본 e2e 는 *backup-only* round-trip 검증.
//
// 후속 (별 iteration):
//   - S3 backup (minio sidecar 모킹) — endpoint / accessKeySecret 검증
//   - MongoDBRestore CR 도입 + restore round-trip e2e
//   - PITR (oplog replay) e2e

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
	backupNamespace      = "mongodb-backup-e2e"
	backupSourceCRName   = "mdb-backup-source"
	backupCRName         = "mdb-backup-pvc"
)

var _ = Describe("MongoDBBackup PVC Round-Trip (Phase 1 M2 / iteration 13)", Ordered, func() {
	BeforeAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", backupNamespace))
		ensureAdminSecret(backupNamespace)

		// 1. Source MongoDB ReplicaSet (3 members) 부트스트랩.
		sourceManifest := fmt.Sprintf(`
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
`, backupSourceCRName, backupNamespace)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(sourceManifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "source MongoDB CR apply")
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "mongodbbackup",
			backupCRName, "-n", backupNamespace, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "mongodb",
			backupSourceCRName, "-n", backupNamespace, "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns",
			backupNamespace, "--ignore-not-found"))
	})

	Context("Source ReplicaSet 부트스트랩", func() {
		It("Phase=Running + STS readyReplicas=3", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					backupSourceCRName, "-n", backupNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 5*time.Second).Should(Equal("Running"))

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					backupSourceCRName, "-n", backupNamespace,
					"-o", "jsonpath={.status.readyReplicas}"))
				return strings.TrimSpace(out)
			}, 3*time.Minute, 5*time.Second).Should(Equal("3"))
		})

		It("dummy data insert (testdb.col 1 doc)", func() {
			// primary pod 에 mongosh 로 testdb.col 에 문서 1개 insert.
			// 본 데이터는 backup → 후속 iteration 의 restore 시 비교 ground truth.
			cmd := exec.Command("kubectl", "exec", "-n",
				backupNamespace, backupSourceCRName+"-0", "--",
				"mongosh", "--quiet", "--eval",
				"db.getSiblingDB('testdb').col.insertOne({_id: 'e2e-it13', value: 'backup-source'})")
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "insertOne 실행 실패: "+out)
			Expect(out).To(ContainSubstring("acknowledged"))
		})
	})

	Context("MongoDBBackup CR (PVC type) → Phase=Completed", func() {
		It("MongoDBBackup CR 생성 (storage.type=pvc, size=2Gi)", func() {
			backupManifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: %s
  namespace: %s
spec:
  clusterRef:
    name: %s
    kind: MongoDB
  storage:
    type: pvc
    pvc:
      size: 2Gi
  type: full
  compression: true
  compressionType: zstd
`, backupCRName, backupNamespace, backupSourceCRName)
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(backupManifest)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "MongoDBBackup CR apply")
		})

		It("Status.Phase=Completed (5 분 timeout)", func() {
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbbackup",
					backupCRName, "-n", backupNamespace,
					"-o", "jsonpath={.status.phase}"))
				return out
			}, 5*time.Minute, 10*time.Second).Should(Equal("Completed"),
				"backup Phase 가 Completed 까지 도달해야 함 (Pending/Running/Failed 아님)")
		})

		It("Status.CompletionTime + Status.Size 채워짐", func() {
			completionTime, err := utils.Run(exec.Command("kubectl", "get", "mongodbbackup",
				backupCRName, "-n", backupNamespace,
				"-o", "jsonpath={.status.completionTime}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(completionTime)).NotTo(BeEmpty(),
				"completionTime 가 set 되어야 함")

			size, err := utils.Run(exec.Command("kubectl", "get", "mongodbbackup",
				backupCRName, "-n", backupNamespace,
				"-o", "jsonpath={.status.size}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(size)).NotTo(BeEmpty(),
				"size 가 set 되어야 함 (zstd 압축 후 byte)")
		})

		It("backup PVC 가 namespace 에 생성됨", func() {
			// PVC 명명 convention: <backup-name> 또는 <source>-<backup-name>.
			// 명세 확정 시 정확 이름 검증, 현재는 *적어도 1 PVC 가 backup label* 보유 검증.
			out, err := utils.Run(exec.Command("kubectl", "get", "pvc",
				"-n", backupNamespace,
				"-l", "app.kubernetes.io/component=backup",
				"-o", "jsonpath={.items[0].metadata.name}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(out)).NotTo(BeEmpty(),
				"backup component label 의 PVC 가 적어도 1개 존재해야 함")
		})
	})

	// Restore CR 도입 후 별 iteration 에서 restore round-trip Context 추가.
	// 현재는 backup-only — 데이터 보존성은 backup PVC 의 binary 로 확인.
})
