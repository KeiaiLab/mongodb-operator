/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbsearch_controller.go — Phase 1: MongoDBSearch reconcile.
//
// source MongoDB 옆에 mongot(검색 엔진) StatefulSet/Service/ConfigMap/NetworkPolicy 를
// 배포하고, source MongoDB CR 에 mongot endpoint annotation 을 설정한다(mongod builder 가
// 읽어 setParameter 주입 — 없으면 무변경=무롤링). searchCoordinator sync 사용자는 MVP 에서
// spec.syncUserSecretRef 로 사용자가 제공(secret: username/password). auto-create 는 후속.

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

	appsv1 "k8s.io/api/apps/v1"
	netv1 "k8s.io/api/networking/v1"

	commonsapply "github.com/keiailab/keiailab-commons/pkg/apply"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const defaultSyncUser = "search-sync"

// MongoDBSearchReconciler reconciles MongoDBSearch — mongot deployment.
type MongoDBSearchReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

func (r *MongoDBSearchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("mongodbsearch", req.NamespacedName)

	search := &mongodbv1beta1.MongoDBSearch{}
	if err := r.Get(ctx, req.NamespacedName, search); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// MVP: source = MongoDB(ReplicaSet). Sharded 는 후속(shard 별 mongot).
	if search.Spec.Source.MongoDBResourceRef == nil || search.Spec.Source.Kind == "MongoDBSharded" {
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
	syncUser, syncSecret := defaultSyncUser, search.Spec.SyncUserSecretRef.Name
	if u := r.secretUsername(ctx, syncSecret, search.Namespace); u != "" {
		syncUser = u
	}

	hosts := sourceReplicaSetHosts(source)
	tlsEnabled := source.Spec.TLS != nil && source.Spec.TLS.Enabled

	// mongot 리소스 apply(owner=search → CR 삭제 시 GC).
	if err := commonsapply.ConfigMap(ctx, r.Client, r.Scheme, search, resources.BuildMongotConfigMap(search, hosts, syncUser, tlsEnabled)); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply mongot configmap: %w", err)
	}
	if err := commonsapply.Service(ctx, r.Client, r.Scheme, search, resources.BuildMongotService(search)); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply mongot service: %w", err)
	}
	if err := commonsapply.StatefulSet(ctx, r.Client, r.Scheme, search, resources.BuildMongotStatefulSet(search, syncSecret), false); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply mongot statefulset: %w", err)
	}
	if err := commonsapply.NetworkPolicy(ctx, r.Client, r.Scheme, search, resources.BuildMongotNetworkPolicy(search)); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply mongot networkpolicy: %w", err)
	}

	// source mongod 에 mongot endpoint annotation → mongod builder 가 setParameter 주입.
	endpoint := resources.MongotEndpoint(search.Name, search.Namespace)
	tlsMode := "disabled"
	if tlsEnabled {
		tlsMode = "requireTLS"
	}
	if err := r.annotateSource(ctx, source, endpoint, tlsMode); err != nil {
		return ctrl.Result{}, fmt.Errorf("annotate source mongodb: %w", err)
	}

	// status: mongot STS ready 여부.
	sts := &appsv1.StatefulSet{}
	ready := int32(0)
	if err := r.Get(ctx, types.NamespacedName{Name: search.Name + "-mongot", Namespace: search.Namespace}, sts); err == nil {
		ready = sts.Status.ReadyReplicas
	}
	phase := "Provisioning"
	if ready > 0 {
		phase = "Ready"
	}
	search.Status.Phase = phase
	search.Status.MongotEndpoint = endpoint
	search.Status.ReadyReplicas = ready
	search.Status.ObservedGeneration = search.Generation
	search.Status.Error = ""
	if err := r.Status().Update(ctx, search); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("reconciled", "phase", phase, "endpoint", endpoint, "ready", ready)
	if phase != "Ready" {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// sourceReplicaSetHosts — source MongoDB RS 멤버 host:27017 목록(mongot syncSource).
func sourceReplicaSetHosts(mdb *mongodbv1alpha1.MongoDB) []string {
	n := mdb.Spec.Members
	if n < 1 {
		n = 1
	}
	hosts := make([]string, 0, n)
	for i := int32(0); i < n; i++ {
		hosts = append(hosts, fmt.Sprintf("%s-%d.%s-headless.%s.svc.cluster.local:27017", mdb.Name, i, mdb.Name, mdb.Namespace))
	}
	return hosts
}

func (r *MongoDBSearchReconciler) secretUsername(ctx context.Context, name, ns string) string {
	s := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, s); err != nil {
		return ""
	}
	return string(s.Data["username"])
}

func (r *MongoDBSearchReconciler) annotateSource(ctx context.Context, source *mongodbv1alpha1.MongoDB, endpoint, tlsMode string) error {
	if source.Annotations[resources.MongotSearchEndpointAnnotation] == endpoint &&
		source.Annotations[resources.MongotTLSModeAnnotation] == tlsMode {
		return nil
	}
	patched := source.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	patched.Annotations[resources.MongotSearchEndpointAnnotation] = endpoint
	patched.Annotations[resources.MongotTLSModeAnnotation] = tlsMode
	return r.Patch(ctx, patched, client.MergeFrom(source))
}

func (r *MongoDBSearchReconciler) pending(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, msg string) (ctrl.Result, error) {
	search.Status.Phase = "Pending"
	search.Status.Error = msg
	_ = r.Status().Update(ctx, search)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *MongoDBSearchReconciler) fail(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, msg string) (ctrl.Result, error) {
	search.Status.Phase = "Failed"
	search.Status.Error = msg
	_ = r.Status().Update(ctx, search)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *MongoDBSearchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1beta1.MongoDBSearch{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&netv1.NetworkPolicy{}).
		Complete(r)
}
