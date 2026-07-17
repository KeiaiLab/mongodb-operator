/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_uploader.go — PITR oplog 아카이브의 *수명주기 + 관측* controller.
//
// # 용도 전환 (아키텍처 A. 사이드카 직접 스트리밍)
//
// 예전 설계에서 본 controller 는 "staging EmptyDir → S3" 업로드 Job/CronJob 을
// 만들 예정이었다. 채택된 A 안에서는 tailer 사이드카가 EmptyDir 을 경유하지
// 않고 S3 로 **직접 스트리밍**하므로 (capture 와 upload 가 한 프로세스 = 원자),
// 업로드를 대행할 주체가 없다. 그래서 본 controller 의 책임은 업로드가 아니라
// *그 결과물의 수명주기와 가시성* 이다:
//
//	1. 복원 가능 window 계산 → MongoDBBackup CR status 기록
//	   (EarliestRestore / LatestRestore / RestorableWindow)
//	2. retention GC — 만료 oplog segment 삭제 (base 커버 불변식 준수)
//	3. 메트릭 배선 — oplog_window_hours / oplog_uploader_active
//
// 순수 계산 (키 파싱 / window / GC plan) 은 backup_gc.go 에 분리돼 있고 본
// 파일은 k8s + S3 접합만 담당한다.
//
// # S3 접근 방식 — 왜 인터페이스 seam 인가
//
// 이 repo 에는 S3 클라이언트가 **없다**: go.mod 에 AWS SDK / minio 가 없고
// keiailab-commons 에도 objectstore 계열 패키지가 없다. 기존 S3 접근 관례는
// 전부 *Job 안의 `aws s3 cp`* (assets/scripts/backup-s3.sh.tpl 이 런타임에
// awscli 를 설치해 쓴다) — 즉 **controller 프로세스는 S3 를 건드린 적이 없다**.
//
// 그런데 window 계산은 Reconcile 안에서의 *동기 list* 를 요구하므로 Job 위임
// (Job 생성 → 완료 대기 → ConfigMap 으로 결과 회수) 은 30s 주기에 파드를 매번
// 태우는 셈이라 성립하지 않는다. 반대로 SDK 도입은 go.mod 변경이라 본 트랙의
// 범위 밖이다. 그래서 접근면을 OplogSegmentStore 인터페이스로 좁혀 두고,
// 구현체 주입은 배선 시점 (cmd/main.go) 에 맡긴다.
//
// Store 가 nil 이면 window / GC 를 건너뛰고 **메트릭 series 를 지운다** —
// 관측 불가는 unknown 이지 0 이 아니다 (0 으로 두면 PrometheusRule 의
// MongoDBOplogUploaderDown 이 상시 오발한다).
//
// 회귀 가드: oplog_uploader_test.go.

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// oplogSegmentStaleAfter — 가장 최근 segment 가 이보다 오래되면 uploader
// (tailer 사이드카의 S3 스트리밍) 가 멈춘 것으로 본다.
//
// tailer 의 batch 회전이 30s 이므로 10 배 여유 — 일시적 회전 지연이 아니라
// *정지* 만 잡는다. PrometheusRule 의 For:5m 과 합쳐 실제 발화까지 최대 10 분.
const oplogSegmentStaleAfter = 5 * time.Minute

// OplogSegmentStore 는 아카이브된 oplog segment 에 대한 *최소* 접근면이다.
// 구현체는 S3 (또는 호환 오브젝트 스토어) 를 읽고 지우며, 자격증명은
// S3StorageSpec.CredentialsRef 가 가리키는 namespace 안의 Secret 에서 얻는다.
//
// 본 인터페이스가 seam 인 이유 + 구현체 부재 사유는 파일 상단 주석 참조.
type OplogSegmentStore interface {
	// ListSegments 는 OplogSegmentPrefix(s3.Prefix, clusterName) 밑의 객체를
	// 나열해 파싱 가능한 segment 만 반환한다 (계약 위반 키는 skip).
	ListSegments(ctx context.Context, s3 *mongodbv1alpha1.S3StorageSpec, namespace, clusterName string) ([]OplogSegment, error)
	// DeleteSegments 는 주어진 키들을 삭제한다. 부분 실패해도 무해하도록
	// 호출자가 plan 을 구성하므로 (PlanOplogPrune godoc), 구현체는 원자성이
	// 아니라 *에러 보고* 만 책임진다.
	DeleteSegments(ctx context.Context, s3 *mongodbv1alpha1.S3StorageSpec, namespace string, keys []string) error
}

