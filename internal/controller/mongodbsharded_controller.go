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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	// Check if keyfile secret already exists - DO NOT regenerate if it exists
	// Keyfile must remain constant across all pods for replica set authentication
	existingSecret := &corev1.Secret{}
	secretName := mdbsh.Name + "-keyfile"
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: mdbsh.Namespace}, existingSecret)
	if err == nil {
		// Secret exists, do not update
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}

	// Secret doesn't exist, create it
	secret := resources.BuildShardedKeyfileSecret(mdbsh)
	if err := controllerutil.SetControllerReference(mdbsh, secret, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, secret)
}

func (r *MongoDBShardedReconciler) reconcileConfigServer(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	// Headless service
	svc := resources.BuildConfigServerService(mdbsh)
	if err := r.createOrUpdate(ctx, mdbsh, svc); err != nil {
		return err
	}

	// StatefulSet
	sts := resources.BuildConfigServerStatefulSet(mdbsh)
	return r.createOrUpdate(ctx, mdbsh, sts)
}

func (r *MongoDBShardedReconciler) isConfigServerReady(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-cfg", Namespace: mdbsh.Namespace}, sts); err != nil {
		return false
	}
	return sts.Status.ReadyReplicas == mdbsh.Spec.ConfigServer.Members
}

func (r *MongoDBShardedReconciler) reconcileShard(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) error {
	// Headless service
	svc := resources.BuildShardService(mdbsh, shardIndex)
	if err := r.createOrUpdate(ctx, mdbsh, svc); err != nil {
		return err
	}

	// StatefulSet
	sts := resources.BuildShardStatefulSet(mdbsh, shardIndex)
	return r.createOrUpdate(ctx, mdbsh, sts)
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
	cm := resources.BuildMongosConfigMap(mdbsh)
	if err := r.createOrUpdate(ctx, mdbsh, cm); err != nil {
		return err
	}

	// Service
	svc := resources.BuildMongosService(mdbsh)
	if err := r.createOrUpdate(ctx, mdbsh, svc); err != nil {
		return err
	}

	// Deployment
	deploy := resources.BuildMongosDeployment(mdbsh)
	return r.createOrUpdate(ctx, mdbsh, deploy)
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
		mongodb.NewPodConnectFactory(mdbsh.Name+"-mongos", 27017, "admin", adminPassword, "admin"),
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
		mongodb.NewPodConnectFactory(mdbsh.Name+"-mongos", 27017, "admin", adminPassword, "admin"),
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

func (r *MongoDBShardedReconciler) createOrUpdate(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, obj client.Object) error {
	// Set owner reference
	if err := controllerutil.SetControllerReference(mdbsh, obj, r.Scheme); err != nil {
		return err
	}

	// Check if object exists
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existing)

	if err != nil {
		if errors.IsNotFound(err) {
			// Create the object
			return r.Create(ctx, obj)
		}
		return err
	}

	// Update the object
	obj.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, obj)
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
	if mdbsh.Status.ConfigServer.Ready != mdbsh.Spec.ConfigServer.Members {
		return false
	}
	if mdbsh.Status.Mongos.Ready != mdbsh.Spec.Mongos.Replicas {
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

// SetupWithManager sets up the controller with the Manager.
func (r *MongoDBShardedReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBSharded{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
