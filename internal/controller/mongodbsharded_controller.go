/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
	"github.com/keiailab/mongodb-operator/internal/resources"
	commonsfinalizer "github.com/keiailab/operator-commons/pkg/finalizer"
)

const (
	// mongodbShardedFinalizer — api/v1alpha1.FinalizerMongoDBSharded local alias.
	mongodbShardedFinalizer = mongodbv1alpha1.FinalizerMongoDBSharded
)

// MongoDBShardedReconciler reconciles a MongoDBSharded object
type MongoDBShardedReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// EnableAutoscaling 게이트 — false면 reconcileMongosHPA / reconcileConfigServerHPA가
	// no-op로 종료. cmd/main.go의 --enable-autoscaling flag에서 주입.
	EnableAutoscaling bool
	// Recorder는 K8s Events 발행용. SetupWithManager에서 자동 주입.
	// nil-safe (helpers.go::applyErrorCondition에서 nil 체크).
	Recorder events.EventRecorder
}

// ensureShardedUpgradeStartTime은 업그레이드 시작 시각이 아직 없을 때만 현재
// 시각으로 설정한다(결함 #12). 이미 기록돼 있으면 보존 — conflict 후 refetch 나
// 재시도 시 서버의 기존 값을 덮어쓰지 않아 업그레이드 소요시간 계산이 안정적이다.
func ensureShardedUpgradeStartTime(mdbsh *mongodbv1alpha1.MongoDBSharded) {
	if mdbsh.Status.UpgradeStartTime == nil {
		now := metav1.Now()
		mdbsh.Status.UpgradeStartTime = &now
	}
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete

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
	if !commonsfinalizer.Has(mdbsh, mongodbShardedFinalizer) {
		commonsfinalizer.Add(mdbsh, mongodbShardedFinalizer)
		if err := r.Update(ctx, mdbsh); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Update status phase to Initializing if pending
	if mdbsh.Status.Phase == "" || mdbsh.Status.Phase == mongodbv1alpha1.ShardedPhasePending {
		setInitializing := func() {
			mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseInitializing
		}
		setInitializing()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, setInitializing); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile resources in order

	// 0.5. (Pillar P7 Phase 2) cert-manager Certificate CR upsert.
	// TLS.Enabled=true + IssuerRef 명시 시 server cert Secret 자동 발급 위임.
	// Phase 3 별 cycle 에서 STS volume mount + mongod.conf net.tls.mode=requireSSL 통합.
	if err := r.reconcileTLS(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "TLSCertificate", err)
	}

	// 1. Keyfile Secret
	if err := r.reconcileKeyfileSecret(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "KeyfileSecret", err)
	}

	// 1.5. Upgrade orchestration
	if result, handled, err := r.reconcileShardedUpgrade(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "Upgrade", err)
	} else if handled {
		return result, nil
	}

	// 2. Config Server
	if err := r.reconcileConfigServer(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "ConfigServer", err)
	}

	// 3. Wait for Config Server to be ready
	if !r.isConfigServerReady(ctx, mdbsh) {
		logger.Info("Waiting for config server to be ready")
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
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
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}

	// 6. Mongos
	if err := r.reconcileMongos(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "Mongos", err)
	}

	// 6.1. Transition to Validating after all components updated
	if mdbsh.Status.UpgradePhase == UpgradePhaseUpgrading {
		setValidating := func() {
			mdbsh.Status.UpgradePhase = UpgradePhaseValidating
			// 기존 시작 시각 보존 — 이미 기록돼 있으면 덮어쓰지 않는다(결함 #12).
			ensureShardedUpgradeStartTime(mdbsh)
		}
		setValidating()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, setValidating); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: parseValidationInterval(mdbsh.Spec.UpgradeStrategy)}, nil
	}

	// 6.5. PodDisruptionBudgets (opt-in, 모든 컴포넌트 cfg/shards/mongos)
	if err := r.reconcileShardedPDBs(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "PodDisruptionBudgets", err)
	}

	// 6.6. NetworkPolicies (opt-in, 컴포넌트별 deny-by-default)
	if err := r.reconcileShardedNetworkPolicies(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "NetworkPolicies", err)
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
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
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

	// 11.5. Mongos HPA (opt-in via Spec.Mongos.AutoScaling.Enabled — ADR-0007).
	// v1.4.1: ConfigServer/Shards rs.initiate + mongos ready + admin user + addShards
	// 모두 완료된 *후*에만 HPA 활성화. 이전(v1.3.0~v1.4.0)에는 단계 6.7/6.8에서
	// HPA를 먼저 만들어 cfg RS 미초기화 상태의 mongos crashloop pod를 HPA가
	// metric 으로 sample 해 잘못된 스케일링이 발생했다(P1).
	if err := r.reconcileMongosHPA(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "MongosHPA", err)
	}

	// 11.6. ConfigServer HPA (opt-in via Spec.ConfigServer.AutoScaling.Enabled +
	// ScalePolicy.Deliberate — ADR-0008/0009 이중 가드).
	if err := r.reconcileConfigServerHPA(ctx, mdbsh); err != nil {
		return r.updateStatusError(ctx, mdbsh, "ConfigServerHPA", err)
	}

	// 12. Update status
	if err := r.updateStatus(ctx, mdbsh); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled MongoDBSharded")
	return ctrl.Result{RequeueAfter: requeueSteady}, nil
}