// OplogUploaderReconciler 는 PITR 활성 클러스터의 oplog 아카이브 수명주기를
// reconcile 한다. MongoDB / MongoDBSharded 양쪽 CRD 의 Spec.Backup 을 관찰한다.
type OplogUploaderReconciler struct {
	client.Client

	// Store 는 S3 oplog segment 접근 seam. nil 이면 window 계산 / GC 를
	// 건너뛴다 (관측 불가 = unknown — 0 으로 단정하지 않는다).
	Store OplogSegmentStore
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs;mongodbshardeds,verbs=get;list;watch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackups,verbs=get;list;watch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackups/status,verbs=get;update;patch

// IsApplicable 은 주어진 BackupSpec 이 본 reconciler 의 처리 대상인지 검사.
// builder 의 IsOplogTailerEnabled 와 동일 의미 — sidecar 활성 = uploader
// 활성. 분리된 이유: future 에 sidecar 만 enable / uploader 만 disable 같은
// 비대칭 정책 도입 여지 보존.
func (r *OplogUploaderReconciler) IsApplicable(spec *mongodbv1alpha1.BackupSpec) bool {
	if spec == nil {
		return false
	}
	if !spec.Enabled || !spec.PITREnabled {
		return false
	}
	if spec.OplogRetentionHours <= 0 {
		return false
	}
	return true
}

// uploaderTarget 은 reconcile 대상 클러스터의 판정 결과.
type uploaderTarget struct {
	// Backup 은 클러스터의 백업 spec.
	Backup *mongodbv1alpha1.BackupSpec
	// Kind 는 MongoDBBackup.Spec.ClusterRef.Kind 와 대조할 CRD kind.
	Kind string
	// WindowSupported 는 단일 cluster-wide window 를 정의할 수 있는지.
	//
	// MongoDBSharded 는 shard 마다 oplog timestamp 가 독립이라 하나의
	// [earliest, latest] 로 cluster-wide 일관 시점을 표현할 수 없다 (RS 전용
	// — api/v1alpha1 RestoreSpec "PITR 제약" 절). 이 경우 window 를 *기록하지
	// 않는다* — 없는 보장을 status 에 적는 것이 거짓 주장이기 때문이며,
	// webhook 은 nil window 를 fail-open 으로 통과시키고 restore job 이
	// authoritative 하게 판정한다. retention GC 는 kind 와 무관하게 수행.
	WindowSupported bool
}

// Reconcile 은 PITR 활성 클러스터의 oplog 아카이브 상태를 조정한다.
func (r *OplogUploaderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("uploader", req.NamespacedName)

	target, err := r.lookupCluster(ctx, req.NamespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}
	// 대상 소멸 또는 PITR 미적용 → 메트릭 series 회수.
	//
	// Set(0) 이 아니라 Delete 인 이유: PrometheusRule 의
	// MongoDBOplogUploaderDown 이 `oplog_uploader_active == 0` 로 critical 을
	// 발화하고 MongoDBOplogWindowTooShort 가 `oplog_window_hours < 1` 로
	// warning 을 발화한다. PITR 을 안 쓰는 클러스터에 0 을 남기면 두 알림이
	// 영구 오발한다 — 알림 문구 자체가 "while PITR is enabled" 다.
	if target == nil || !r.IsApplicable(target.Backup) {
		r.clearMetrics(req.Namespace, req.Name)
		return ctrl.Result{}, nil
	}

	// PITR 아카이브는 S3 전용 — PVC 백업에는 segment 키 스킴이 없다.
	s3 := target.Backup.Storage.S3
	if target.Backup.Storage.Type != "s3" || s3 == nil {
		logger.V(1).Info("PITR enabled but storage is not S3; oplog archive unsupported",
			"storageType", target.Backup.Storage.Type)
		r.clearMetrics(req.Namespace, req.Name)
		return ctrl.Result{}, nil
	}
	if r.Store == nil {
		logger.V(1).Info("oplog segment store not wired; skipping window/GC (metrics stay absent = unknown)")
		r.clearMetrics(req.Namespace, req.Name)
		return ctrl.Result{RequeueAfter: requeueSteady}, nil
	}

	segments, err := r.Store.ListSegments(ctx, s3, req.Namespace, req.Name)
	if err != nil {
		// 관측 실패도 unknown — 메트릭을 0 으로 단정하지 않고 그대로 둔다.
		return ctrl.Result{}, fmt.Errorf("list oplog segments: %w", err)
	}

	now := time.Now()
	r.observeUploaderActive(req.Namespace, req.Name, segments, now)

	backups, err := r.listBaseBackups(ctx, req.Namespace, req.Name, target.Kind)
	if err != nil {
		return ctrl.Result{}, err
	}
	projected := make([]BaseBackup, 0, len(backups))
	index := make(map[string]*mongodbv1alpha1.MongoDBBackup, len(backups))
	for i := range backups {
		projected = append(projected, toBaseBackup(&backups[i]))
		index[backups[i].Name] = &backups[i]
	}

	plan := PlanBackupRetention(projected, target.Backup.Retention, now)
	if len(plan.Expired) > 0 {
		// 판정만 하고 삭제는 하지 않는다 — 사유는 OplogGCCutoff godoc
		// ("base archive 는 왜 삭제하지 않는가").
		logger.V(1).Info("base backups past retention (evaluation only; base archive GC not wired)",
			"expired", len(plan.Expired), "retained", len(plan.Retained))
	}

	survivors := segments
	if cutoff, ok := OplogGCCutoff(plan.Retained, target.Backup.OplogRetentionHours, now); ok {
		survivors, err = r.pruneSegments(ctx, s3, req.Namespace, segments, plan.Retained, cutoff)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if !target.WindowSupported {
		// sharded — window 미정의. 없는 보장을 적지 않는다.
		MetricOplogWindowHours.DeleteLabelValues(req.Namespace, req.Name)
		return ctrl.Result{RequeueAfter: requeueSteady}, nil
	}
	if err := r.updateWindows(ctx, req.Namespace, req.Name, plan.Retained, survivors, index); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueSteady}, nil
}

// lookupCluster 는 req 가 가리키는 MongoDB / MongoDBSharded 를 찾는다.
// 둘 다 없으면 (nil, nil) — 삭제된 CR. NotFound 가 아닌 에러는 그대로 전파해
// 일시적 API 장애를 "대상 없음" 으로 오인하지 않는다.
func (r *OplogUploaderReconciler) lookupCluster(ctx context.Context, key types.NamespacedName) (*uploaderTarget, error) {
	mdb := &mongodbv1alpha1.MongoDB{}
	err := r.Get(ctx, key, mdb)
	if err == nil {
		return &uploaderTarget{Backup: mdb.Spec.Backup, Kind: "MongoDB", WindowSupported: true}, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	mdbsh := &mongodbv1alpha1.MongoDBSharded{}
	err = r.Get(ctx, key, mdbsh)
	if err == nil {
		return &uploaderTarget{Backup: mdbsh.Spec.Backup, Kind: "MongoDBSharded", WindowSupported: false}, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	return nil, nil
}

// listBaseBackups 는 클러스터에 속한 *base 스냅샷* MongoDBBackup CR 을 모은다.
// restore 작업 CR (Spec.Restore != nil) / 미완료 / 삭제 중인 것은 제외.
func (r *OplogUploaderReconciler) listBaseBackups(ctx context.Context, namespace, cluster, kind string) ([]mongodbv1alpha1.MongoDBBackup, error) {
	list := &mongodbv1alpha1.MongoDBBackupList{}
	if err := r.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list MongoDBBackup in %s: %w", namespace, err)
	}
	out := make([]mongodbv1alpha1.MongoDBBackup, 0, len(list.Items))
	for i := range list.Items {
		b := list.Items[i]
		// Kind 까지 대조 — MongoDB 와 MongoDBSharded 가 같은 namespace 에서
		// 같은 이름을 쓸 수 있으므로 이름만으로는 섞인다.
		if b.Spec.ClusterRef.Name != cluster || b.Spec.ClusterRef.Kind != kind {
			continue
		}
		if b.Spec.Restore != nil {
			continue
		}
		if b.Status.Phase != backupPhaseCompleted {
			continue
		}
		if !b.DeletionTimestamp.IsZero() {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

// toBaseBackup 은 CR → 순수 도메인 투영 (backup_gc.go 를 k8s 비의존으로 유지).
func toBaseBackup(b *mongodbv1alpha1.MongoDBBackup) BaseBackup {
	created := b.CreationTimestamp.Time
	if b.Status.CompletionTime != nil {
		created = b.Status.CompletionTime.Time
	}
	out := BaseBackup{Name: b.Name, CreatedAt: created, Location: b.Status.Location}
	if b.Status.OplogStart != nil {
		ts := bsonTSFromTime(b.Status.OplogStart.Time)
		out.OplogStart = &ts
	}
	return out
}

// pruneSegments 는 list → plan → (검증) → delete 2 단계로 만료 segment 를
// 지우고 생존 segment 를 반환한다. plan 은 삭제 전에 먼저 로그로 남긴다.
func (r *OplogUploaderReconciler) pruneSegments(
	ctx context.Context,
	s3 *mongodbv1alpha1.S3StorageSpec,
	namespace string,
	segments []OplogSegment,
	retained []BaseBackup,
	cutoff BSONTimestamp,
) ([]OplogSegment, error) {
	logger := log.FromContext(ctx)

	victims := PlanOplogPrune(segments, cutoff)
	if len(victims) == 0 {
		return segments, nil
	}

	keys := make([]string, 0, len(victims))
	doomed := make(map[string]bool, len(victims))
	for _, v := range victims {
		keys = append(keys, v.Key)
		doomed[v.Key] = true
	}
	survivors := make([]OplogSegment, 0, len(segments)-len(victims))
	for _, s := range segments {
		if !doomed[s.Key] {
			survivors = append(survivors, s)
		}
	}

	// 1 단계 — plan 을 먼저 기록 (비가역 삭제의 audit trail).
	logger.Info("oplog prune plan",
		"cutoff", cutoff.String(),
		"victims", len(victims),
		"survivors", len(survivors),
		"oldest", victims[0].Key,
		"newest", victims[len(victims)-1].Key)

	// 2 단계 — 안전장치: 보존 base 의 window 가 하나라도 달라지면 폐기.
	if err := VerifyPrunePlan(retained, segments, survivors); err != nil {
		// plan 이 올바르면 도달 불가 (심층 방어). 재시도해도 같은 결과라
		// reconcile 실패로 올려 hot loop 를 만들지 않고, GC 만 건너뛴다.
		logger.Error(err, "oplog prune plan aborted; skipping GC this cycle", "cutoff", cutoff.String())
		return segments, nil
	}

	if err := r.Store.DeleteSegments(ctx, s3, namespace, keys); err != nil {
		// 부분 삭제는 무해하다 — victims 는 전부 어떤 보존 window 에도
		// 기여할 수 없는 쓰레기라 삭제는 순서 무관 + 멱등이다
		// (PlanOplogPrune godoc). 다음 reconcile 이 재-list 후 재시도한다.
		return segments, fmt.Errorf("delete oplog segments: %w", err)
	}
	logger.Info("oplog segments pruned", "deleted", len(keys), "cutoff", cutoff.String())
	return survivors, nil
}

// updateWindows 는 보존 base 각각의 복원 가능 window 를 계산해 CR status 에
// 기록하고, 클러스터 단위 window 메트릭을 갱신한다.
func (r *OplogUploaderReconciler) updateWindows(
	ctx context.Context,
	namespace, cluster string,
	retained []BaseBackup,
	segments []OplogSegment,
	index map[string]*mongodbv1alpha1.MongoDBBackup,
) error {
	logger := log.FromContext(ctx)

	var best time.Duration
	eligible := 0
	for _, b := range retained {
		if b.OplogStart == nil {
			// `--oplog` 없이 뜬 백업 — PITR 기점 불가.
			continue
		}
		cr := index[b.Name]
		if cr == nil {
			continue
		}
		w := ComputeOplogWindow(*b.OplogStart, segments)
		if w.GapFrom != nil {
			logger.V(1).Info("oplog chain gap detected; window truncated",
				"backup", b.Name, "gapFrom", w.GapFrom.String(), "gapTo", w.GapTo.String())
		}
		if err := r.applyWindowStatus(ctx, cr, w); err != nil {
			return err
		}
		if d := w.Duration(); eligible == 0 || d > best {
			best = d
		}
		eligible++
	}

	// PITR 이 켜져 있는데 쓸 수 있는 base 가 하나도 없으면 0 = 진짜 위험
	// (MongoDBOplogWindowTooShort 발화가 정당). 최초 활성화 직후 첫 백업
	// 전에는 잠시 0 이지만 알림의 For:5m 이 그 과도기를 흡수한다.
	MetricOplogWindowHours.WithLabelValues(namespace, cluster).Set(best.Hours())
	return nil
}

// applyWindowStatus 는 window 3 필드를 status 에 반영한다. 값이 그대로면
// API 호출을 건너뛴다 — reconcile 이 requeueSteady 마다 도는데 매번 status
// 를 써대면 무의미한 write 폭주가 된다.
func (r *OplogUploaderReconciler) applyWindowStatus(ctx context.Context, cr *mongodbv1alpha1.MongoDBBackup, w OplogWindow) error {
	earliest := metav1.NewTime(w.Base.Time())
	latest := metav1.NewTime(w.Latest.Time())
	text := formatRestorableWindow(w)

	if timeEqual(cr.Status.EarliestRestore, &earliest) &&
		timeEqual(cr.Status.LatestRestore, &latest) &&
		cr.Status.RestorableWindow == text {
		return nil
	}

	apply := func() {
		cr.Status.EarliestRestore = &earliest
		cr.Status.LatestRestore = &latest
		cr.Status.RestorableWindow = text
	}
	apply()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, cr, apply); err != nil {
		return fmt.Errorf("update restorable window on %s: %w", cr.Name, err)
	}
	return nil
}

// formatRestorableWindow 는 status.RestorableWindow 의 사람이 읽는 한 줄 표기.
// 표시 전용 — 기계 판정은 이 문자열을 파싱하지 말고 EarliestRestore /
// LatestRestore 를 직접 읽어야 한다 (api/v1alpha1 godoc).
func formatRestorableWindow(w OplogWindow) string {
	return w.Base.Time().Format(time.RFC3339) + " ~ " + w.Latest.Time().Format(time.RFC3339)
}

func timeEqual(a, b *metav1.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Time.Equal(b.Time)
}

// observeUploaderActive 는 uploader 생존을 *실측 증거* 로 판정한다.
//
// "PITR 설정이 켜져 있다" 를 그대로 1 로 내보내면 사이드카가 전부 죽어도 항상
// 1 이라 MongoDBOplogUploaderDown (critical) 이 영원히 발화하지 않는다 — 설정
// 을 되읊는 메트릭은 알림을 무력화한다. 대신 "최근 segment 가 실제로 올라오고
// 있는가" 로 판정하면 사이드카 사망 / S3 자격증명 만료를 진짜로 잡는다.
func (r *OplogUploaderReconciler) observeUploaderActive(namespace, name string, segments []OplogSegment, now time.Time) {
	active := 0.0
	if len(segments) > 0 {
		newest := segments[0].End
		for _, s := range segments[1:] {
			if s.End.Compare(newest) > 0 {
				newest = s.End
			}
		}
		if now.Sub(newest.Time()) <= oplogSegmentStaleAfter {
			active = 1
		}
	}
	MetricOplogUploaderActive.WithLabelValues(namespace, name).Set(active)
}

// clearMetrics 는 클러스터의 PITR 메트릭 series 를 제거한다 (cardinality 회수
// + 오발 방지). Reconcile 의 Delete-vs-Set(0) 주석 참조.
func (r *OplogUploaderReconciler) clearMetrics(namespace, name string) {
	MetricOplogUploaderActive.DeleteLabelValues(namespace, name)
	MetricOplogWindowHours.DeleteLabelValues(namespace, name)
}

// SetupWithManager 는 두 CRD 변화 모두 본 reconciler 로 라우팅.
// MongoDB / MongoDBSharded 가 같은 NamespacedName 공간을 공유하지 않으므로
// 두 source 를 별도 controller 로 등록 (controller-runtime 표준 패턴).
func (r *OplogUploaderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named("oplog-uploader-mongodb").
		For(&mongodbv1alpha1.MongoDB{}).
		Complete(r); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("oplog-uploader-mongodbsharded").
		For(&mongodbv1alpha1.MongoDBSharded{}).
		Complete(r)
}
