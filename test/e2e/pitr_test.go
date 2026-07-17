//go:build e2e
// +build e2e

/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// pitr_test.go — PITR (시점복원) e2e 계약.
//
// 본 파일은 두 층으로 구성된다:
//
//  1. "PITR API" — Spec.Restore.{SourceBackupName, PointInTime} 이 API server
//     에 수용되고 Phase=Restoring 으로 진입하는지 (표면 검증).
//  2. "PITR round-trip" — **PITR 완성의 정의**. base 백업 이후 t1 에 doc-A,
//     t2 에 doc-B 를 넣고 PIT=t1+1s 로 복원했을 때 *doc-A 는 살아있고 doc-B 는
//     사라져야* 한다. 이 단언이 통과하지 않으면 PITR 은 "이름뿐" 이다 —
//     CRD 와 문서가 지원을 표기하더라도.
//
// 아키텍처 전제 (oplog tailer 사이드카 = S3 직접 스트리밍):
//   - tailer 는 mongod pod 에 함께 배치되어 local.oplog.rs 를 증분 tail 하고
//     세그먼트를 S3 (`<prefix>/<cluster>/oplog/<startTs>_<endTs>.bson.gz`) 로
//     직접 올린다. EmptyDir 경유가 없으므로 oplog 의 도달점은 S3 뿐이다 →
//     본 e2e 는 in-cluster MinIO 를 S3 엔드포인트로 띄운다 (pitrDeployMinIO).
//   - PITR 은 ReplicaSet 전용 — sharded 는 shard 별 oplog ts 가 독립이라 단일
//     PIT 로 cluster-wide 일관 시점을 정의할 수 없다 (RestoreSpec godoc 의
//     "PITR 제약" 절). 본 e2e 도 RS 만 다룬다.
//
// **현 시점 RED (의도된 것)**: restore 완료 전이(Completed/Failed) 미배선 +
// restore job 의 S3 oplog fetch 부재 + base 백업의 `--oplog` 누락 → round-trip
// Context 후반 It 은 실패한다. 이 테스트를 통과시키려고 단언을 약화하지 말 것.
//
// 실행: `make test-e2e` (kind 필요). CI 미실행.

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/keiailab/mongodb-operator/test/utils"
)

const (
	pitrNamespace     = "mongodb-pitr-e2e"
	pitrSourceCRName  = "mdb-pitr-source"
	pitrBackupCRName  = "mdb-pitr-backup"
	pitrRestoreCRName = "mdb-pitr-restore"
	pitrWaitDuration  = 3 * time.Minute
	pitrPollInterval  = 5 * time.Second

	// --- PITR round-trip 전용 --------------------------------------------
	// API stub 과 namespace 를 분리한다: stub 의 restore CR 은 존재하지 않는
	// source 를 참조하므로 그 namespace 에서 reconciler 가 계속 실패한다.
	// round-trip 은 깨끗한 namespace 에서 관측되어야 한다.
	pitrRTNamespace   = "mongodb-pitr-rt-e2e"
	pitrRTClusterName = "mdb-pitr-rt"
	pitrRTBaseBackup  = "mdb-pitr-rt-base"
	pitrRTRestoreName = "mdb-pitr-rt-restore"
	pitrRTBucket      = "mongodb-pitr-e2e"
	pitrRTS3Secret    = "pitr-s3-credentials"
	pitrRTS3AccessKey = "minioadmin"
	pitrRTS3SecretKey = "minioadmin123"

	// pitrRTDB / pitrRTColl — insert 대상. oplog entry 의 ns 와 일치해야 한다.
	pitrRTDB   = "testdb"
	pitrRTColl = "col"
)

// minioImage — round-trip 의 S3 엔드포인트. MINIO_IMAGE 로 override 가능
// (managerImage 의 IMG override 관례 차용). e2e 는 로컬 kind 전용이라 고정
// RELEASE 태그 pin 대신 quay latest 를 기본으로 둔다 — 죽은 태그로 인한
// 인프라 실패가 테스트 실패보다 진단하기 나쁘기 때문.
var minioImage = func() string {
	if v := os.Getenv("MINIO_IMAGE"); v != "" {
		return v
	}
	return "quay.io/minio/minio:latest"
}()

