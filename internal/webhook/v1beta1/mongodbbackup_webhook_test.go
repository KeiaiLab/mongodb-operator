/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbbackup_webhook_test.go — restore(PITR) admission 검증 회귀 가드.
//
// 각 거부 규칙마다 "거부돼야 하는 케이스" 와 "그 규칙 때문에 거부되면 안 되는
// 케이스" 를 짝으로 둔다 — 규칙이 과잉 차단(특히 관측 불가 → 거부)으로 무너지는
// 쪽이 실제 위험이기 때문(DR 경로 봉쇄 / CR 삭제 불가).

package v1beta1

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

const (
	testNS      = "default"
	srcName     = "base-backup"
	clusterName = "rs0"
)

var (
	tBase   = time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC) // OplogStart / EarliestRestore
	tMid    = tBase.Add(4 * time.Hour)                     // window 안
	tLatest = tBase.Add(8 * time.Hour)                     // LatestRestore
)

func mt(t time.Time) *metav1.Time { m := metav1.NewTime(t); return &m }

func ptr[T any](v T) *T { return &v }

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := mongodbv1beta1.AddToScheme(s); err != nil {
		t.Fatalf("add mongodb v1beta1 scheme: %v", err)
	}
	return s
}

func newValidator(t *testing.T, objs ...client.Object) *MongoDBBackupCustomValidator {
	t.Helper()
	return &MongoDBBackupCustomValidator{
		Reader: fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build(),
	}
}

// srcBackup — PITR 기점으로 쓸 수 있는 완료 백업(Completed + 앵커 + window).
func srcBackup(mutate ...func(*mongodbv1beta1.MongoDBBackup)) *mongodbv1beta1.MongoDBBackup {
	b := &mongodbv1beta1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: srcName, Namespace: testNS},
		Spec: mongodbv1beta1.MongoDBBackupSpec{
			ClusterRef: mongodbv1beta1.ClusterReference{Kind: clusterKindMongoDB, Name: clusterName},
			Storage:    mongodbv1beta1.BackupStorageSpec{Type: "s3"},
		},
		Status: mongodbv1beta1.MongoDBBackupStatus{
			Phase:           backupPhaseCompleted,
			OplogStart:      mt(tBase),
			EarliestRestore: mt(tBase),
			LatestRestore:   mt(tLatest),
		},
	}
	for _, m := range mutate {
		m(b)
	}
	return b
}

// restoreCR — srcName 을 복원하는 restore 작업 CR.
func restoreCR(mutate ...func(*mongodbv1beta1.MongoDBBackup)) *mongodbv1beta1.MongoDBBackup {
	b := &mongodbv1beta1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "restore-1", Namespace: testNS},
		Spec: mongodbv1beta1.MongoDBBackupSpec{
			ClusterRef: mongodbv1beta1.ClusterReference{Kind: clusterKindMongoDB, Name: "target"},
			Storage:    mongodbv1beta1.BackupStorageSpec{Type: "s3"},
			Restore:    &mongodbv1beta1.RestoreSpec{SourceBackupName: srcName},
		},
	}
	for _, m := range mutate {
		m(b)
	}
	return b
}

func withPIT(ts time.Time) func(*mongodbv1beta1.MongoDBBackup) {
	return func(b *mongodbv1beta1.MongoDBBackup) { b.Spec.Restore.PointInTime = mt(ts) }
}

func rsCluster(pitr bool) *mongodbv1beta1.MongoDB {
	return &mongodbv1beta1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNS},
		Spec: mongodbv1beta1.MongoDBSpec{
			Backup: &mongodbv1beta1.BackupSpec{Enabled: true, PITREnabled: pitr},
		},
	}
}

func shCluster(pitr bool) *mongodbv1beta1.MongoDBSharded {
	return &mongodbv1beta1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNS},
		Spec: mongodbv1beta1.MongoDBShardedSpec{
			Backup: &mongodbv1beta1.BackupSpec{Enabled: true, PITREnabled: pitr},
		},
	}
}

