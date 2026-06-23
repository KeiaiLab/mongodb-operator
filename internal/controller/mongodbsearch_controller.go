/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbsearch_controller.go — Phase 1.1: MongoDBSearch reconcile (sidecar 모델).
//
// Community mongot 은 localhost mongod 로 topology 연결 → mongod pod 의 sidecar 여야 한다
// (별도 StatefulSet 비호환, kind e2e 실증). 따라서 search controller 는 mongot 을 *직접
// 배포하지 않고*: ① mongot config ConfigMap(localhost syncSource) 생성 ② source MongoDB 에
// sidecar annotation(image/sync-secret/tls) 설정 → mongod builder 가 mongot sidecar 컨테이너 +
// init + setParameter 를 mongod pod 에 주입. searchCoordinator sync 사용자는 spec.syncUserSecretRef
// 로 사용자 제공(secret: username/password). auto-create 는 후속.

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonsapply "github.com/keiailab/keiailab-commons/pkg/apply"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	defaultSyncUser         = "search-sync"
	kindMongoDBSharded      = "MongoDBSharded"
	searchPhasePending      = "Pending"
	searchPhaseProvisioning = "Provisioning"
	searchPhaseReady        = "Ready"
	searchPhaseFailed       = "Failed"
	mongodbPhaseRunning     = "Running"
	mongotSidecarEndpoint   = "localhost:27028" // sidecar — mongod 와 동일 pod localhost
)

// MongoDBSearchReconciler reconciles MongoDBSearch — mongot sidecar 활성화.
type MongoDBSearchReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *MongoDBSearchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("mongodbsearch", req.NamespacedName)

	search := &mongodbv1beta1.MongoDBSearch{}
	if err := r.Get(ctx, req.NamespacedName, search); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// MVP: source = MongoDB(ReplicaSet). Sharded 는 후속(shard 별 sidecar).
	if search.Spec.Source.MongoDBResourceRef == nil || search.Spec.Source.Kind == kindMongoDBSharded {
		return r.fail(ctx, search, "source.mongodbResourceRef(Kind=MongoDB) required (Sharded not yet supported)")
	}
	source := &mongodbv1alpha1.MongoDB{}
	if err := r.Get(ctx, types.NamespacedName{Name: search.Spec.Source.MongoDBResourceRef.Name, Namespace: search.Namespace}, source); err != nil {
		if apierrors.IsNotFound(err) {
			return r.pending(ctx, search, "source MongoDB not found yet")
		}
		return ctrl.Result{}, err
	}

	// MVP: syncUserSecretRef 필수(searchCoordinator 사용자 — 사용자 선생성).
	if search.Spec.SyncUserSecretRef == nil {
		return r.fail(ctx, search, "spec.syncUserSecretRef required (searchCoordinator user secret with username/password)")
	}
	syncSecret := search.Spec.SyncUserSecretRef.Name
	syncUser, err := r.resolveSyncUser(ctx, syncSecret, search.Namespace)
	if err != nil {
		return r.fail(ctx, search, fmt.Sprintf("sync secret invalid: %v", err))
	}

	tlsEnabled := source.Spec.TLS != nil && source.Spec.TLS.Enabled
	tlsMode := "disabled"
	if tlsEnabled {
		tlsMode = "requireTLS"
	}
	image := resources.MongotImage(search.Spec.Version)

	// mongot config ConfigMap(sidecar, localhost syncSource). owner=search → CR 삭제 시 GC.
	cm := resources.BuildMongotConfigMap(source.Name, search.Namespace, search.Name, syncUser, tlsEnabled)
	if err := commonsapply.ConfigMap(ctx, r.Client, r.Scheme, search, cm); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply mongot configmap: %w", err)
	}

	// source mongod 에 sidecar annotation → mongod builder 가 mongot sidecar + setParameter 주입.
	if err := r.annotateSource(ctx, source, image, syncSecret, tlsMode); err != nil {
		return ctrl.Result{}, fmt.Errorf("annotate source mongodb: %w", err)
	}

	// status: source mongod Running 이면 sidecar 도 running pod 에 포함 → Ready.
	phase := searchPhaseProvisioning
	if source.Status.Phase == mongodbPhaseRunning {
		phase = searchPhaseReady
	}
	search.Status.Phase = phase
	search.Status.MongotEndpoint = mongotSidecarEndpoint
	search.Status.ObservedGeneration = search.Generation
	search.Status.Error = ""
	if err := r.Status().Update(ctx, search); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("reconciled (sidecar)", "phase", phase, "image", image)
	if phase != searchPhaseReady {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// resolveSyncUser — syncUserSecretRef 검증 + username 결정. secret GET 실패 → error(fail-fast).
// secret 존재하나 username 키 부재 → defaultSyncUser. silent 폴백 금지(cross-review).
func (r *MongoDBSearchReconciler) resolveSyncUser(ctx context.Context, name, ns string) (string, error) {
	s := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, s); err != nil {
		return "", fmt.Errorf("get sync secret %s: %w", name, err)
	}
	if u := string(s.Data["username"]); u != "" {
		return u, nil
	}
	return defaultSyncUser, nil
}

// annotateSource — source MongoDB 에 sidecar annotation 설정(idempotent — 동일 값이면 skip).
func (r *MongoDBSearchReconciler) annotateSource(ctx context.Context, source *mongodbv1alpha1.MongoDB, image, syncSecret, tlsMode string) error {
	if source.Annotations[resources.MongotSidecarImageAnnotation] == image &&
		source.Annotations[resources.MongotSyncSecretAnnotation] == syncSecret &&
		source.Annotations[resources.MongotTLSModeAnnotation] == tlsMode {
		return nil
	}
	patched := source.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	patched.Annotations[resources.MongotSidecarImageAnnotation] = image
	patched.Annotations[resources.MongotSyncSecretAnnotation] = syncSecret
	patched.Annotations[resources.MongotTLSModeAnnotation] = tlsMode
	return r.Patch(ctx, patched, client.MergeFrom(source))
}

func (r *MongoDBSearchReconciler) pending(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, msg string) (ctrl.Result, error) {
	search.Status.Phase = searchPhasePending
	search.Status.Error = msg
	_ = r.Status().Update(ctx, search)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *MongoDBSearchReconciler) fail(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, msg string) (ctrl.Result, error) {
	search.Status.Phase = searchPhaseFailed
	search.Status.Error = msg
	_ = r.Status().Update(ctx, search)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *MongoDBSearchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1beta1.MongoDBSearch{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