// oplogTSPattern — EJSON canonical Timestamp 의 초(t) 추출.
// `{"$timestamp":{"t":1752710400,"i":1}}` 에서 t 만 뽑는다 (`"$timestamp"` 는
// 따옴표 때문에 `"t"` 에 매치되지 않는다).
var oplogTSPattern = regexp.MustCompile(`"t"\s*:\s*(\d+)`)

// mongoEval 은 pod 안의 mongosh 로 js 를 실행하고 stdout 을 trim 하여 반환한다.
// 인증 파라미터는 ensureAdminSecret 의 admin/changeme123 과 일치.
func mongoEval(ns, pod, js string) string {
	cmd := exec.Command("kubectl", "exec", "-n", ns, pod, "--",
		"mongosh", "--quiet",
		"-u", "admin", "-p", "changeme123",
		"--authenticationDatabase", "admin",
		"--eval", js)
	out, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "mongosh eval 실패\njs: %s\nout: %s", js, out)
	return strings.TrimSpace(out)
}

// oplogSec 는 testdb.col 의 _id=id insert 가 oplog 에 기록된 timestamp 의 초(t).
//
// **벽시계(time.Now) 를 쓰지 않는 이유**: PIT 는 oplog timestamp 공간에서
// 해석된다 (mongorestore --oplogLimit). 테스트 러너의 시계와 mongod 노드의
// 시계가 어긋나면 PIT 경계가 밀려 doc-A 가 잘려나가거나 doc-B 가 살아남는
// flaky 가 된다. insert 자신이 oplog 에 남긴 ts 를 ground truth 로 삼으면
// 시계 편차와 무관해진다 — 복원이 보는 것과 정확히 같은 좌표계다.
//
// ts 는 EJSON canonical (`{"$timestamp":{"t":..,"i":..}}`) 로 뽑는다. mongosh
// 의 Timestamp 프로퍼티 접근(.t/.i)은 bson 드라이버 버전에 따라 표면이 변하지만
// $timestamp extended JSON 은 스펙 고정이다.
func oplogSec(ns, pod, id string) int64 {
	js := fmt.Sprintf(
		`EJSON.stringify(db.getSiblingDB('local').oplog.rs.findOne({ns:'%s.%s','o._id':'%s'}).ts)`,
		pitrRTDB, pitrRTColl, id)
	out := mongoEval(ns, pod, js)

	m := oplogTSPattern.FindStringSubmatch(out)
	ExpectWithOffset(1, m).NotTo(BeNil(), "oplog ts 파싱 실패 (id=%s): %s", id, out)

	sec, err := strconv.ParseInt(m[1], 10, 64)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "oplog ts 초 변환 실패: %s", m[1])
	return sec
}

// insertDoc 은 testdb.col 에 문서 1개를 insert 한다 (acknowledged 확인).
func insertDoc(ns, pod, id string) {
	out := mongoEval(ns, pod, fmt.Sprintf(
		`db.getSiblingDB('%s').%s.insertOne({_id:'%s',v:'%s'})`,
		pitrRTDB, pitrRTColl, id, id))
	ExpectWithOffset(1, out).To(ContainSubstring("acknowledged"), "insertOne 미확인: %s", out)
}