// --- 검증 대상 범위 ------------------------------------------------------

// 예약 백업 CronJob 이 만드는 capture CR(Restore=nil)은 무검증 통과해야 한다 —
// 여기서 막히면 백업 자체가 멎는다.
func TestValidateCreate_PlainBackupCR_Passes(t *testing.T) {
	v := newValidator(t)
	b := restoreCR(func(b *mongodbv1beta1.MongoDBBackup) { b.Spec.Restore = nil })
	if _, err := v.ValidateCreate(context.Background(), b); err != nil {
		t.Errorf("백업 capture CR 거부: %v", err)
	}
}

// --- 규칙 1: source 백업 Phase ------------------------------------------

func TestValidateCreate_SourceBackupPhase(t *testing.T) {
	tests := []struct {
		name    string
		phase   string
		wantErr bool
	}{
		{"Completed → 허용", backupPhaseCompleted, false},
		{"Running → 거부", "Running", true},
		{"Failed → 거부", "Failed", true},
		{"Pending → 거부", "Pending", true},
		{"빈 phase → 거부", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := srcBackup(func(b *mongodbv1beta1.MongoDBBackup) { b.Status.Phase = tc.phase })
			v := newValidator(t, src, rsCluster(true))
			_, err := v.ValidateCreate(context.Background(), restoreCR())
			if tc.wantErr && err == nil {
				t.Errorf("phase=%q 통과 — 거부 기대", tc.phase)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("phase=%q 거부: %v", tc.phase, err)
			}
		})
	}
}

func TestValidateCreate_SourceBackupNotFound(t *testing.T) {
	v := newValidator(t, rsCluster(true))
	_, err := v.ValidateCreate(context.Background(), restoreCR())
	if err == nil {
		t.Fatal("존재하지 않는 source 백업 통과 — 거부 기대")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("에러가 원인을 밝히지 않음: %v", err)
	}
}

// PIT 없는 base 스냅샷 복원은 PITR 게이트를 타지 않는다 — PITR 미활성
// 클러스터라도 통과해야 한다.
func TestValidateCreate_BaseOnlyRestore_SkipsPITRGates(t *testing.T) {
	v := newValidator(t, srcBackup(), rsCluster(false))
	if _, err := v.ValidateCreate(context.Background(), restoreCR()); err != nil {
		t.Errorf("base 스냅샷 복원 거부: %v", err)
	}
}

// --- 규칙 2: source 클러스터 PITREnabled --------------------------------

func TestValidateCreate_PITRDisabledOnSourceCluster(t *testing.T) {
	tests := []struct {
		name    string
		cluster client.Object
		wantErr bool
	}{
		{"pitrEnabled=true → 허용", rsCluster(true), false},
		{"pitrEnabled=false → 거부", rsCluster(false), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := newValidator(t, srcBackup(), tc.cluster)
			_, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tMid)))
			if tc.wantErr && err == nil {
				t.Error("PITR 미활성 클러스터의 PIT 복원 통과 — 거부 기대")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("유효한 PIT 복원 거부: %v", err)
			}
		})
	}
}

// backup 블록 자체가 없는 클러스터 = PITR 불가 → 거부(nil 역참조 아님).
func TestValidateCreate_SourceClusterWithoutBackupSpec(t *testing.T) {
	c := rsCluster(true)
	c.Spec.Backup = nil
	v := newValidator(t, srcBackup(), c)
	if _, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tMid))); err == nil {
		t.Error("backup 미설정 클러스터의 PIT 복원 통과 — 거부 기대")
	}
}

