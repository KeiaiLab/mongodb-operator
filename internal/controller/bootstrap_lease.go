/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// bootstrapLeaseDuration은 BootstrapAdminUser가 RS primary 선출 + anonymous
// connect + createUser + post-verify까지 완료할 충분한 시간이다. 30s면 정상
// path에서 충분하고, holder pod이 죽었을 때 takeover까지 대기 시간으로도 적절.
const bootstrapLeaseDuration = 30 * time.Second

// errBootstrapBusy는 다른 reconcile loop이 부트스트랩 lease를 점유 중일 때
// reconcileAdminUser가 반환하는 sentinel. Reconcile은 이를 짧은 requeue로
// 흡수하며 phase=Failed로 전이시키지 않는다 (transient busy).
var errBootstrapBusy = errors.New("bootstrap lease busy")

// bootstrapLeaseHolder는 현재 controller process를 식별한다.
// controller-runtime의 leader-election과는 *별개*의 resource-level lock이라
// process-unique 식별자(host+pid)만 있으면 된다 — leader pod이 동일 CR에
// 대해 동시에 두 reconcile loop을 돌리는 시나리오를 차단하기 위함.
func bootstrapLeaseHolder() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// bootstrapLeaseName은 mdb 별 lease 이름. 같은 namespace 안에서 mdb 인스턴스
// 단위로 직렬화된다.
func bootstrapLeaseName(mdb *mongodbv1alpha1.MongoDB) string {
	return fmt.Sprintf("mongodb-bootstrap-%s", mdb.Name)
}

// acquireBootstrapLease는 mdb 부트스트랩 진입을 직렬화하는 K8s Lease를 점유한다.
//
// 반환값:
//
//	(lease, true,  nil) — 점유 성공. 호출자는 작업 후 releaseBootstrapLease 호출.
//	(nil,   false, nil) — 다른 holder가 유효한 lease 보유 중. 호출자는 requeue.
//	(nil,   false, err) — API 오류. 호출자는 status에 노출.
//
// CAS: Get → modify → Update 흐름에서 ResourceVersion conflict는 다른 reconcile
// 이 동시 갱신했다는 뜻이므로 busy로 해석한다 (false, nil 반환).
func (r *MongoDBReconciler) acquireBootstrapLease(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (*coordinationv1.Lease, bool, error) {
	leaseName := bootstrapLeaseName(mdb)
	holder := bootstrapLeaseHolder()
	durationSec := int32(bootstrapLeaseDuration / time.Second)
	now := metav1.NewMicroTime(time.Now())

	existing := &coordinationv1.Lease{}
	err := r.Get(ctx, types.NamespacedName{Name: leaseName, Namespace: mdb.Namespace}, existing)

	if apierrors.IsNotFound(err) {
		fresh := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: mdb.Namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holder,
				LeaseDurationSeconds: &durationSec,
				AcquireTime:          &now,
				RenewTime:            &now,
			},
		}
		if err := controllerutil.SetControllerReference(mdb, fresh, r.Scheme); err != nil {
			return nil, false, err
		}
		if err := r.Create(ctx, fresh); err != nil {
			// 다른 reconcile이 동시에 막 만든 상태 — busy.
			if apierrors.IsAlreadyExists(err) {
				return nil, false, nil
			}
			return nil, false, err
		}
		return fresh, true, nil
	}
	if err != nil {
		return nil, false, err
	}

	// 다른 holder가 valid한 lease를 보유 중인지 검사.
	if existing.Spec.HolderIdentity != nil &&
		*existing.Spec.HolderIdentity != holder &&
		existing.Spec.RenewTime != nil &&
		existing.Spec.LeaseDurationSeconds != nil {
		deadline := existing.Spec.RenewTime.Add(time.Duration(*existing.Spec.LeaseDurationSeconds) * time.Second)
		if time.Now().Before(deadline) {
			return nil, false, nil
		}
	}

	// 자가 점유 중이거나 expired → 갱신 시도(CAS via ResourceVersion).
	existing.Spec.HolderIdentity = &holder
	existing.Spec.AcquireTime = &now
	existing.Spec.RenewTime = &now
	existing.Spec.LeaseDurationSeconds = &durationSec
	if err := r.Update(ctx, existing); err != nil {
		if apierrors.IsConflict(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return existing, true, nil
}

// releaseBootstrapLease는 점유한 lease를 삭제한다. delete 실패는 무시 — TTL이
// 짧아 어차피 자동으로 expired 처리되며, 다음 reconcile이 takeover 가능.
func (r *MongoDBReconciler) releaseBootstrapLease(ctx context.Context, lease *coordinationv1.Lease) {
	if lease == nil {
		return
	}
	_ = r.Delete(ctx, lease, &client.DeleteOptions{})
}
