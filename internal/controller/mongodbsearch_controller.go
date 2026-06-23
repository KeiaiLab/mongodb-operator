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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonsapply "github.com/keiailab/keiailab-commons/pkg/apply"
	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	defaultSyncUser         = "search-sync"
	kindMongoDBSharded      = "MongoDBSharded"
	searchPhasePending      = "Pending"
	searchPhaseProvisioning = "Provisioning"
	searchPhaseSyncing      = "Syncing"
	searchPhaseReady        = "Ready"
	searchPhaseDegraded     = "Degraded"
	searchPhaseFailed       = "Failed"
	mongodbPhaseRunning     = "Running"
	mongotSidecarEndpoint   = "localhost:27028" // sidecar — mongod 와 동일 pod localhost
	mongotContainerName     = "mongot"          // mongod pod 내 sidecar 컨테이너 이름(resources.MongotSidecar)

	// condition types — k8s convention(Available/Progressing/Degraded).
	conditionTypeSearchAvailable   = "Available"
	conditionTypeSearchProgressing = "Progressing"
	conditionTypeSearchDegraded    = "Degraded"
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
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch

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

	// status: mongot 은 별도 워크로드가 아니라 source mongod pod 의 sidecar 컨테이너 —
	// 실제 컨테이너 readiness 로 phase 결정. source not Running/STS 미생성 → Provisioning,
	// sidecar 일부 ready → Syncing, 전부 ready → Ready, sidecar 있으나 0 ready → Degraded.
	var readyReplicas, totalSidecars int32
	phase := searchPhaseProvisioning
	if source.Status.Phase == mongodbPhaseRunning {
		readyReplicas, totalSidecars, err = r.countReadyMongotSidecars(ctx, source)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("count mongot sidecars: %w", err)
		}
		phase = searchPhaseFromReadiness(readyReplicas, totalSidecars)
	}

	apply := func() {
		search.Status.Phase = phase
		search.Status.MongotEndpoint = mongotSidecarEndpoint
		search.Status.ReadyReplicas = readyReplicas
		search.Status.ObservedGeneration = search.Generation
		search.Status.Error = ""
		setSearchConditions(&search.Status.Conditions, search.Generation, phase)
	}
	apply()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, search, apply); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("reconciled (sidecar)", "phase", phase, "readyMongot", readyReplicas, "totalMongot", totalSidecars, "image", image)
	if phase != searchPhaseReady {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// countReadyMongotSidecars — source mongod pod 들의 mongot sidecar 컨테이너 ready 집계.
// mongot 은 별도 STS 가 아니라 mongod pod 의 sidecar 이므로, source STS selector 로 pod 를
// 조회해 ContainerStatuses[mongot].Ready 를 센다(upgrade.go pod 조회 패턴 재사용).
// total=0 = 아직 sidecar 가 주입된 pod 없음(annotation reconcile 전/롤링 전).
func (r *MongoDBSearchReconciler) countReadyMongotSidecars(ctx context.Context, source *mongodbv1alpha1.MongoDB) (ready, total int32, err error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: source.Name, Namespace: source.Namespace}, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, 0, nil // STS 아직 미생성
		}
		return 0, 0, err
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(source.Namespace), client.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
		return 0, 0, err
	}
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.Name == mongotContainerName {
				total++
				if cs.Ready {
					ready++
				}
			}
		}
	}
	return ready, total, nil
}

// searchPhaseFromReadiness — mongot sidecar ready/total 집계로 phase 결정.
func searchPhaseFromReadiness(ready, total int32) string {
	switch {
	case total == 0:
		return searchPhaseProvisioning // sidecar 아직 미주입(롤링 전)
	case ready == 0:
		return searchPhaseDegraded // sidecar 있으나 0 ready
	case ready < total:
		return searchPhaseSyncing // 일부 ready(롤링 진행 중)
	default:
		return searchPhaseReady // 전부 ready
	}
}

// setSearchConditions — phase 기반 표준 conditions(Available/Progressing/Degraded) 설정.
// meta.SetStatusCondition(ADR-0013) 위임 — Status 전이 시에만 LastTransitionTime 갱신.
func setSearchConditions(conds *[]metav1.Condition, generation int64, phase string) {
	avail, prog, degraded := metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse
	switch phase {
	case searchPhaseReady:
		avail = metav1.ConditionTrue
	case searchPhaseProvisioning, searchPhaseSyncing, searchPhasePending:
		prog = metav1.ConditionTrue
	case searchPhaseDegraded, searchPhaseFailed:
		degraded = metav1.ConditionTrue
	}
	reason := "Phase" + phase
	msg := "mongot sidecar phase=" + phase
	meta.SetStatusCondition(conds, metav1.Condition{
		Type: conditionTypeSearchAvailable, Status: avail, ObservedGeneration: generation, Reason: reason, Message: msg,
	})
	meta.SetStatusCondition(conds, metav1.Condition{
		Type: conditionTypeSearchProgressing, Status: prog, ObservedGeneration: generation, Reason: reason, Message: msg,
	})
	meta.SetStatusCondition(conds, metav1.Condition{
		Type: conditionTypeSearchDegraded, Status: degraded, ObservedGeneration: generation, Reason: reason, Message: msg,
	})
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
	apply := func() {
		search.Status.Phase = searchPhasePending
		search.Status.Error = msg
		setSearchConditions(&search.Status.Conditions, search.Generation, searchPhasePending)
	}
	apply()
	_ = commonsstatus.UpdateWithRetry(ctx, r.Client, search, apply)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *MongoDBSearchReconciler) fail(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, msg string) (ctrl.Result, error) {
	apply := func() {
		search.Status.Phase = searchPhaseFailed
		search.Status.Error = msg
		setSearchConditions(&search.Status.Conditions, search.Generation, searchPhaseFailed)
	}
	apply()
	_ = commonsstatus.UpdateWithRetry(ctx, r.Client, search, apply)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *MongoDBSearchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1beta1.MongoDBSearch{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