// DR 본류: source 클러스터가 이미 삭제된 뒤 백업으로 새 클러스터를 세운다.
// PITREnabled 확인이 불가능하다는 이유로 막으면 안 된다(Warning 후 통과).
func TestValidateCreate_SourceClusterDeleted_WarnsNotRejects(t *testing.T) {
	v := newValidator(t, srcBackup()) // 클러스터 CR 없음
	warnings, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tMid)))
	if err != nil {
		t.Fatalf("source 클러스터 부재로 DR 복원 거부 — 통과 기대: %v", err)
	}
	if len(warnings) == 0 {
		t.Error("확인 불가를 통과시켰으면 Warning 은 남겨야 함")
	}
}

// --- 규칙 3: base 스냅샷 oplog 앵커 -------------------------------------

func TestValidateCreate_PITBeforeOplogStart(t *testing.T) {
	v := newValidator(t, srcBackup(), rsCluster(true))
	_, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tBase.Add(-1*time.Second))))
	if err == nil {
		t.Fatal("base 스냅샷 이전 시점 통과 — 거부 기대")
	}
	if !strings.Contains(err.Error(), "lower bound") {
		t.Errorf("에러가 원인을 밝히지 않음: %v", err)
	}
}

// `mongodump --oplog` 없이 떠진 백업 = PITR 기점 불가.
func TestValidateCreate_NoOplogAnchor(t *testing.T) {
	src := srcBackup(func(b *mongodbv1beta1.MongoDBBackup) {
		b.Status.OplogStart = nil
		b.Status.EarliestRestore = nil
		b.Status.LatestRestore = nil
	})
	v := newValidator(t, src, rsCluster(true))
	_, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tMid)))
	if err == nil {
		t.Fatal("oplogStart 없는 백업의 PIT 복원 통과 — 거부 기대")
	}
	if !strings.Contains(err.Error(), "oplogStart") {
		t.Errorf("에러가 원인을 밝히지 않음: %v", err)
	}
}

// --- 규칙 4: 복원 가능 window 경계 --------------------------------------

func TestValidateCreate_WindowBounds(t *testing.T) {
	tests := []struct {
		name    string
		pit     time.Time
		wantErr bool
	}{
		{"window 한참 이전 → 거부", tBase.Add(-1 * time.Hour), true},
		{"하한 경계(inclusive) → 허용", tBase, false},
		{"window 중간 → 허용", tMid, false},
		{"상한 경계(inclusive) → 허용", tLatest, false},
		{"상한 1초 초과(trimmed/미래) → 거부", tLatest.Add(1 * time.Second), true},
		{"먼 미래 → 거부", tLatest.Add(72 * time.Hour), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := newValidator(t, srcBackup(), rsCluster(true))
			_, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tc.pit)))
			if tc.wantErr && err == nil {
				t.Errorf("pit=%s 통과 — 거부 기대", tc.pit.Format(time.RFC3339))
			}
			if !tc.wantErr && err != nil {
				t.Errorf("pit=%s 거부: %v", tc.pit.Format(time.RFC3339), err)
			}
		})
	}
}

// retention prune 으로 base 직후 segment 가 사라진 경우:
// Earliest==Latest==OplogStart → base 시점만 복원 가능.
func TestValidateCreate_CollapsedWindow(t *testing.T) {
	src := srcBackup(func(b *mongodbv1beta1.MongoDBBackup) {
		b.Status.EarliestRestore = mt(tBase)
		b.Status.LatestRestore = mt(tBase)
	})
	v := newValidator(t, src, rsCluster(true))

	if _, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tBase))); err != nil {
		t.Errorf("collapsed window 의 base 시점 복원 거부: %v", err)
	}
	if _, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tMid))); err == nil {
		t.Error("collapsed window 밖 시점 통과 — 거부 기대")
	}
}

// --- 규칙 7: 관측 불가(window 미기록) → Warning, 거부 금지 --------------

