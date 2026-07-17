/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbbackup_webhook.go — MongoDBBackup validating webhook(v1beta1).
//
// Spec.Restore 가 설정된 CR(= restore 작업)만 검증한다. 순수 백업 capture CR
// (Restore=nil)은 즉시 통과 — 예약 백업 CronJob 이 만드는 CR 이 그 경로다.
//
// 본 webhook 은 *권고 게이트* 다. 복원 가능 window(status)는 uploader 가 S3
// segment 를 관측해 주기 갱신하는 값이라 admission 시점엔 늘 stale 일 수 있고,
// 진본 판정은 restore job 이 실제 segment chain 으로 한다(api/v1beta1
// RestoreSpec godoc). 따라서 원칙은 **확실히 틀린 것만 거부**:
//   - 관측 가능하고 명백히 틀림(source 백업 미완료 / PITR 미활성 / 앵커 부재 /
//     window 밖) → 거부
//   - 관측 불가(window 미기록 / source 클러스터 이미 삭제) → Warning 후 통과
//
// 조기 차단의 값어치: restore job 은 `mongorestore --drop` 으로 target 을 먼저
// 비우고 나서야 oplog replay 에 실패한다 — 틀린 PIT 를 admit 하면 살아 있던
// 데이터를 지운 뒤 목표 시점에 못 닿는다. 그래서 판정 가능한 오류는 CR 이
// 만들어지기 전에 끊는다.
package v1beta1

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

const (
	// clusterKind* — ClusterReference.Kind enum (CRD marker 와 동일 집합).
	clusterKindMongoDB = "MongoDB"
	clusterKindSharded = "MongoDBSharded"

	// backupPhaseCompleted — MongoDBBackupStatus.Phase enum 중 restore 가능 상태.
	backupPhaseCompleted = "Completed"
)