// countDoc 은 testdb.col 의 _id=id 문서 수를 문자열로 반환 ("1" / "0").
//
// utils.Run 은 CombinedOutput 이라 mongosh 의 stderr 경고가 값에 섞일 수 있다.
// 값은 항상 마지막 줄이므로 그 줄만 취한다 — 완화책으로 ContainSubstring 을
// 쓰면 "1" 이 "10" 에도 매치되어 단언이 무의미해진다.
func countDoc(ns, pod, id string) string {
	out := mongoEval(ns, pod, fmt.Sprintf(
		`db.getSiblingDB('%s').%s.countDocuments({_id:'%s'})`,
		pitrRTDB, pitrRTColl, id))
	lines := utils.GetNonEmptyLines(out)
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// backupStatusUnix 는 MongoDBBackup status 의 RFC3339 시각 필드를 epoch 초로
// 반환한다. 미설정 / 미파싱이면 ok=false.
func backupStatusUnix(ns, name, field string) (int64, bool) {
	out, err := utils.Run(exec.Command("kubectl", "get", "mongodbbackup", name,
		"-n", ns, "-o", "jsonpath={.status."+field+"}"))
	if err != nil {
		return 0, false
	}
	v := strings.TrimSpace(out)
	if v == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// pitrRTS3Block 은 BackupStorageSpec 의 S3 stanza 를 주어진 들여쓰기로 렌더한다.
// MongoDB.spec.backup.storage (깊음) 와 MongoDBBackup.spec.storage (얕음) 두
// 위치에서 동일 버킷을 가리켜야 하므로 한 곳에서 생성한다.
func pitrRTS3Block(indent string) string {
	lines := []string{
		"type: s3",
		"s3:",
		"  bucket: " + pitrRTBucket,
		fmt.Sprintf("  endpoint: http://minio.%s.svc:9000", pitrRTNamespace),
		"  region: us-east-1",
		"  prefix: pitr/",
		"  credentialsRef:",
		"    name: " + pitrRTS3Secret,
		"  insecureSkipTLS: true",
	}
	return indent + strings.Join(lines, "\n"+indent)
}

// pitrDeployMinIO 는 round-trip namespace 에 S3 엔드포인트(MinIO) 를 띄운다.
//
// 버킷은 mc 사이드카 없이 데이터 디렉터리의 최상위 폴더로 사전 생성한다 —
// MinIO 는 `/data/<name>` 를 버킷으로 인식하므로 e2e 에 별도 프로비저닝
// 스텝이 필요 없다.
func pitrDeployMinIO(ns string) {
	manifest := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  access-key: %s
  secret-key: %s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: minio
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: minio
  template:
    metadata:
      labels:
        app: minio
    spec:
      containers:
        - name: minio
          image: %s
          command:
            - sh
            - -c
            - mkdir -p /data/%s && exec minio server /data --address :9000 --console-address :9001
          env:
            - name: MINIO_ROOT_USER
              value: %s
            - name: MINIO_ROOT_PASSWORD
              value: %s
          ports:
            - containerPort: 9000
          readinessProbe:
            httpGet:
              path: /minio/health/ready
              port: 9000
            initialDelaySeconds: 5
            periodSeconds: 5
          volumeMounts:
            - name: data
              mountPath: /data
      volumes:
        - name: data
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: minio
  namespace: %s
spec:
  selector:
    app: minio
  ports:
    - port: 9000
      targetPort: 9000
`, pitrRTS3Secret, ns, pitrRTS3AccessKey, pitrRTS3SecretKey,
		ns, minioImage, pitrRTBucket, pitrRTS3AccessKey, pitrRTS3SecretKey, ns)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "MinIO apply 실패: %s", out)

	out, err = utils.Run(exec.Command("kubectl", "wait", "deployment/minio",
		"-n", ns, "--for=condition=Available", "--timeout=3m"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "MinIO Available 대기 실패: %s", out)
}

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

	// ---------------------------------------------------------------------
	// PITR round-trip — **PITR 완성의 정의** (acceptance).
	//
	// base 백업 → doc-A insert(t1) → 3s → doc-B insert(t2) → PIT=t1+1s 복원
	// → doc-A 존재 ∧ doc-B 부재.
	//
	// `mongorestore --oplogLimit` 은 **배타적**이다 (ts < limit 만 replay).
	// PIT=t1+1s 이면 doc-A(t1 < t1+1) 는 포함되고 doc-B(t2 >= t1+2) 는 배제된다.
	//
	// 복원은 원본 RS 로 in-place 수행한다 — restore job 이 `--drop` 을 쓰므로
	// base 덤프에 들어있던 testdb.col 이 먼저 drop 되고, 그 위에 base + oplog
	// replay 가 얹힌다. 따라서 t2 의 doc-B 는 살아남을 수 없다.
	// ---------------------------------------------------------------------
	Context("PITR round-trip", Ordered, func() {
		var (
			primaryPod = pitrRTClusterName + "-0"

			// insert 가 oplog 에 남긴 ts 의 초. 벽시계가 아니라 이 값이
			// PIT 계산의 ground truth 다 (oplogSec godoc 참조).
			tASec int64
			tBSec int64
		)

		BeforeAll(func() {
			By("creating PITR round-trip namespace + admin secret")
			_, _ = utils.Run(exec.Command("kubectl", "create", "ns", pitrRTNamespace))
			ensureAdminSecret(pitrRTNamespace)

			By("deploying in-cluster MinIO as the S3 endpoint for oplog segments")
			pitrDeployMinIO(pitrRTNamespace)

			By("creating source MongoDB ReplicaSet with PITR enabled")
			// spec.backup.{enabled,pitrEnabled,oplogRetentionHours} = oplog
			// tailer 사이드카 주입 조건 (IsOplogTailerEnabled).
			// spec.backup.storage 는 CRD required — tailer 가 세그먼트를 올릴
			// S3 좌표이기도 하다.
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
  backup:
    enabled: true
    pitrEnabled: true
    oplogRetentionHours: 24
    storage:
%s
`, pitrRTClusterName, pitrRTNamespace, pitrRTS3Block("      "))
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(manifest)
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "source MongoDB CR apply: %s", out)

			By("waiting for Phase=Running + STS readyReplicas=3")
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodb",
					pitrRTClusterName, "-n", pitrRTNamespace, "-o", "jsonpath={.status.phase}"))
				return strings.TrimSpace(out)
			}, 5*time.Minute, pitrPollInterval).Should(Equal("Running"))

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "sts",
					pitrRTClusterName, "-n", pitrRTNamespace, "-o", "jsonpath={.status.readyReplicas}"))
				return strings.TrimSpace(out)
			}, 3*time.Minute, pitrPollInterval).Should(Equal("3"))
		})

		AfterAll(func() {
			By("cleaning up PITR round-trip namespace")
			_, _ = utils.Run(exec.Command("kubectl", "delete", "ns",
				pitrRTNamespace, "--ignore-not-found", "--wait=false"))
		})

		It("1. doc-0 seed insert (base 백업 이전 baseline)", func() {
			insertDoc(pitrRTNamespace, primaryPod, "doc-0")
			Expect(countDoc(pitrRTNamespace, primaryPod, "doc-0")).To(Equal("1"))
		})

		It("2. base 백업 CR → Phase=Completed", func() {
			// compressionType=gzip — restore job 이 `--gzip` 으로 아카이브를
			// 읽으므로 zstd 를 쓰면 복원 단계에서 깨진다.
			manifest := fmt.Sprintf(`
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: %s
  namespace: %s
spec:
  clusterRef:
    name: %s
    kind: MongoDB
  type: full
  compression: true
  compressionType: gzip
  storage:
%s
`, pitrRTBaseBackup, pitrRTNamespace, pitrRTClusterName, pitrRTS3Block("    "))
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(manifest)
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "base backup CR apply: %s", out)

			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbbackup",
					pitrRTBaseBackup, "-n", pitrRTNamespace, "-o", "jsonpath={.status.phase}"))
				return strings.TrimSpace(out)
			}, 5*time.Minute, 10*time.Second).Should(Equal("Completed"),
				"base 백업이 Completed 까지 도달해야 함")
		})

		It("2b. base 백업이 Status.OplogStart (replay 하한) 을 기록함", func() {
			// mongodump --oplog 가 남긴 일관 시점. 이 값이 없으면 oplog replay
			// 의 하한을 알 수 없어 silent gap 이 생긴다 — PITR 이 "이름뿐" 이
			// 되는 첫 번째 지점이다.
			_, ok := backupStatusUnix(pitrRTNamespace, pitrRTBaseBackup, "oplogStart")
			Expect(ok).To(BeTrue(),
				"base 백업의 status.oplogStart 가 RFC3339 로 기록되어야 함 (mongodump --oplog)")
		})

		It("3. doc-A insert → oplog ts(t1) 캡처", func() {
			insertDoc(pitrRTNamespace, primaryPod, "doc-A")
			tASec = oplogSec(pitrRTNamespace, primaryPod, "doc-A")
			Expect(tASec).To(BeNumerically(">", 0), "doc-A 의 oplog ts 가 잡혀야 함")
		})

		It("4. 3s 후 doc-B insert → oplog ts(t2), t2-t1 >= 2s 분리 보장", func() {
			// 동기화용 sleep 이 아니라 *시간 분리를 만드는* sleep 이다 —
			// 같은 초 안의 두 write 는 초 단위 PIT 로 구분할 수 없다.
			// 분리가 실제로 일어났는지는 아래 가드가 확인한다 (가정 금지).
			time.Sleep(3 * time.Second)

			insertDoc(pitrRTNamespace, primaryPod, "doc-B")
			tBSec = oplogSec(pitrRTNamespace, primaryPod, "doc-B")

			Expect(tBSec-tASec).To(BeNumerically(">=", 2),
				"doc-A(t=%d) 와 doc-B(t=%d) 가 2초 이상 떨어져야 PIT 로 가를 수 있음", tASec, tBSec)
		})

		It("5. flush gate — oplog 세그먼트가 t2 까지 S3 에 도달 (status.latestRestore >= t2)", func() {
			// 사이드카는 배치 주기로 세그먼트를 올린다. 이 게이트 없이 곧바로
			// 복원하면 doc-A 를 담은 세그먼트가 아직 S3 에 없어 replay 가
			// 비고, "doc-A 부재" 로 실패하는 flaky 가 된다.
			//
			// 관측점은 S3 를 직접 뒤지지 않고 base 백업 CR 의
			// status.latestRestore (uploader 가 최신 세그먼트의 endTs 로 갱신)
			// 를 쓴다 — 복원 가능 window 배선까지 같이 검증된다.
			Eventually(func() int64 {
				sec, ok := backupStatusUnix(pitrRTNamespace, pitrRTBaseBackup, "latestRestore")
				if !ok {
					return -1
				}
				return sec
			}, 5*time.Minute, pitrPollInterval).Should(BeNumerically(">=", tBSec),
				"latestRestore 가 doc-B 시점(t=%d) 이상으로 전진해야 함", tBSec)
		})

		It("6. 복원 CR 적용 (PIT = t1+1s, in-place)", func() {
			pit := time.Unix(tASec+1, 0).UTC().Format(time.RFC3339)
			By("restoring to PIT=" + pit)

			manifest := fmt.Sprintf(`
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
%s
  restore:
    sourceBackupName: %s
    pointInTime: "%s"
`, pitrRTRestoreName, pitrRTNamespace, pitrRTClusterName,
				pitrRTS3Block("    "), pitrRTBaseBackup, pit)
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(manifest)
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "restore CR apply: %s", out)
		})

		It("7. 복원 Phase=Completed", func() {
			// 현재 RED: restore 경로에 Completed/Failed 전이가 배선되지 않아
			// Restoring 에서 멈춘다.
			Eventually(func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "mongodbbackup",
					pitrRTRestoreName, "-n", pitrRTNamespace, "-o", "jsonpath={.status.phase}"))
				return strings.TrimSpace(out)
			}, 5*time.Minute, 10*time.Second).Should(Equal("Completed"),
				"restore 가 Completed 까지 도달해야 함 (Restoring 정지 = 미완성)")
		})

		It("8. **PITR 정확성** — doc-A 존재 ∧ doc-B 부재 ∧ doc-0 보존", func() {
			// 이 세 단언이 PITR 완성의 정의다.
			//   doc-0: base 스냅샷이 온전히 복원됨
			//   doc-A: base 이후 ~ PIT 이전의 oplog 가 replay 됨
			//   doc-B: PIT 이후의 oplog 는 replay 되지 않음 (경계가 실재함)
			// doc-B 가 남아있으면 --oplogLimit 이 무의미하다 = 시점복원이 아님.
			Expect(countDoc(pitrRTNamespace, primaryPod, "doc-0")).To(Equal("1"),
				"base 스냅샷의 doc-0 이 보존되어야 함")
			Expect(countDoc(pitrRTNamespace, primaryPod, "doc-A")).To(Equal("1"),
				"PIT 이전의 doc-A 는 oplog replay 로 복원되어야 함")
			Expect(countDoc(pitrRTNamespace, primaryPod, "doc-B")).To(Equal("0"),
				"PIT 이후의 doc-B 는 복원되지 않아야 함 — 남아있으면 PITR 이 아님")
		})
	})
})
