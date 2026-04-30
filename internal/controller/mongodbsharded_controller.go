/*
Copyright 2024 Keiailab.

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

package controller

import (
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	mongodbShardedFinalizer = "mongodbsharded.keiailab.com/finalizer"
)

// MongoDBShardedReconciler reconciles a MongoDBSharded object
type MongoDBShardedReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// EnableAutoscaling 게이트 — false면 reconcileMongosHPA / reconcileConfigServerHPA가
	// no-op로 종료. cmd/main.go의 --enable-autoscaling flag에서 주입.
	EnableAutoscaling bool
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

func (r *MongoDBShardedReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling MongoDBSharded", "namespace", req.Namespace, "name", req.Name)

	// Fetch MongoDBSharded instance
	mdbsh := &mongodbv1alpha1.MongoDBSharded{}
	if err := r.Get(ctx, req.NamespacedName, mdbsh); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("MongoDBSharded resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get MongoDBSharded")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !mdbsh.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, mdbsh)
	}

	// Add finalizer if needed
	if !controllerutil.ContainsFinalizer(mdbsh, mongodbShardedFinalizer) {
		controllerutil.AddFinalizer(mdbsh, mongodbShardedFinalizer)
		if err := r.Update(ctx, mdbsh); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Update status phase to Initializing if pending
	if mdbsh.Status.Phase == "" || mdbsh.Status.Phase == mongodbv1alpha1.ShardedPhasePending {
		mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseInitializing
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile resources in order

	// 1. Keyfile Secret
	if err := r.reconcileKeyfileSecret(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "KeyfileSecret", err)
	}

	// 2. Config Server
	if err := r.reconcileConfigServer(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "ConfigServer", err)
	}

	// 3. Wait for Config Server to be ready
	if !r.isConfigServerReady(ctx, mdbsh) {
		logger.Info("Waiting for config server to be ready")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// 4. Shards
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if err := r.reconcileShard(ctx, mdbsh, i); err != nil {
			return r.updateStatusError(ctx, mdbsh, fmt.Sprintf("Shard-%d", i), err)
		}
	}

	// 5. Wait for Shards to be ready
	if !r.areShardsReady(ctx, mdbsh) {
		logger.Info("Waiting for shards to be ready")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// 6. Mongos
	if err := r.reconcileMongos(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "Mongos", err)
	}

	// 6.5. PodDisruptionBudgets (opt-in, 모든 컴포넌트 cfg/shards/mongos)
	if err := r.reconcileShardedPDBs(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "PodDisruptionBudgets", err)
	}

	// 6.6. NetworkPolicies (opt-in, 컴포넌트별 deny-by-default)
	if err := r.reconcileShardedNetworkPolicies(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "NetworkPolicies", err)
	}

	// 6.7. Mongos HPA (opt-in via Spec.Mongos.AutoScaling.Enabled — ADR-0007)
	if err := r.reconcileMongosHPA(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "MongosHPA", err)
	}

	// 6.8. ConfigServer HPA (opt-in via Spec.ConfigServer.AutoScaling.Enabled +
	// ScalePolicy.Deliberate — ADR-0008/0009 이중 가드)
	if err := r.reconcileConfigServerHPA(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "ConfigServerHPA", err)
	}

	// 7. Initialize Config Server replica set.
	// 이전: 모든 단계가 silent. 운영자는 cfg server가 "no replset config"로
	// 영구 멈춰도 conditions에서 인지 불가. updateStatusError로 ReconcileError
	// condition을 남기고 controller-runtime의 자동 backoff에 맡긴다.
	if !mdbsh.Status.ConfigServerInitialized {
		if err := r.reconcileConfigServerInit(ctx, mdbsh); err != nil {
			return r.updateStatusError(ctx, mdbsh, "ConfigServerInit", err)
		}
	}

	// 8. Initialize Shard replica sets — reconcileShardsInit이 부분 실패를
	// errors.Join으로 묶어 반환. 호출자가 status에 노출.
	if err := r.reconcileShardsInit(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "ShardsInit", err)
	}

	// 9. Wait for mongos to be ready
	if !r.isMongosReady(ctx, mdbsh) {
		logger.Info("Waiting for mongos to be ready")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// 10. Create admin user
	if !mdbsh.Status.AdminUserCreated {
		if err := r.reconcileShardedAdminUser(ctx, mdbsh); err != nil {
			return r.updateStatusError(ctx, mdbsh, "AdminUser", err)
		}
	}

	// 10.5. Scale-in: spec.shardCount < status.shardCount면 잉여 shard drain.
	// 진행 중이면 ShardDraining condition + elapsed-based backoff requeue.
	if scaleInRes, err := r.reconcileScaleIn(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "ScaleIn", err)
	} else if scaleInRes.RequeueAfter > 0 {
		return scaleInRes, nil
	}

	// 11. Add shards to cluster — reconcileAddShards도 errors.Join 누적 패턴.
	if err := r.reconcileAddShards(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "AddShards", err)
	}

	// 12. Update status
	if err := r.updateStatus(ctx, mdbsh); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled MongoDBSharded")
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *MongoDBShardedReconciler) handleDeletion(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling MongoDBSharded deletion")

	if controllerutil.ContainsFinalizer(mdbsh, mongodbShardedFinalizer) {
		// Perform cleanup logic here if needed

		// Remove finalizer
		controllerutil.RemoveFinalizer(mdbsh, mongodbShardedFinalizer)
		if err := r.Update(ctx, mdbsh); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *MongoDBShardedReconciler) reconcileKeyfileSecret(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	// Keyfile은 sharded 인증용 — 모든 pod에 동일 값 유지. 멱등 helper로 통합.
	return reconcileSecretIfNotExists(ctx, r.Client, r.Scheme, mdbsh, mdbsh.Name+"-keyfile",
		func() *corev1.Secret { return resources.BuildShardedKeyfileSecret(mdbsh) })
}

func (r *MongoDBShardedReconciler) reconcileConfigServer(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	// Scripts ConfigMap (admin bootstrap + readiness, port 27019)
	// StatefulSet 생성 전에 reconcile해야 pod가 mount 실패하지 않는다.
	if err := r.reconcileConfigServerScriptsConfigMap(ctx, mdbsh); err != nil {
		return err
	}

	// Headless service
	if err := applyService(ctx, r.Client, r.Scheme, mdbsh, resources.BuildConfigServerService(mdbsh)); err != nil {
		return err
	}

	// StatefulSet
	preserve := resources.IsConfigServerHPAActive(mdbsh) || !resources.IsConfigServerScaleDeliberate(mdbsh)
	return applyStatefulSet(ctx, r.Client, r.Scheme, mdbsh, resources.BuildConfigServerStatefulSet(mdbsh), preserve)
}

// reconcileConfigServerScriptsConfigMap는 cfg StatefulSet이 lifecycle.postStart에서
// 호출하는 bootstrap-admin.sh + readiness 스크립트를 담은 ConfigMap을 reconcile한다.
// AdminCredentialsSecretRef가 비어있으면 cfg StatefulSet도 ConfigMap을 마운트하지
// 않으므로 이 단계 자체를 skip한다.
func (r *MongoDBShardedReconciler) reconcileConfigServerScriptsConfigMap(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name == "" {
		return nil
	}
	return applyConfigMap(ctx, r.Client, r.Scheme, mdbsh, resources.BuildConfigServerScriptsConfigMap(mdbsh))
}

func (r *MongoDBShardedReconciler) isConfigServerReady(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-cfg", Namespace: mdbsh.Namespace}, sts); err != nil {
		return false
	}
	return sts.Status.ReadyReplicas == mdbsh.Spec.ConfigServer.Members
}

func (r *MongoDBShardedReconciler) reconcileShard(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) error {
	// Scripts ConfigMap (admin bootstrap + readiness, port 27018)
	if err := r.reconcileShardScriptsConfigMap(ctx, mdbsh, shardIndex); err != nil {
		return err
	}

	// Headless service
	if err := applyService(ctx, r.Client, r.Scheme, mdbsh, resources.BuildShardService(mdbsh, shardIndex)); err != nil {
		return err
	}

	// StatefulSet
	preserve := !resources.IsShardScaleDeliberate(mdbsh)
	return applyStatefulSet(ctx, r.Client, r.Scheme, mdbsh, resources.BuildShardStatefulSet(mdbsh, shardIndex), preserve)
}

// reconcileShardScriptsConfigMap는 shard StatefulSet이 lifecycle.postStart에서 호출
// 하는 bootstrap-admin.sh + readiness 스크립트를 담은 ConfigMap을 reconcile한다.
func (r *MongoDBShardedReconciler) reconcileShardScriptsConfigMap(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) error {
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name == "" {
		return nil
	}
	return applyConfigMap(ctx, r.Client, r.Scheme, mdbsh, resources.BuildShardScriptsConfigMap(mdbsh, shardIndex))
}

func (r *MongoDBShardedReconciler) areShardsReady(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		sts := &appsv1.StatefulSet{}
		stsName := fmt.Sprintf("%s-shard-%d", mdbsh.Name, i)
		if err := r.Get(ctx, types.NamespacedName{Name: stsName, Namespace: mdbsh.Namespace}, sts); err != nil {
			return false
		}
		if sts.Status.ReadyReplicas != mdbsh.Spec.Shards.MembersPerShard {
			return false
		}
	}
	return true
}

func (r *MongoDBShardedReconciler) reconcileMongos(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	// ConfigMap
	if err := applyConfigMap(ctx, r.Client, r.Scheme, mdbsh, resources.BuildMongosConfigMap(mdbsh)); err != nil {
		return err
	}

	// Service
	if err := applyService(ctx, r.Client, r.Scheme, mdbsh, resources.BuildMongosService(mdbsh)); err != nil {
		return err
	}

	// Deployment
	return applyDeployment(ctx, r.Client, r.Scheme, mdbsh, resources.BuildMongosDeployment(mdbsh), resources.IsMongosHPAActive(mdbsh))
}

func (r *MongoDBShardedReconciler) isMongosReady(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-mongos", Namespace: mdbsh.Namespace}, deploy); err != nil {
		return false
	}
	return deploy.Status.ReadyReplicas >= 1
}

// newRSManager는 sharded cluster의 특정 RS(config server 또는 shard)에 대한
// driver 기반 ReplicaSetManager를 만든다. service name과 port가 RS마다 달라
// 호출자가 명시적으로 전달한다.
func (r *MongoDBShardedReconciler) newRSManager(serviceName string, port int, adminPassword string) *mongodb.ReplicaSetManager {
	return mongodb.NewReplicaSetManagerWithFactory(
		mongodb.NewPodConnectFactory(serviceName, port, "admin", adminPassword, "admin"),
	)
}

func (r *MongoDBShardedReconciler) reconcileConfigServerInit(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	logger := log.FromContext(ctx)
	logger.Info("Initializing config server replica set")

	adminPassword, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return fmt.Errorf("get admin password: %w", err)
	}
	// Config servers use port 27019
	rsManager := r.newRSManager(mdbsh.Name+"-cfg-headless", 27019, adminPassword)

	// Check if already initialized.
	// IsInitialized는 "아직 init 안됨" 정상 케이스를 (false, nil)로 반환하므로
	// 여기 도달한 err는 connect/auth/network 같은 진짜 결함이다. 이전엔 silent
	// nil로 삼켜 cfg server가 영구 미초기화되어도 status에 나오지 않았다.
	firstPod := fmt.Sprintf("%s-cfg-0", mdbsh.Name)
	initialized, err := rsManager.IsInitialized(ctx, firstPod, mdbsh.Namespace)
	if err != nil {
		return fmt.Errorf("check config server init: %w", err)
	}

	if initialized {
		logger.Info("Config server replica set already initialized")
		mdbsh.Status.ConfigServerInitialized = true
		return updateStatusWithRetry(ctx, r.Client, mdbsh)
	}

	// Build config server replica set configuration
	rsName := mdbsh.Name + "-cfg"
	serviceName := mdbsh.Name + "-cfg-headless"
	config := mongodb.BuildConfigServerReplicaSetConfig(
		rsName,
		mdbsh.Name+"-cfg",
		serviceName,
		mdbsh.Namespace,
		int(mdbsh.Spec.ConfigServer.Members),
		27019, // Config servers use port 27019
	)

	// Initialize
	if err := rsManager.Initiate(ctx, firstPod, mdbsh.Namespace, config); err != nil {
		return fmt.Errorf("failed to initiate config server replica set: %w", err)
	}

	logger.Info("Config server replica set initialized successfully")
	mdbsh.Status.ConfigServerInitialized = true
	return updateStatusWithRetry(ctx, r.Client, mdbsh)
}

func (r *MongoDBShardedReconciler) reconcileShardsInit(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	logger := log.FromContext(ctx)

	// Initialize or expand the ShardsInitialized slice if needed (preserve existing values on scale-up)
	if len(mdbsh.Status.ShardsInitialized) < int(mdbsh.Spec.Shards.Count) {
		newSlice := make([]bool, mdbsh.Spec.Shards.Count)
		copy(newSlice, mdbsh.Status.ShardsInitialized)
		mdbsh.Status.ShardsInitialized = newSlice
	}

	// shardErrs는 각 shard별 실패를 누적한다. 이전 구현은 continue로 에러를 삼키고
	// 루프 종료 후 Status().Update로 항상 nil error를 반환해 호출자에게 "성공"
	// 신호를 보냈음. 그 결과 운영자는 클러스터가 깨진 것을 알지 못했다.
	var shardErrs []error

	// 각 shard마다 service name이 다르므로 매 iteration에서 factory를 새로 만든다.
	// admin password는 한 번만 fetch.
	adminPassword, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return fmt.Errorf("get admin password: %w", err)
	}

	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if mdbsh.Status.ShardsInitialized[i] {
			continue
		}

		shardName := fmt.Sprintf("%s-shard-%d", mdbsh.Name, i)
		firstPod := fmt.Sprintf("%s-0", shardName)
		serviceName := shardName + "-headless"

		logger.Info("Initializing shard replica set", "shard", shardName)

		// Shards use port 27018
		rsManager := r.newRSManager(serviceName, 27018, adminPassword)

		// Check if already initialized
		initialized, err := rsManager.IsInitialized(ctx, firstPod, mdbsh.Namespace)
		if err != nil {
			logger.Error(err, "Failed to check shard initialization", "shard", shardName)
			shardErrs = append(shardErrs, fmt.Errorf("shard %s: check init: %w", shardName, err))
			continue
		}

		if initialized {
			logger.Info("Shard replica set already initialized", "shard", shardName)
			mdbsh.Status.ShardsInitialized[i] = true
			continue
		}

		// Build shard replica set configuration
		config := mongodb.BuildShardReplicaSetConfig(
			shardName,
			shardName,
			serviceName,
			mdbsh.Namespace,
			int(mdbsh.Spec.Shards.MembersPerShard),
			27018, // Shards use port 27018
		)

		// Initialize
		if err := rsManager.Initiate(ctx, firstPod, mdbsh.Namespace, config); err != nil {
			logger.Error(err, "Failed to initiate shard replica set", "shard", shardName)
			shardErrs = append(shardErrs, fmt.Errorf("shard %s: initiate: %w", shardName, err))
			continue
		}

		logger.Info("Shard replica set initialized successfully", "shard", shardName)
		mdbsh.Status.ShardsInitialized[i] = true
	}

	// 부분 진행 상태(Initialized 슬라이스)는 항상 status에 반영. status update 자체가
	// 실패해도 shard 실패가 우선이므로 errors.Join으로 묶어 호출자에게 전달.
	statusErr := updateStatusWithRetry(ctx, r.Client, mdbsh)
	if len(shardErrs) > 0 {
		return stderrors.Join(append(shardErrs, statusErr)...)
	}
	return statusErr
}

func (r *MongoDBShardedReconciler) reconcileShardedAdminUser(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating admin user via mongos")

	// Get admin password from secret
	adminPassword, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return fmt.Errorf("failed to get admin password: %w", err)
	}

	// Get mongos pod name
	mongosPod, err := r.getMongosPodName(ctx, mdbsh)
	if err != nil {
		return fmt.Errorf("failed to get mongos pod: %w", err)
	}

	// admin user는 mongos pod의 lifecycle.postStart bootstrap이 만든다.
	// operator는 driver 인증으로 verify만 한다.
	authManager := mongodb.NewAuthManagerWithFactory(
		mongodb.NewServiceConnectFactory(mdbsh.Name+"-mongos", mdbsh.Namespace, 27017, "admin", adminPassword, "admin"),
	)

	exists, err := authManager.UserExists(ctx, mongosPod, mdbsh.Namespace, "admin", "admin")
	if err != nil {
		return fmt.Errorf("verify admin user: %w", err)
	}
	if !exists {
		return fmt.Errorf("admin user not found — mongos pod의 postStart bootstrap이 실행되지 않았거나 실패함")
	}

	logger.Info("Admin user verified (created by mongos pod bootstrap)")
	mdbsh.Status.AdminUserCreated = true
	return updateStatusWithRetry(ctx, r.Client, mdbsh)
}

func (r *MongoDBShardedReconciler) reconcileAddShards(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	logger := log.FromContext(ctx)

	// Initialize or expand the ShardsAdded slice if needed (preserve existing values on scale-up)
	if len(mdbsh.Status.ShardsAdded) < int(mdbsh.Spec.Shards.Count) {
		newSlice := make([]bool, mdbsh.Spec.Shards.Count)
		copy(newSlice, mdbsh.Status.ShardsAdded)
		mdbsh.Status.ShardsAdded = newSlice
	}

	// All shards must be initialized first
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if !mdbsh.Status.ShardsInitialized[i] {
			return nil // Wait for all shards to be initialized
		}
	}

	// Get admin password for authentication
	adminPassword, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return fmt.Errorf("failed to get admin password: %w", err)
	}

	// Get mongos pod name
	mongosPod, err := r.getMongosPodName(ctx, mdbsh)
	if err != nil {
		return fmt.Errorf("failed to get mongos pod: %w", err)
	}

	// ShardManager는 mongos를 향한 driver factory로 만든다.
	shardManager := mongodb.NewShardManagerWithFactory(
		mongodb.NewServiceConnectFactory(mdbsh.Name+"-mongos", mdbsh.Namespace, 27017, "admin", adminPassword, "admin"),
	)

	// reconcileShardsInit과 동일한 사일런트 실패 패턴을 가지고 있어 동일 방식으로 수정.
	var addErrs []error
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if mdbsh.Status.ShardsAdded[i] {
			continue
		}

		shardName := fmt.Sprintf("%s-shard-%d", mdbsh.Name, i)
		serviceName := shardName + "-headless"

		logger.Info("Adding shard to cluster", "shard", shardName)

		// Build shard connection string (shards run on port 27018)
		shardConnString := mongodb.BuildShardConnectionString(
			shardName,
			shardName,
			serviceName,
			mdbsh.Namespace,
			int(mdbsh.Spec.Shards.MembersPerShard),
			27018, // Shards use port 27018
		)

		// Add shard via mongos with authentication (container "mongos", port 27017)
		if err := shardManager.AddShardWithAuthInContainer(ctx, mongosPod, mdbsh.Namespace, "mongos", "admin", adminPassword, shardConnString, 27017); err != nil {
			logger.Error(err, "Failed to add shard", "shard", shardName)
			addErrs = append(addErrs, fmt.Errorf("shard %s: add: %w", shardName, err))
			continue
		}

		logger.Info("Shard added successfully", "shard", shardName)
		mdbsh.Status.ShardsAdded[i] = true
	}

	statusErr := updateStatusWithRetry(ctx, r.Client, mdbsh)
	if len(addErrs) > 0 {
		return stderrors.Join(append(addErrs, statusErr)...)
	}
	return statusErr
}

func (r *MongoDBShardedReconciler) getMongosPodName(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (string, error) {
	// List mongos pods
	podList := &corev1.PodList{}
	labels := map[string]string{
		"app.kubernetes.io/instance":  mdbsh.Name,
		"app.kubernetes.io/component": "mongos",
	}

	if err := r.List(ctx, podList, client.InNamespace(mdbsh.Namespace), client.MatchingLabels(labels)); err != nil {
		return "", err
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return pod.Name, nil
		}
	}

	return "", fmt.Errorf("no running mongos pod found")
}

func (r *MongoDBShardedReconciler) getAdminPassword(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (string, error) {
	secret := &corev1.Secret{}
	secretName := mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: mdbsh.Namespace}, secret); err != nil {
		return "", fmt.Errorf("failed to get admin credentials secret: %w", err)
	}

	password, ok := secret.Data["password"]
	if !ok {
		return "", fmt.Errorf("password key not found in secret %s", secretName)
	}

	return string(password), nil
}

func (r *MongoDBShardedReconciler) updateStatus(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	// Update ConfigServer status
	cfgSts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-cfg", Namespace: mdbsh.Namespace}, cfgSts); err == nil {
		mdbsh.Status.ConfigServer = mongodbv1alpha1.ComponentStatus{
			Ready: cfgSts.Status.ReadyReplicas,
			Total: mdbsh.Spec.ConfigServer.Members,
			Phase: r.getComponentPhase(cfgSts.Status.ReadyReplicas, mdbsh.Spec.ConfigServer.Members),
		}
	}

	// Update Shards status
	mdbsh.Status.Shards = []mongodbv1alpha1.ShardStatus{}
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shardSts := &appsv1.StatefulSet{}
		stsName := fmt.Sprintf("%s-shard-%d", mdbsh.Name, i)
		if err := r.Get(ctx, types.NamespacedName{Name: stsName, Namespace: mdbsh.Namespace}, shardSts); err == nil {
			mdbsh.Status.Shards = append(mdbsh.Status.Shards, mongodbv1alpha1.ShardStatus{
				Name:  stsName,
				Ready: shardSts.Status.ReadyReplicas,
				Total: mdbsh.Spec.Shards.MembersPerShard,
				Phase: r.getComponentPhase(shardSts.Status.ReadyReplicas, mdbsh.Spec.Shards.MembersPerShard),
			})
		}
	}

	// Update Mongos status
	mongosDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-mongos", Namespace: mdbsh.Namespace}, mongosDeploy); err == nil {
		mdbsh.Status.Mongos = mongodbv1alpha1.ComponentStatus{
			Ready: mongosDeploy.Status.ReadyReplicas,
			Total: mdbsh.Spec.Mongos.Replicas,
			Phase: r.getComponentPhase(mongosDeploy.Status.ReadyReplicas, mdbsh.Spec.Mongos.Replicas),
		}
	}

	// Update overall phase
	if r.isClusterReady(mdbsh) {
		mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseRunning
	} else {
		mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseInitializing
	}

	// Set connection string
	mdbsh.Status.ConnectionString = fmt.Sprintf("mongodb://%s-mongos.%s.svc.cluster.local:27017",
		mdbsh.Name, mdbsh.Namespace)

	mdbsh.Status.ObservedGeneration = mdbsh.Generation

	return updateStatusWithRetry(ctx, r.Client, mdbsh)
}

func (r *MongoDBShardedReconciler) getComponentPhase(ready, total int32) string {
	if ready == total {
		return string(mongodbv1alpha1.ShardedPhaseRunning)
	}
	if ready > 0 {
		return string(mongodbv1alpha1.ShardedPhaseInitializing)
	}
	return string(mongodbv1alpha1.ShardedPhasePending)
}

func (r *MongoDBShardedReconciler) isClusterReady(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	// 모든 shard가 status에 보고됐는지 정합성 검사 — 부분 누락된 상태에서
	// 잘못 ready로 판정되는 silent bug를 차단.
	// gosec G115- int(len) → int32 변환은 슬라이스 길이 2^31 이상에서 오버플로우.
	// shard 수는 K8s 실용 범위(<10000)이므로 안전하지만 명시적 bounds check로 가드.
	shardLen := len(mdbsh.Status.Shards)
	if shardLen > math.MaxInt32 || int32(shardLen) != mdbsh.Spec.Shards.Count {
		return false
	}
	// Spec=0이면 잘못된 설정 — never ready.
	if mdbsh.Spec.ConfigServer.Members <= 0 ||
		mdbsh.Status.ConfigServer.Ready != mdbsh.Spec.ConfigServer.Members {
		return false
	}
	if mdbsh.Spec.Mongos.Replicas <= 0 ||
		mdbsh.Status.Mongos.Ready != mdbsh.Spec.Mongos.Replicas {
		return false
	}
	if mdbsh.Spec.Shards.MembersPerShard <= 0 {
		return false
	}
	for _, shard := range mdbsh.Status.Shards {
		if shard.Ready != mdbsh.Spec.Shards.MembersPerShard {
			return false
		}
	}
	return true
}

func (r *MongoDBShardedReconciler) updateStatusError(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, component string, err error) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(err, "Failed to reconcile component", "component", component)

	mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseFailed
	// 동일 type append 시 condition이 무한 누적되는 P2 버그 차단 — 항상 1건만.
	mdbsh.Status.Conditions = filterConditionsByType(mdbsh.Status.Conditions, "ReconcileError")
	mdbsh.Status.Conditions = append(mdbsh.Status.Conditions, metav1.Condition{
		Type:               "ReconcileError",
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "ReconcileFailed",
		Message:            fmt.Sprintf("Failed to reconcile %s: %v", component, err),
	})

	if statusErr := updateStatusWithRetry(ctx, r.Client, mdbsh); statusErr != nil {
		logger.Error(statusErr, "Failed to update status")
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, err
}

// reconcileScaleIn은 spec.Shards.Count < status.shardCount일 때 잉여 shard들을
// MongoDB의 removeShard 명령으로 drain하고 완료된 자원을 cleanup한다.
//
// 흐름:
//
//	spec.Count <= statusCount-1인 shard들에 대해
//	  1. mongos에 removeShard 명령 → state(started/ongoing/completed)
//	  2. state != completed → ShardDraining condition + return (다음 reconcile에서 polling)
//	  3. state == completed → STS/Service/scripts CM/PDB/NetworkPolicy cleanup
//
// 안전 가드: removeShard 명령은 chunks/dbs 마이그레이션이 *완전*히 끝난 후에만
// completed 상태가 된다. 즉 사용자 데이터 손실 위험은 MongoDB 자체가 차단.
// 단, PVC는 의도적으로 보존(StatefulSet의 PVCRetentionPolicy=Retain) — 운영자가
// 수동으로 삭제 결정.
// reconcileScaleIn returns ctrl.Result with RequeueAfter when a shard drain is
// in progress — caller(Reconcile)는 그 RequeueAfter를 사용해 polling 빈도를
// adaptive하게 조정한다(elapsed < 5m: 30s, 5-30m: 1m, >30m: 5m).
func (r *MongoDBShardedReconciler) reconcileScaleIn(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, error) {
	// gosec G115 bounds check — shard 수는 K8s 실용 범위(<10000)지만 명시 가드.
	shardLen := len(mdbsh.Status.Shards)
	if shardLen > math.MaxInt32 {
		return ctrl.Result{}, nil // 비현실적 — 무시
	}
	statusShardCount := int32(shardLen) //nolint:gosec // bounds checked above
	if mdbsh.Spec.Shards.Count >= statusShardCount {
		// scale-out 또는 stable. ShardDraining condition은 cleanup.
		mdbsh.Status.Conditions = filterConditionsByType(mdbsh.Status.Conditions, "ShardDraining")
		return ctrl.Result{}, nil
	}

	adminPassword, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get admin password: %w", err)
	}
	shardMgr := mongodb.NewShardManagerWithFactory(
		mongodb.NewServiceConnectFactory(mdbsh.Name+"-mongos", mdbsh.Namespace, 27017, "admin", adminPassword, "admin"),
	)
	mongosPod := mdbsh.Name + "-mongos-0"

	for i := mdbsh.Spec.Shards.Count; i < statusShardCount; i++ {
		shardName := fmt.Sprintf("%s-shard-%d", mdbsh.Name, i)
		result, err := shardMgr.RemoveShardWithStatus(ctx, mongosPod, mdbsh.Namespace, shardName)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("remove shard %s: %w", shardName, err)
		}
		if result.State != "completed" {
			r.recordShardDrainingCondition(mdbsh, shardName, result)
			return ctrl.Result{RequeueAfter: scaleInPollInterval(mdbsh, shardName)}, nil
		}
		if err := r.cleanupShardResources(ctx, mdbsh, i); err != nil {
			return ctrl.Result{}, fmt.Errorf("cleanup shard %s: %w", shardName, err)
		}
	}
	mdbsh.Status.Conditions = filterConditionsByType(mdbsh.Status.Conditions, "ShardDraining")
	return ctrl.Result{}, nil
}

// scaleInPollInterval은 ShardDraining condition의 LastTransitionTime을 보고
// elapsed 시간 기반으로 polling 간격을 결정. 짧은 drain은 빠른 응답, long-
// running drain(chunks 마이그레이션)은 mongos 부하를 낮추는 backoff.
func scaleInPollInterval(mdbsh *mongodbv1alpha1.MongoDBSharded, shardName string) time.Duration {
	for _, c := range mdbsh.Status.Conditions {
		if c.Type == "ShardDraining" && strings.Contains(c.Message, shardName) {
			elapsed := time.Since(c.LastTransitionTime.Time)
			switch {
			case elapsed < 5*time.Minute:
				return 30 * time.Second
			case elapsed < 30*time.Minute:
				return 1 * time.Minute
			default:
				return 5 * time.Minute
			}
		}
	}
	// 첫 호출(condition 미설정) — 다음 polling은 30s.
	return 30 * time.Second
}

// recordShardDrainingCondition은 removeShard 진행 상황을 ShardDraining condition
// 으로 status에 기록한다. 운영자가 kubectl describe로 진행 정도(remaining chunks,
// dbs)를 추적 가능.
func (r *MongoDBShardedReconciler) recordShardDrainingCondition(mdbsh *mongodbv1alpha1.MongoDBSharded, shardName string, result *mongodb.RemoveShardResult) {
	mdbsh.Status.Conditions = filterConditionsByType(mdbsh.Status.Conditions, "ShardDraining")
	mdbsh.Status.Conditions = append(mdbsh.Status.Conditions, metav1.Condition{
		Type:               "ShardDraining",
		Status:             metav1.ConditionTrue,
		Reason:             result.State,
		Message:            fmt.Sprintf("shard %s state=%s, remaining chunks=%d, dbs=%d", shardName, result.State, result.Remaining.Chunks, result.Remaining.Databases),
		ObservedGeneration: mdbsh.Generation,
		LastTransitionTime: metav1.Now(),
	})
}

// cleanupShardResources는 drain 완료된 shard의 STS/Service/scripts CM/PDB/
// NetworkPolicy를 삭제한다. PVC는 보존(데이터 손실 방지).
func (r *MongoDBShardedReconciler) cleanupShardResources(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) error {
	prefix := fmt.Sprintf("%s-shard-%d", mdbsh.Name, shardIndex)
	candidates := []client.Object{
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: mdbsh.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: prefix + "-headless", Namespace: mdbsh.Namespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: prefix + "-scripts", Namespace: mdbsh.Namespace}},
		&policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: prefix + "-pdb", Namespace: mdbsh.Namespace}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: prefix + "-netpol", Namespace: mdbsh.Namespace}},
	}
	for _, obj := range candidates {
		if err := r.Delete(ctx, obj); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete %T %s: %w", obj, obj.GetName(), err)
		}
	}
	return nil
}

// reconcileMongosHPA는 mongos HorizontalPodAutoscaler를 reconcile한다.
// `Spec.Mongos.AutoScaling.Enabled=false` (또는 nil)이면 기존 HPA를 삭제한다
// (spec/cluster 동기화). enabled=true이면 BuildMongosHPA로 desired state 생성 후
// controllerutil.CreateOrUpdate로 idempotent apply.
//
// 주의: HPA가 Replicas를 직접 관리하므로 mongos Deployment의 spec.replicas 필드는
// 운영자가 *직접 조정하지 않는다*(HPA controller가 patch). reconcileMongos에서
// HPA enabled 시 desired replicas를 무시하도록 별도 가드는 도입하지 않는다 —
// HPA controller가 매 reconcile cycle에서 자체 patch로 정렬하기 때문(<60s 안에
// 수렴).
func (r *MongoDBShardedReconciler) reconcileMongosHPA(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	if !r.EnableAutoscaling {
		return nil
	}
	desired := resources.BuildMongosHPA(mdbsh)
	if desired == nil {
		// disabled — 기존 HPA 정리
		existing := &autoscalingv2.HorizontalPodAutoscaler{}
		err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-mongos-hpa", Namespace: mdbsh.Namespace}, existing)
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}
	if err := controllerutil.SetControllerReference(mdbsh, desired, r.Scheme); err != nil {
		return fmt.Errorf("set HPA owner ref: %w", err)
	}
	existing := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		return controllerutil.SetControllerReference(mdbsh, existing, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("apply mongos HPA: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.FromContext(ctx).Info("Mongos HPA reconciled", "operation", op, "minReplicas", *desired.Spec.MinReplicas, "maxReplicas", desired.Spec.MaxReplicas)
	}
	return nil
}

// reconcileConfigServerHPA는 cfg StatefulSet의 HPA를 reconcile한다. mongos
// HPA와 동일 패턴이지만 BuildConfigServerHPA가 이중 가드(enabled+deliberate)를
// 강제하므로 별도 검사 없이 builder 결과 nil/non-nil로 분기.
func (r *MongoDBShardedReconciler) reconcileConfigServerHPA(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	if !r.EnableAutoscaling {
		return nil
	}
	desired := resources.BuildConfigServerHPA(mdbsh)
	if desired == nil {
		existing := &autoscalingv2.HorizontalPodAutoscaler{}
		err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-cfg-hpa", Namespace: mdbsh.Namespace}, existing)
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}
	if err := controllerutil.SetControllerReference(mdbsh, desired, r.Scheme); err != nil {
		return fmt.Errorf("set cfg HPA owner ref: %w", err)
	}
	existing := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		return controllerutil.SetControllerReference(mdbsh, existing, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("apply cfg HPA: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.FromContext(ctx).Info("ConfigServer HPA reconciled", "operation", op, "minReplicas", *desired.Spec.MinReplicas, "maxReplicas", desired.Spec.MaxReplicas)
	}
	return nil
}

// reconcileShardedPDBs는 cfg/shards/mongos 각 컴포넌트의 PodDisruptionBudget을
// reconcile한다. spec.podDisruptionBudget가 nil/disabled면 기존 PDB를 삭제한다
// (spec/cluster 동기화).
func (r *MongoDBShardedReconciler) reconcileShardedPDBs(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	desired := resources.BuildShardedPDBs(mdbsh)
	if desired == nil {
		return r.cleanupShardedPDBs(ctx, mdbsh)
	}
	for _, pdb := range desired {
		if err := applyPDB(ctx, r.Client, r.Scheme, mdbsh, pdb); err != nil {
			return fmt.Errorf("apply PDB %s: %w", pdb.Name, err)
		}
	}
	return nil
}

func (r *MongoDBShardedReconciler) cleanupShardedPDBs(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	names := []string{mdbsh.Name + "-cfg-pdb", mdbsh.Name + "-mongos-pdb"}
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		names = append(names, fmt.Sprintf("%s-shard-%d-pdb", mdbsh.Name, i))
	}
	for _, n := range names {
		existing := &policyv1.PodDisruptionBudget{}
		err := r.Get(ctx, types.NamespacedName{Name: n, Namespace: mdbsh.Namespace}, existing)
		if errors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// reconcileShardedNetworkPolicies는 cfg/shards/mongos 각 컴포넌트의 NetworkPolicy
// 를 reconcile한다. disabled면 cleanup.
func (r *MongoDBShardedReconciler) reconcileShardedNetworkPolicies(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	desired := resources.BuildShardedNetworkPolicies(mdbsh)
	if desired == nil {
		return r.cleanupShardedNetworkPolicies(ctx, mdbsh)
	}
	for _, np := range desired {
		if err := applyNetworkPolicy(ctx, r.Client, r.Scheme, mdbsh, np); err != nil {
			return fmt.Errorf("apply NetworkPolicy %s: %w", np.Name, err)
		}
	}
	return nil
}

func (r *MongoDBShardedReconciler) cleanupShardedNetworkPolicies(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	names := []string{mdbsh.Name + "-cfg-netpol", mdbsh.Name + "-mongos-netpol"}
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		names = append(names, fmt.Sprintf("%s-shard-%d-netpol", mdbsh.Name, i))
	}
	for _, n := range names {
		existing := &networkingv1.NetworkPolicy{}
		err := r.Get(ctx, types.NamespacedName{Name: n, Namespace: mdbsh.Namespace}, existing)
		if errors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MongoDBShardedReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBSharded{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Complete(r)
}