// SetupMongoDBBackupWebhookWithManager registers the validating webhook.
func SetupMongoDBBackupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &mongodbv1beta1.MongoDBBackup{}).
		WithValidator(&MongoDBBackupCustomValidator{Reader: mgr.GetAPIReader()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-mongodb-keiailab-com-v1beta1-mongodbbackup,mutating=false,failurePolicy=fail,sideEffects=None,groups=mongodb.keiailab.com,resources=mongodbbackups,verbs=create;update,versions=v1beta1,name=vmongodbbackup-v1beta1.kb.io,admissionReviewVersions=v1

// MongoDBBackupCustomValidator — restore(PITR) admission validation.
//
// Reader 로 source 백업 CR + source 클러스터 CR 을 읽는다. 기존 webhook 들과
// 달리 stateless 가 아닌 이유는 검증 근거(Phase / PITREnabled / window)가 전부
// 다른 객체에 있기 때문.
//
// 캐시 client 가 아니라 APIReader(직접 read)를 쓴다: ① restore CR 생성은 사람이
// 하는 드문 이벤트라 캐시 이득이 없고 ② window 는 이미 stale 한 관측치인데
// informer lag 을 얹으면 더 벌어지며 ③ webhook 은 캐시 sync 완료 전에도 호출될
// 수 있다.
type MongoDBBackupCustomValidator struct {
	Reader client.Reader
}

// ValidateCreate — 신규 CR 검증.
func (v *MongoDBBackupCustomValidator) ValidateCreate(ctx context.Context, b *mongodbv1beta1.MongoDBBackup) (admission.Warnings, error) {
	return v.validate(ctx, b)
}

// ValidateUpdate — restore spec 이 *실제로 바뀐* 경우에만 재검증한다.
//
// 무조건 재검증하면 안 된다: 본 검증은 시간이 지나면 좁아지는 외부 상태(source
// 백업의 window — retention prune)에 의존한다. create 시점엔 유효했던 PIT 가
// 나중에 window 밖으로 밀리면, spec 과 무관한 update(finalizer 제거 / label /
// annotation)까지 전부 거부돼 CR 이 삭제조차 안 되는 상태로 갇힌다. 이미 admit
// 된 값을 드리프트하는 외부 상태로 재심하지 않는다.
func (v *MongoDBBackupCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *mongodbv1beta1.MongoDBBackup) (admission.Warnings, error) {
	if oldObj != nil && apiequality.Semantic.DeepEqual(oldObj.Spec.Restore, newObj.Spec.Restore) {
		return nil, nil
	}
	return v.validate(ctx, newObj)
}

// ValidateDelete — 항상 허용.
func (v *MongoDBBackupCustomValidator) ValidateDelete(_ context.Context, _ *mongodbv1beta1.MongoDBBackup) (admission.Warnings, error) {
	return nil, nil
}

// validate — Restore 작업 CR 만 검증 대상.
func (v *MongoDBBackupCustomValidator) validate(ctx context.Context, b *mongodbv1beta1.MongoDBBackup) (admission.Warnings, error) {
	// Restore=nil = 백업 capture CR — 검증 대상 아님(예약 백업 CronJob 경로).
	if b.Spec.Restore == nil {
		return nil, nil
	}
	warnings, errs := v.validateRestore(ctx, b)
	if len(errs) > 0 {
		return warnings, apiError("MongoDBBackup", b.GetName(), errs)
	}
	return warnings, nil
}

// validateRestore — source 백업 상태 + (PIT 지정 시) PITR 게이트.
func (v *MongoDBBackupCustomValidator) validateRestore(ctx context.Context, b *mongodbv1beta1.MongoDBBackup) (admission.Warnings, field.ErrorList) {
	var warnings admission.Warnings
	var errs field.ErrorList

	p := field.NewPath("spec", "restore")
	r := b.Spec.Restore

	// 목표 시점 확정. 못 읽으면 이후 window 대조가 전부 무의미 → 즉시 종료.
	target, parseErrs := resolvePointInTime(p, r)
	if len(parseErrs) > 0 {
		return warnings, parseErrs
	}
	if r.PointInTime != nil && r.PointInTimeTimestamp != nil {
		warnings = append(warnings, fmt.Sprintf(
			"spec.restore: pointInTimeTimestamp %q takes precedence — pointInTime %q is ignored",
			*r.PointInTimeTimestamp, r.PointInTime.UTC().Format(time.RFC3339)))
	}

	// source 백업 CR 조회.
	src := &mongodbv1beta1.MongoDBBackup{}
	key := types.NamespacedName{Name: r.SourceBackupName, Namespace: b.Namespace}
	if err := v.Reader.Get(ctx, key, src); err != nil {
		if apierrors.IsNotFound(err) {
			errs = append(errs, field.Invalid(p.Child("sourceBackupName"), r.SourceBackupName,
				"source backup not found in namespace "+b.Namespace))
			return warnings, errs
		}
		// 일시적 API 오류. source 백업을 못 읽으면 검증 근거가 통째로 없다 —
		// 파괴적 restore(--drop)를 무검증 통과시키지 않는다.
		errs = append(errs, field.InternalError(p.Child("sourceBackupName"), err))
		return warnings, errs
	}

	// 규칙 1 — source 백업이 Completed 여야 복원 대상이 될 수 있다.
	if src.Status.Phase != backupPhaseCompleted {
		errs = append(errs, field.Invalid(p.Child("sourceBackupName"), r.SourceBackupName,
			fmt.Sprintf("source backup phase is %q — must be %q before it can be restored",
				src.Status.Phase, backupPhaseCompleted)))
	}

	// PIT 미지정 = base 스냅샷 시점 복원 — PITR 게이트 불요.
	if target == nil {
		return warnings, errs
	}

	w, e := v.validatePITR(ctx, b, src, target)
	return append(warnings, w...), append(errs, e...)
}

// validatePITR — PIT 지정 restore 의 게이트: sharded 경고 + source 클러스터 PITR
// 활성 + base 앵커/복원 가능 window.
func (v *MongoDBBackupCustomValidator) validatePITR(ctx context.Context, b, src *mongodbv1beta1.MongoDBBackup, t *pitTarget) (admission.Warnings, field.ErrorList) {
	var warnings admission.Warnings
	var errs field.ErrorList

	// 규칙 6 — sharded 는 shard 마다 oplog ts 가 독립이라 단일 PIT 로 cluster-wide
	// 일관 시점을 정의할 수 없다. 거부하지 않고 best-effort 경고(RestoreSpec godoc).
	if b.Spec.ClusterRef.Kind == clusterKindSharded {
		warnings = append(warnings, shardedPITRWarning("restore target "+b.Spec.ClusterRef.Name))
	}
	if src.Spec.ClusterRef.Kind == clusterKindSharded {
		warnings = append(warnings, shardedPITRWarning("source cluster "+src.Spec.ClusterRef.Name))
	}

	w, e := v.validatePITREnabled(ctx, src, t)
	warnings, errs = append(warnings, w...), append(errs, e...)

	w, e = validateRestorableWindow(src, t)
	return append(warnings, w...), append(errs, e...)
}

// validatePITREnabled — 규칙 2. source 백업이 떠진 클러스터가 PITR 을 켜고
// 있었는지. 꺼져 있으면 replay 할 archived oplog segment 자체가 없다.
//
// 클러스터 CR 이 이미 없는 경우는 *거부하지 않는다* — 클러스터가 사라진 뒤
// 백업으로 새 클러스터를 세우는 것이 DR 의 본류이고, 그때 원본 CR 이 없는 건
// 정상이다. 확인 불가는 Warning 으로 넘기고, 실제 segment 유무는 restore
// job(authoritative)이 판정한다.
func (v *MongoDBBackupCustomValidator) validatePITREnabled(ctx context.Context, src *mongodbv1beta1.MongoDBBackup, t *pitTarget) (admission.Warnings, field.ErrorList) {
	ref := src.Spec.ClusterRef
	key := types.NamespacedName{Name: ref.Name, Namespace: src.Namespace}

	var spec *mongodbv1beta1.BackupSpec
	switch ref.Kind {
	case clusterKindMongoDB:
		c := &mongodbv1beta1.MongoDB{}
		if err := v.Reader.Get(ctx, key, c); err != nil {
			return clusterLookupWarning(ref.Kind, ref.Name, err), nil
		}
		spec = c.Spec.Backup
	case clusterKindSharded:
		c := &mongodbv1beta1.MongoDBSharded{}
		if err := v.Reader.Get(ctx, key, c); err != nil {
			return clusterLookupWarning(ref.Kind, ref.Name, err), nil
		}
		spec = c.Spec.Backup
	default:
		// ClusterRef.Kind 는 CRD enum 이 강제 — 도달 시 판정 근거 없음.
		return admission.Warnings{fmt.Sprintf(
			"spec.restore: cannot verify pitrEnabled — source backup %q has unknown clusterRef.kind %q",
			src.Name, ref.Kind)}, nil
	}

	if spec == nil || !spec.PITREnabled {
		return nil, field.ErrorList{field.Invalid(t.path, t.raw, fmt.Sprintf(
			"point-in-time restore requires backup.pitrEnabled=true on the source cluster %s/%s — "+
				"that cluster archives no oplog, so only base-snapshot restore is possible "+
				"(omit pointInTime/pointInTimeTimestamp)",
			ref.Kind, ref.Name))}
	}
	return nil, nil
}

// validateRestorableWindow — 규칙 3(base 앵커) + 규칙 4(window 경계).
//
// 하한 = EarliestRestore(uploader 계산) ?? OplogStart(base 아카이브의 불변 사실).
// 둘은 구조적으로 동치라(Status godoc) uploader 가 아직 안 돌았어도 OplogStart
// 로 하한을 잡을 수 있다.
//
// 상한 = LatestRestore. 아직 기록 전(nil)이면 *거부하지 않는다* — 관측치 부재는
// 오류의 증거가 아니다. Warning 후 restore job 에 판정을 넘긴다.
//
// 비교는 초 단위로만 한다: window 는 RFC3339 초 정밀도라 BSON ordinal 을 대조할
// 상대가 없다. 초 내 경계는 S3 segment 키가 진본이고 restore job 이 판정한다.
func validateRestorableWindow(src *mongodbv1beta1.MongoDBBackup, t *pitTarget) (admission.Warnings, field.ErrorList) {
	st := src.Status

	// 규칙 3 — base 스냅샷의 oplog 앵커가 없으면 replay 하한이 정의되지 않는다
	// (= `mongodump --oplog` 없이 떠진 백업).
	if st.OplogStart == nil {
		return nil, field.ErrorList{field.Invalid(t.path, t.raw, fmt.Sprintf(
			"source backup %q has no status.oplogStart — it was taken without an oplog anchor and "+
				"cannot start a point-in-time replay (omit pointInTime for a base-snapshot restore)",
			src.Name))}
	}

	lower := st.OplogStart
	if st.EarliestRestore != nil {
		lower = st.EarliestRestore
	}
	if t.sec < lower.Unix() {
		return nil, field.ErrorList{field.Invalid(t.path, t.raw, fmt.Sprintf(
			"is before the restorable window lower bound %s — oplog replay starts at the base "+
				"snapshot and cannot go back before it",
			lower.UTC().Format(time.RFC3339)))}
	}

	if st.LatestRestore == nil {
		return admission.Warnings{fmt.Sprintf(
			"spec.restore: source backup %q has no status.latestRestore yet — the restorable window "+
				"upper bound is unknown, so the target time was not verified; the restore job will "+
				"fail if no oplog segment chain reaches it", src.Name)}, nil
	}
	if t.sec > st.LatestRestore.Unix() {
		return nil, field.ErrorList{field.Invalid(t.path, t.raw, fmt.Sprintf(
			"is after the restorable window upper bound %s — no archived oplog segment chain reaches "+
				"this point (it is in the future, or retention already trimmed the segments)",
			st.LatestRestore.UTC().Format(time.RFC3339)))}
	}
	return nil, nil
}

// pitTarget — 확정된 복구 목표 시점.
type pitTarget struct {
	sec  int64       // unix epoch 초 (window 대조용)
	raw  string      // 사용자 표기 원문 (에러 메시지용)
	path *field.Path // 값을 읽은 필드 (pointInTime | pointInTimeTimestamp)
}

// resolvePointInTime — 복구 목표 시점을 확정한다.
//
// PointInTimeTimestamp 가 설정되면 PointInTime 보다 **우선**한다(RestoreSpec
// godoc — 초 내 순번까지 끊어야 할 때 쓰는 값이므로 더 구체적인 쪽이 이긴다).
// 둘 다 nil 이면 base 스냅샷 시점 복원 → nil 반환.
func resolvePointInTime(p *field.Path, r *mongodbv1beta1.RestoreSpec) (*pitTarget, field.ErrorList) {
	if r.PointInTimeTimestamp != nil {
		path := p.Child("pointInTimeTimestamp")
		raw := *r.PointInTimeTimestamp
		sec, err := parseBSONTimestamp(raw)
		if err != nil {
			return nil, field.ErrorList{field.Invalid(path, raw, err.Error())}
		}
		return &pitTarget{sec: sec, raw: raw, path: path}, nil
	}
	if r.PointInTime != nil {
		return &pitTarget{
			sec:  r.PointInTime.Unix(),
			raw:  r.PointInTime.UTC().Format(time.RFC3339),
			path: p.Child("pointInTime"),
		}, nil
	}
	return nil, nil
}

// parseBSONTimestamp — "<sec>:<ordinal>" → epoch 초.
//
// CRD pattern marker 가 형식을 강제하지만 어차피 sec 을 얻으려면 파싱해야 한다 —
// 아래 오류 분기는 그 파싱의 자연스러운 산물이지 marker 불신이 아니다. ordinal
// 은 BSON timestamp 의 uint32 순번이라 범위까지 본다(대조 상대는 없지만 형식이
// 틀리면 restore job 의 --oplogLimit 이 깨진다).
func parseBSONTimestamp(raw string) (int64, error) {
	secStr, ordStr, ok := strings.Cut(raw, ":")
	if !ok {
		return 0, fmt.Errorf("must be a BSON timestamp %q — e.g. \"1752710400:7\"", "<seconds>:<ordinal>")
	}
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil || sec < 0 {
		return 0, fmt.Errorf("seconds part %q must be a non-negative unix epoch integer", secStr)
	}
	if _, err := strconv.ParseUint(ordStr, 10, 32); err != nil {
		return 0, fmt.Errorf("ordinal part %q must be an integer in the uint32 range", ordStr)
	}
	return sec, nil
}

// shardedPITRWarning — sharded PITR 정합 미보장 경고(거부 아님).
func shardedPITRWarning(subject string) string {
	return "spec.restore: PITR is not consistency-guaranteed for sharded clusters (" + subject +
		") — each shard advances its own oplog timestamp, so a single point in time cannot define a " +
		"cluster-wide consistent cut; the restore proceeds per-shard best-effort"
}

// clusterLookupWarning — source 클러스터 조회 실패는 PITR 거부 사유가 아니다
// (validatePITREnabled 주석 참조).
func clusterLookupWarning(kind, name string, err error) admission.Warnings {
	reason := "lookup failed: " + err.Error()
	if apierrors.IsNotFound(err) {
		reason = "it no longer exists (restoring after the source cluster was deleted is a normal DR path)"
	}
	return admission.Warnings{fmt.Sprintf(
		"spec.restore: cannot verify backup.pitrEnabled on source cluster %s/%s — %s; "+
			"the restore job will fail if no oplog segments are archived", kind, name, reason)}
}