func (r *MongoDBShardedReconciler) handleDeletion(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, error) {
	log.FromContext(ctx).Info("Handling MongoDBSharded deletion")
	// 모든 sub-resource는 OwnerReference로 GC. shard PVC는 retain 정책 그대로.
	return handleFinalizerCleanup(ctx, r.Client, mdbsh, mongodbShardedFinalizer, nil)
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
	sts := resources.BuildConfigServerStatefulSet(mdbsh)
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, resources.BuildPEMMergeInitContainer())
	}
	return applyStatefulSet(ctx, r.Client, r.Scheme, mdbsh, sts, preserve)
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
	sts := resources.BuildShardStatefulSet(mdbsh, shardIndex)
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, resources.BuildPEMMergeInitContainer())
	}
	return applyStatefulSet(ctx, r.Client, r.Scheme, mdbsh, sts, preserve)
}

// reconcileShardScriptsConfigMap는 shard StatefulSet이 lifecycle.postStart에서 호출
// 하는 bootstrap-admin.sh + readiness 스크립트를 담은 ConfigMap을 reconcile한다.
func (r *MongoDBShardedReconciler) reconcileShardScriptsConfigMap(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) error {
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name == "" {
		return nil
	}
	return applyConfigMap(ctx, r.Client, r.Scheme, mdbsh, resources.BuildShardScriptsConfigMap(mdbsh, shardIndex))
}

// areShardsInitialized는 모든 shard의 rs.initiate가 완료됐는지 검사한다.
// v1.4.1 HPA readiness gate에서 사용 — Status.ShardsInitialized 슬라이스의
// 길이가 spec.Shards.Count와 일치하고 모든 인덱스가 true여야 한다.
// reconcileShardsInit이 인덱스별로 true를 누적하므로 부분 초기화 상태를
// (false) 로 안전하게 보고한다.
func (r *MongoDBShardedReconciler) areShardsInitialized(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	// gosec G115 (int → int32 overflow) 회피 — int32 → int upcast 는 항상 안전.
	if len(mdbsh.Status.ShardsInitialized) < int(mdbsh.Spec.Shards.Count) {
		return false
	}
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if !mdbsh.Status.ShardsInitialized[i] {
			return false
		}
	}
	return true
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
	dep := resources.BuildMongosDeployment(mdbsh)
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		dep.Spec.Template.Spec.InitContainers = append(dep.Spec.Template.Spec.InitContainers, resources.BuildPEMMergeInitContainer())
	}
	return applyDeployment(ctx, r.Client, r.Scheme, mdbsh, dep, resources.IsMongosHPAActive(mdbsh))
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
		setConfigServerInitialized := func() {
			mdbsh.Status.ConfigServerInitialized = true
		}
		setConfigServerInitialized()
		return updateStatusWithRetry(ctx, r.Client, mdbsh, setConfigServerInitialized)
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
	markConfigServerInitialized := func() {
		mdbsh.Status.ConfigServerInitialized = true
	}
	markConfigServerInitialized()
	return updateStatusWithRetry(ctx, r.Client, mdbsh, markConfigServerInitialized)
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
	// conflict refetch 가 누적 슬라이스를 덮어쓰지 않도록 스냅샷을 재적용한다.
	initializedSnapshot := append([]bool(nil), mdbsh.Status.ShardsInitialized...)
	statusErr := updateStatusWithRetry(ctx, r.Client, mdbsh, func() {
		mdbsh.Status.ShardsInitialized = initializedSnapshot
	})
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
	setAdminUserCreated := func() {
		mdbsh.Status.AdminUserCreated = true
	}
	setAdminUserCreated()
	return updateStatusWithRetry(ctx, r.Client, mdbsh, setAdminUserCreated)
}