func TestValidateCreate_WindowNotYetComputed_WarnsNotRejects(t *testing.T) {
	// uploader 가 아직 안 돌아 window 는 없지만 앵커는 있는 상태.
	src := srcBackup(func(b *mongodbv1beta1.MongoDBBackup) {
		b.Status.EarliestRestore = nil
		b.Status.LatestRestore = nil
	})
	v := newValidator(t, src, rsCluster(true))
	warnings, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tMid)))
	if err != nil {
		t.Fatalf("window 미기록 상태에서 거부 — 통과 기대(fail-open): %v", err)
	}
	if len(warnings) == 0 {
		t.Error("window 미검증을 통과시켰으면 Warning 은 남겨야 함")
	}
}

// window 가 없어도 앵커(OplogStart)는 하한으로 살아 있다 — fail-open 이
// "아무거나 통과" 로 새지 않는지.
func TestValidateCreate_WindowNotYetComputed_StillEnforcesAnchor(t *testing.T) {
	src := srcBackup(func(b *mongodbv1beta1.MongoDBBackup) {
		b.Status.EarliestRestore = nil
		b.Status.LatestRestore = nil
	})
	v := newValidator(t, src, rsCluster(true))
	if _, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tBase.Add(-1*time.Hour)))); err == nil {
		t.Error("window 미기록이어도 base 이전 시점은 거부해야 함")
	}
}

// --- 규칙 5: PointInTimeTimestamp 형식 + 우선순위 -----------------------

func TestValidateCreate_PointInTimeTimestampFormat(t *testing.T) {
	tests := []struct {
		name    string
		ts      string
		wantErr bool
	}{
		{"유효 <sec>:<ord>", ts(tMid, 7), false},
		{"ordinal 0", ts(tMid, 0), false},
		{"콜론 없음", "1752710400", true},
		{"sec 비정수", "abc:1", true},
		{"ordinal 비정수", "1752710400:x", true},
		{"ordinal uint32 초과", "1752710400:4294967296", true},
		{"빈 문자열", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := restoreCR(func(b *mongodbv1beta1.MongoDBBackup) {
				b.Spec.Restore.PointInTimeTimestamp = ptr(tc.ts)
			})
			v := newValidator(t, srcBackup(), rsCluster(true))
			_, err := v.ValidateCreate(context.Background(), b)
			if tc.wantErr && err == nil {
				t.Errorf("ts=%q 통과 — 거부 기대", tc.ts)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ts=%q 거부: %v", tc.ts, err)
			}
		})
	}
}

// 둘 다 지정되면 PointInTimeTimestamp 가 이긴다(RestoreSpec godoc).
// PointInTime 은 window 안, Timestamp 는 window 밖 → 거부되면 ts 가 이긴 것.
func TestValidateCreate_TimestampTakesPrecedenceOverPointInTime(t *testing.T) {
	b := restoreCR(withPIT(tMid), func(b *mongodbv1beta1.MongoDBBackup) {
		b.Spec.Restore.PointInTimeTimestamp = ptr(ts(tLatest.Add(1*time.Hour), 1))
	})
	v := newValidator(t, srcBackup(), rsCluster(true))
	_, err := v.ValidateCreate(context.Background(), b)
	if err == nil {
		t.Fatal("window 밖 pointInTimeTimestamp 통과 — pointInTime 이 우선 적용된 듯")
	}
	if !strings.Contains(err.Error(), "pointInTimeTimestamp") {
		t.Errorf("거부가 pointInTimeTimestamp 를 가리키지 않음: %v", err)
	}

	// 역방향: ts 가 window 안이면 pointInTime 이 밖이어도 통과 + 무시 경고.
	b2 := restoreCR(withPIT(tLatest.Add(24*time.Hour)), func(b *mongodbv1beta1.MongoDBBackup) {
		b.Spec.Restore.PointInTimeTimestamp = ptr(ts(tMid, 3))
	})
	warnings, err := v.ValidateCreate(context.Background(), b2)
	if err != nil {
		t.Fatalf("window 안 pointInTimeTimestamp 거부 — pointInTime 이 우선 적용된 듯: %v", err)
	}
	if !hasWarning(warnings, "takes precedence") {
		t.Errorf("pointInTime 무시 경고 없음: %v", warnings)
	}
}

