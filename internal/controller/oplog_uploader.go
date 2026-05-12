/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// oplog_uploader.go — F03 (cycle 1) PITR Oplog S3 uploader controller.
//
// 책임: BackupSpec.PITREnabled=true 인 MongoDB / MongoDBSharded CR 을 watch
// 하여 *cluster 별 retention 만료 oplog 객체 정리* + *uploader CronJob 생성/
// 동기화* 를 담당한다. 실제 oplog 객체는 sidecar (F02) 가 mongod pod 의
// EmptyDir 에 떨어뜨리고, uploader CronJob 이 *주기적* (default 1-min)
// 으로 staging volume 에 접근하여 S3 (또는 PVC) 로 mv 한다.
//
// 본 controller 는 *skeleton + reconciliation hook* 만 제공. 실제 S3 multi-
// part upload + ETag 검증 + retention prune 로직은 후속 cycle (cycle 6+ KMS
// 통합 시점) 에서 강화 — 본 cycle 은 *uploader 의 controller 존재 + 등록 +
// MongoDB/MongoDBSharded watch + 빈 reconcile loop* 까지가 acceptance.
//
// 회귀 가드: oplog_uploader_test.go 가 Reconciler.IsApplicable 정합 검증.

package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// OplogUploaderReconciler 는 PITR 활성 클러스터의 oplog 업로드 정책을
// reconcile 한다. MongoDB / MongoDBSharded 양쪽 CRD 의 Spec.Backup 필드를
// 관찰한다.
type OplogUploaderReconciler struct {
	client.Client
}

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

// Reconcile 은 MongoDB / MongoDBSharded 의 backup spec 변화에 대응한다.
// 본 cycle 1 acceptance 의 skeleton — 향후 cycle:
//   - cycle 2: rollover 통계 메트릭 노출 (uploader queue depth 등)
//   - cycle 6: KMS encryption + ETag verify 정합
//   - cycle 9: retention prune (OplogRetentionHours 만료 객체 삭제)
//
//nolint:unparam // ctrl.Result 빈 값은 reconcile 완료 / err 는 caller 전파.
func (r *OplogUploaderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("uploader", req.NamespacedName)

	// MongoDB CR 우선 lookup
	mdb := &mongodbv1alpha1.MongoDB{}
	if err := r.Get(ctx, req.NamespacedName, mdb); err == nil {
		if r.IsApplicable(mdb.Spec.Backup) {
			logger.V(1).Info("oplog uploader reconcile (MongoDB)",
				"pitr", true,
				"retentionHours", mdb.Spec.Backup.OplogRetentionHours)
		}
		return ctrl.Result{}, nil
	}

	// MongoDBSharded CR fallback
	mdbsh := &mongodbv1alpha1.MongoDBSharded{}
	if err := r.Get(ctx, req.NamespacedName, mdbsh); err == nil {
		if r.IsApplicable(mdbsh.Spec.Backup) {
			logger.V(1).Info("oplog uploader reconcile (MongoDBSharded)",
				"pitr", true,
				"retentionHours", mdbsh.Spec.Backup.OplogRetentionHours)
		}
		return ctrl.Result{}, nil
	}

	// 두 CRD 모두 not-found → 정리 noop (이미 controller-runtime cache 에서 drop).
	return ctrl.Result{}, nil
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