func (r *MongoDBShardedReconciler) reconcileAddShards(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	logger := log.FromContext(ctx)

	// Initialize or expand the ShardsAdded slice if needed (preserve existing values on scale-up)
	if len(mdbsh.Status.ShardsAdded) < int(mdbsh.Spec.Shards.Count) {
		newSlice := make([]bool, mdbsh.Spec.Shards.Count)
		copy(newSlice, mdbsh.Status.ShardsAdded)
		mdbsh.Status.ShardsAdded = newSlice
	}

	// All shards must be initialized first.
	// v1.4.3: 슬라이스 길이 가드 — Status.ShardsInitialized 가 empty/short 면
	// reconcileShardsInit 가 아직 첫 패스를 완료하지 않은 상태이므로 wait. 가드
	// 부재 시 CRD schema drop 등으로 슬라이스가 비어 있을 때 인덱스 panic 발생.
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if int(i) >= len(mdbsh.Status.ShardsInitialized) || !mdbsh.Status.ShardsInitialized[i] {
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

	// conflict refetch 가 누적 슬라이스를 덮어쓰지 않도록 스냅샷을 재적용한다.
	addedSnapshot := append([]bool(nil), mdbsh.Status.ShardsAdded...)
	statusErr := updateStatusWithRetry(ctx, r.Client, mdbsh, func() {
		mdbsh.Status.ShardsAdded = addedSnapshot
	})

	// Balancer active window (Phase 5.3) — Window 설정 시 config.settings 갱신
	// (idempotent upsert). 미설정 시 no-op (default off → 기존 동작 보존).
	if b := mdbsh.Spec.Balancer; b != nil && b.Window != nil {
		if err := shardManager.SetBalancerWindow(ctx, mongosPod, mdbsh.Namespace, b.Window.Start, b.Window.Stop); err != nil {
			logger.Error(err, "Failed to set balancer window")
			addErrs = append(addErrs, fmt.Errorf("balancer window: %w", err))
		} else {
			logger.Info("Balancer active window applied", "start", b.Window.Start, "stop", b.Window.Stop)
		}
	}

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
	// v1.4.1 status truth source 분기:
	// HPA active 컴포넌트는 .spec.replicas의 owner가 HPA controller로 넘어가므로
	// Total을 mdbsh.Spec.X.Replicas에서 읽으면 영구 divergence가 발생한다(P1).
	// HPA active 시 obj.Spec.Replicas (HPA가 patch한 desired), inactive 시
	// CR.Spec을 source-of-truth로 사용한다.

	// Update ConfigServer status
	cfgSts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-cfg", Namespace: mdbsh.Namespace}, cfgSts); err == nil {
		cfgTotal := mdbsh.Spec.ConfigServer.Members
		if resources.IsConfigServerHPAActive(mdbsh) && cfgSts.Spec.Replicas != nil {
			cfgTotal = *cfgSts.Spec.Replicas
		}
		mdbsh.Status.ConfigServer = mongodbv1alpha1.ComponentStatus{
			Ready: cfgSts.Status.ReadyReplicas,
			Total: cfgTotal,
			Phase: r.getComponentPhase(cfgSts.Status.ReadyReplicas, cfgTotal),
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("get cfg statefulset for status: %w", err)
	}

	// Update Shards status (shard는 HPA 미지원이므로 분기 불필요)
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
		} else if !errors.IsNotFound(err) {
			return fmt.Errorf("get shard %d statefulset for status: %w", i, err)
		}
	}

	// Update Mongos status
	mongosDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdbsh.Name + "-mongos", Namespace: mdbsh.Namespace}, mongosDeploy); err == nil {
		mongosTotal := mdbsh.Spec.Mongos.Replicas
		if resources.IsMongosHPAActive(mdbsh) && mongosDeploy.Spec.Replicas != nil {
			mongosTotal = *mongosDeploy.Spec.Replicas
		}
		mdbsh.Status.Mongos = mongodbv1alpha1.ComponentStatus{
			Ready: mongosDeploy.Status.ReadyReplicas,
			Total: mongosTotal,
			Phase: r.getComponentPhase(mongosDeploy.Status.ReadyReplicas, mongosTotal),
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("get mongos deployment for status: %w", err)
	}

	// Update overall phase
	ready := r.isClusterReady(mdbsh)
	if ready {
		mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseRunning
	} else {
		mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseInitializing
	}

	// Set connection string
	// status 계산을 클로저로 묶어 conflict refetch 후에도 재적용한다.
	applyStatus := func() {
		mdbsh.Status.ConnectionString = fmt.Sprintf("mongodb://%s-mongos.%s.svc.cluster.local:27017",
			mdbsh.Name, mdbsh.Namespace)

		mdbsh.Status.ObservedGeneration = mdbsh.Generation
		mdbsh.Status.Conditions = clearReconcileErrorCondition(mdbsh.Status.Conditions, mdbsh.Generation)

		// C37 (ADR-0013 helpers.go SetStatusCondition 활용): operational visibility
		// 격차 해소 — valkey/postgres 의 풍부한 conditions 패턴 차용.
		// evaluateShardedConditions pure function 추출로 isolated unit test 가능.
		for _, c := range evaluateShardedConditions(mdbsh, ready) {
			meta.SetStatusCondition(&mdbsh.Status.Conditions, c)
		}
	}
	applyStatus()
	return updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus)
}

