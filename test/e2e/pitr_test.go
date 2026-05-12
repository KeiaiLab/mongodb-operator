//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// pitr_test.go — F05 (cycle 1) PITR e2e 시나리오.
//
// 본 e2e 는 *MongoDBBackup.Spec.Restore.PointInTime API path* 가 클러스터
// 에서 정합하게 인식되는지 검증. 실제 mongorestore --oplogReplay 명령
// 실행은 cycle 6+ (KMS encryption + ETag verify) 통합 시점에 강화 — 본
// cycle 의 acceptance 는:
//
//   1. MongoDBBackup CR 에 Spec.Restore.{SourceBackupName, PointInTime} 적용 가능
//   2. Status.Phase=Restoring 으로 진입
//   3. Status.StartTime 이 nil 이 아님
//
// cycle 6+ 후속 (TODO):
//   - 실제 oplog tailer sidecar (BuildOplogTailerSidecar) 가 mongod pod 에 함께
//     기동되는지 검증
//   - 시점 t1 에 데이터 insert → t2 에 추가 insert → t1 으로 PITR restore →
//     t1 시점 데이터만 보존되는지 round-trip 검증
//   - S3 oplog 객체가 정확한 timestamp 로 업로드되는지 검증

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
	pitrNamespace      = "mongodb-pitr-e2e"
	pitrSourceCRName   = "mdb-pitr-source"
	pitrBackupCRName   = "mdb-pitr-backup"
	pitrRestoreCRName  = "mdb-pitr-restore"
	pitrWaitDuration   = 3 * time.Minute
	pitrPollInterval   = 5 * time.Second
)

var _ = Describe("MongoDBBackup PITR API (cycle 1 / F01-F05)", Ordered, func() {
	BeforeAll(func() {
		By("creating PITR e2e namespace")
		cmd := exec.Command("kubectl", "create", "namespace", pitrNamespace)
		_, _ = utils.Run(cmd) // ignore AlreadyExists
	})

	AfterAll(func() {
		By("cleaning up PITR e2e namespace")
		cmd := exec.Command("kubectl", "delete", "namespace", pitrNamespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)
	})

	It("Restore CR with PointInTime is accepted by API server (CRD validation OK)", func() {
		By("apply MongoDBBackup restore CR with Spec.Restore set")
		manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: %s
  namespace: %s
spec:
  clusterRef:
    kind: MongoDB
    name: %s
  storage:
    type: pvc
  restore:
    sourceBackupName: %s
    pointInTime: "2026-05-12T00:00:00Z"
`, pitrRestoreCRName, pitrNamespace, pitrSourceCRName, pitrBackupCRName)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "kubectl apply output: %s", out)
	})

	It("Reconciler transitions Phase to Restoring", func() {
		By("polling Status.Phase until Restoring or timeout")
		Eventually(func() string {
			cmd := exec.Command("kubectl", "-n", pitrNamespace, "get", "mongodbbackup", pitrRestoreCRName,
				"-o", "jsonpath={.status.phase}")
			out, _ := utils.Run(cmd)
			return strings.TrimSpace(out)
		}, pitrWaitDuration, pitrPollInterval).Should(Equal("Restoring"))
	})

	It("Status.StartTime is set on entering Restoring", func() {
		cmd := exec.Command("kubectl", "-n", pitrNamespace, "get", "mongodbbackup", pitrRestoreCRName,
			"-o", "jsonpath={.status.startTime}")
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "startTime must be set when phase=Restoring")
	})

	// cycle 6+ 추가 예정 — 실제 mongorestore + oplogReplay round-trip 검증.
})