// --- 규칙 6: sharded = Warning(거부 아님) -------------------------------

func TestValidateCreate_ShardedPITR_WarnsNotRejects(t *testing.T) {
	src := srcBackup(func(b *mongodbv1beta1.MongoDBBackup) {
		b.Spec.ClusterRef.Kind = clusterKindSharded
	})
	b := restoreCR(withPIT(tMid), func(b *mongodbv1beta1.MongoDBBackup) {
		b.Spec.ClusterRef.Kind = clusterKindSharded
	})
	v := newValidator(t, src, shCluster(true))

	warnings, err := v.ValidateCreate(context.Background(), b)
	if err != nil {
		t.Fatalf("sharded PITR 거부 — best-effort 통과 기대: %v", err)
	}
	if !hasWarning(warnings, "not consistency-guaranteed") {
		t.Errorf("sharded 정합 미보장 경고 없음: %v", warnings)
	}
}

// RS 전용 경로에는 sharded 경고가 새지 않아야 한다(경고 피로 방지).
func TestValidateCreate_ReplicaSetPITR_NoShardedWarning(t *testing.T) {
	v := newValidator(t, srcBackup(), rsCluster(true))
	warnings, err := v.ValidateCreate(context.Background(), restoreCR(withPIT(tMid)))
	if err != nil {
		t.Fatalf("유효한 RS PITR 거부: %v", err)
	}
	if hasWarning(warnings, "not consistency-guaranteed") {
		t.Errorf("RS 복원에 sharded 경고 오출력: %v", warnings)
	}
}

// --- ValidateUpdate ------------------------------------------------------

// window 는 retention prune 으로 좁아진다. 이미 admit 된 restore spec 을 드리프트한
// window 로 재심하면 finalizer 제거까지 막혀 CR 이 삭제 불가로 갇힌다.
func TestValidateUpdate_UnchangedRestoreSpec_NotRevalidated(t *testing.T) {
	// source 백업 window 가 이미 지나가 버린(collapsed) 상태.
	src := srcBackup(func(b *mongodbv1beta1.MongoDBBackup) {
		b.Status.EarliestRestore = mt(tBase)
		b.Status.LatestRestore = mt(tBase)
	})
	v := newValidator(t, src, rsCluster(true))

	// create 당시엔 유효했던 PIT (지금은 window 밖) — spec 은 그대로 두고
	// finalizer 만 제거하는, 삭제 진행을 위한 update.
	old := restoreCR(withPIT(tMid))
	old.Finalizers = []string{"mongodb.keiailab.com/backup"}
	updated := restoreCR(withPIT(tMid))
	updated.Finalizers = nil

	if _, err := v.ValidateUpdate(context.Background(), old, updated); err != nil {
		t.Errorf("restore spec 무변경 update 거부 — CR 이 갇힌다: %v", err)
	}
}

func TestValidateUpdate_ChangedRestoreSpec_Revalidated(t *testing.T) {
	v := newValidator(t, srcBackup(), rsCluster(true))
	old := restoreCR(withPIT(tMid))
	updated := restoreCR(withPIT(tLatest.Add(48 * time.Hour))) // window 밖으로 변경

	if _, err := v.ValidateUpdate(context.Background(), old, updated); err == nil {
		t.Error("window 밖으로 바뀐 restore spec 통과 — 거부 기대")
	}
}

func TestValidateDelete_AlwaysAllowed(t *testing.T) {
	v := newValidator(t)
	if _, err := v.ValidateDelete(context.Background(), restoreCR(withPIT(tMid))); err != nil {
		t.Errorf("delete 거부: %v", err)
	}
}

// --- helpers -------------------------------------------------------------

// ts — "<sec>:<ordinal>" BSON timestamp 표기.
func ts(t time.Time, ord int) string {
	return strings.Join([]string{itoa(t.Unix()), itoa(int64(ord))}, ":")
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