// evaluateShardedConditions — pure function. *side effect 0*. updateStatus 가
// 이미 mdbsh.Status.{ConfigServer,Shards,Mongos} 갱신한 후 호출. C37 회귀 가드
// (envtest 미도달 영역, isolated unit test 범위).
func evaluateShardedConditions(mdbsh *mongodbv1alpha1.MongoDBSharded, ready bool) []metav1.Condition {
	const (
		reasonAvailable    = "Available"
		reasonInitializing = "Initializing"
	)
	conds := make([]metav1.Condition, 0, 8)

	readyStatus := metav1.ConditionFalse
	readyReason := reasonInitializing
	readyMessage := "MongoDBSharded cluster is initializing components"
	if ready {
		readyStatus = metav1.ConditionTrue
		readyReason = reasonAvailable
		readyMessage = "All components (config server, shards, mongos) ready"
	}
	conds = append(conds, metav1.Condition{
		Type:               "Ready",
		Status:             readyStatus,
		ObservedGeneration: mdbsh.Generation,
		Reason:             readyReason,
		Message:            readyMessage,
	})

	progressingStatus := metav1.ConditionFalse
	progressingReason := reasonAvailable
	progressingMessage := "Cluster reached desired state"
	if !ready {
		progressingStatus = metav1.ConditionTrue
		progressingReason = reasonInitializing
		progressingMessage = "Cluster components transitioning to ready state"
	}
	conds = append(conds, metav1.Condition{
		Type:               "Progressing",
		Status:             progressingStatus,
		ObservedGeneration: mdbsh.Generation,
		Reason:             progressingReason,
		Message:            progressingMessage,
	})

	// C37 2차 — component-level conditions.
	cfgReady := mdbsh.Status.ConfigServer.Ready == mdbsh.Status.ConfigServer.Total && mdbsh.Status.ConfigServer.Total > 0
	cfgStatus := metav1.ConditionFalse
	cfgReason := reasonInitializing
	if cfgReady {
		cfgStatus = metav1.ConditionTrue
		cfgReason = reasonAvailable
	}
	conds = append(conds, metav1.Condition{
		Type:               "ConfigServerReady",
		Status:             cfgStatus,
		ObservedGeneration: mdbsh.Generation,
		Reason:             cfgReason,
		Message: fmt.Sprintf("ConfigServer %d/%d ready",
			mdbsh.Status.ConfigServer.Ready, mdbsh.Status.ConfigServer.Total),
	})

	allShardsReady := len(mdbsh.Status.Shards) > 0
	for _, s := range mdbsh.Status.Shards {
		if s.Ready != s.Total || s.Total == 0 {
			allShardsReady = false
			break
		}
	}
	shardsStatus := metav1.ConditionFalse
	shardsReason := reasonInitializing
	if allShardsReady {
		shardsStatus = metav1.ConditionTrue
		shardsReason = reasonAvailable
	}
	conds = append(conds, metav1.Condition{
		Type:               "ShardsReady",
		Status:             shardsStatus,
		ObservedGeneration: mdbsh.Generation,
		Reason:             shardsReason,
		Message:            fmt.Sprintf("%d/%d shards reporting all members ready", len(mdbsh.Status.Shards), mdbsh.Spec.Shards.Count),
	})

	mongosReady := mdbsh.Status.Mongos.Ready == mdbsh.Status.Mongos.Total && mdbsh.Status.Mongos.Total > 0
	mongosStatus := metav1.ConditionFalse
	mongosReason := reasonInitializing
	if mongosReady {
		mongosStatus = metav1.ConditionTrue
		mongosReason = reasonAvailable
	}
	conds = append(conds, metav1.Condition{
		Type:               "MongosReady",
		Status:             mongosStatus,
		ObservedGeneration: mdbsh.Generation,
		Reason:             mongosReason,
		Message: fmt.Sprintf("Mongos %d/%d ready",
			mdbsh.Status.Mongos.Ready, mdbsh.Status.Mongos.Total),
	})

	// C37 3차 — 조건부 (TLSReady / BackupReady). spec 활성 시만.
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		conds = append(conds, metav1.Condition{
			Type:               "TLSReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: mdbsh.Generation,
			Reason:             "TLSEnabled",
			Message:            "TLS spec enabled (cert validation is operator-internal)",
		})
	}
	if mdbsh.Spec.Backup != nil && mdbsh.Spec.Backup.Enabled {
		conds = append(conds, metav1.Condition{
			Type:               "BackupReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: mdbsh.Generation,
			Reason:             "BackupEnabled",
			Message: fmt.Sprintf("Backup enabled (schedule: %q, type: %s)",
				mdbsh.Spec.Backup.Schedule, mdbsh.Spec.Backup.Storage.Type),
		})
	}

	return conds
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
	// HPA active 시 ready 판정 기준은 Spec.Replicas(절대값)가 아니라 *현재
	// Status.Total*(=HPA가 patch한 desired). HPA가 traffic 변동에 따라 scale-up/
	// down 한 직후에도 ready가 정확히 desired와 일치하면 cluster ready로 본다.
	// (이전 v1.4.0에서는 Spec.Replicas 고정 비교 → HPA scale-down 후 영구 Initializing)
	cfgTarget := mdbsh.Spec.ConfigServer.Members
	if resources.IsConfigServerHPAActive(mdbsh) && mdbsh.Status.ConfigServer.Total > 0 {
		cfgTarget = mdbsh.Status.ConfigServer.Total
	}
	if cfgTarget <= 0 || mdbsh.Status.ConfigServer.Ready != cfgTarget {
		return false
	}
	mongosTarget := mdbsh.Spec.Mongos.Replicas
	if resources.IsMongosHPAActive(mdbsh) && mdbsh.Status.Mongos.Total > 0 {
		mongosTarget = mdbsh.Status.Mongos.Total
	}
	if mongosTarget <= 0 || mdbsh.Status.Mongos.Ready != mongosTarget {
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
	return applyErrorCondition(ctx, r.Client, mdbsh, component, err, r.Recorder)
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
		meta.RemoveStatusCondition(&mdbsh.Status.Conditions, "ShardDraining")
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
	meta.RemoveStatusCondition(&mdbsh.Status.Conditions, "ShardDraining")
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
	// ADR-0013 정합: meta.SetStatusCondition 위임 — drain 시작 시 1회만
	// LastTransitionTime 설정. scaleInPollInterval 의 elapsed 백오프 (5min/30min
	// 분기) 가 정상 동작하려면 매 reconcile 갱신을 회피해야 한다 (regression fix).
	meta.SetStatusCondition(&mdbsh.Status.Conditions, metav1.Condition{
		Type:               "ShardDraining",
		Status:             metav1.ConditionTrue,
		Reason:             result.State,
		Message:            fmt.Sprintf("shard %s state=%s, remaining chunks=%d, dbs=%d", shardName, result.State, result.Remaining.Chunks, result.Remaining.Databases),
		ObservedGeneration: mdbsh.Generation,
	})
}

// cleanupShardResources는 drain 완료된 shard의 STS/Service/scripts CM/PDB/
// NetworkPolicy + 관련 PVC를 삭제하고 Status flag 를 리셋한다.
//
// v1.4.4 (2026-05-01) - PVC 보존 정책 supersession:
//
//	기존(~v1.4.3) 은 "PVC 보존 (데이터 손실 방지)" 였으나, scale-back-out 시
//	stale PVC 의 mongod 가 옛 RS config + 새 cluster 의 shard 미등록 상태로
//	ShardNotFound 영구 CrashLoopBackOff 재현. drain 이 이미 데이터를 살아있는
//	샤드로 마이그레이션 *완료*했으므로 (removeShard state=completed 보장), 본
//	shard 의 PVC 데이터는 **redundant copy** — 삭제해도 무손실. 대신 scale-
//	back-out 시 fresh PVC 가 프로비저닝되어 깨끗한 RS init + sh.addShard 등록.
//
//	Status.ShardsAdded[i] / ShardsInitialized[i] 도 false 로 리셋 — scale-back-
//	out 시 reconcileShardsInit / reconcileAddShards 가 본 인덱스를 fresh shard
//	로 재인식.
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

	// v1.4.4: PVC 정리. PVC label = component:"shard-<i>" + instance:<mdbsh.Name>
	// (builder 가 STS volumeClaimTemplates 에 부여한 라벨).
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList, client.InNamespace(mdbsh.Namespace), client.MatchingLabels{
		"app.kubernetes.io/component": fmt.Sprintf("shard-%d", shardIndex),
		"app.kubernetes.io/instance":  mdbsh.Name,
	}); err != nil {
		return fmt.Errorf("list PVCs for %s: %w", prefix, err)
	}
	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if err := r.Delete(ctx, pvc); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete PVC %s: %w", pvc.Name, err)
		}
	}

	// v1.4.4: Status flag 리셋 — scale-back-out 시 fresh shard 로 재인식.
	if int(shardIndex) < len(mdbsh.Status.ShardsAdded) {
		mdbsh.Status.ShardsAdded[shardIndex] = false
	}
	if int(shardIndex) < len(mdbsh.Status.ShardsInitialized) {
		mdbsh.Status.ShardsInitialized[shardIndex] = false
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
	// v1.4.1 readiness gate: ConfigServer + 모든 Shard의 rs.initiate가 완료된
	// 후에만 HPA를 활성화한다. Reconcile()의 단계 순서가 RS init → HPA로 보장
	// 되지만, 외부 호출/재진입/순서 리팩터링 회귀를 방어하기 위한 이중 가드.
	// gate fail 시 nil 반환 — 다음 reconcile에서 자연 재시도되며, 기존 HPA가
	// 있다면 *유지*된다(삭제하면 stable cluster의 replica를 흔든다).
	if !mdbsh.Status.ConfigServerInitialized || !r.areShardsInitialized(mdbsh) {
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
	// v1.4.1 readiness gate: cfg HPA는 cfg RS의 PRIMARY 선출이 완료된 후에만
	// 의미가 있다. ConfigServerInitialized=false면 HPA가 기존 STS에 부착되어
	// crashloop pod의 metric을 sample하므로 부정확한 스케일링 발생.
	if !mdbsh.Status.ConfigServerInitialized {
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
	if r.Recorder == nil {
		// events API 마이그레이션 완료 (RFC-0023 Phase 2, 2026-05-11).
		r.Recorder = mgr.GetEventRecorder("mongodbsharded-controller")
	}
	// v1.4.1 P1 fix: HPA를 Owns에 등록한다. 누락 시 controller-runtime 의 default
	// cached reader가 HPA informer를 lazy 생성 시도 → cache sync wait timeout
	// (default 2분) → r.Get(... HPA ...) 가 영구 hang. RS controller (mongodb_controller.go)
	// 는 v1.2.0 도입 시 추가됐으나 Sharded controller는 v1.2.0 (mongos HPA) +
	// v1.3.0 (cfg HPA) 시점에 누락. 24h soak 의 informer cache timeout 증상과 일치.
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBSharded{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Complete(r)
}
