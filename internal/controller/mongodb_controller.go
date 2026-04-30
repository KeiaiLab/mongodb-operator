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
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	mongodbFinalizer = "mongodb.keiailab.com/finalizer"
	requeueAfter     = 30 * time.Second
)

// MongoDBReconciler reconciles a MongoDB object
type MongoDBReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// EnableAutoscaling 게이트 — false면 reconcileRSHPA가 no-op로 종료.
	// cmd/main.go의 --enable-autoscaling flag에서 주입.
	EnableAutoscaling bool
	// Recorder는 K8s Events 발행용. SetupWithManager에서 자동 주입.
	// nil이어도 안전 (eventf 래퍼가 guard) — 단위 테스트에서 미주입 허용.
	Recorder record.EventRecorder
}

// eventf는 nil-safe wrapper — Recorder가 미주입된 단위 테스트 환경에서도 panic 없이 동작.
func (r *MongoDBReconciler) eventf(obj *mongodbv1alpha1.MongoDB, eventType, reason, fmtStr string, args ...interface{}) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(obj, eventType, reason, fmtStr, args...)
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *MongoDBReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling MongoDB", "namespace", req.Namespace, "name", req.Name)

	// Fetch MongoDB instance
	mdb := &mongodbv1alpha1.MongoDB{}
	if err := r.Get(ctx, req.NamespacedName, mdb); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("MongoDB resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get MongoDB")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !mdb.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, mdb)
	}

	// Add finalizer if needed
	if !controllerutil.ContainsFinalizer(mdb, mongodbFinalizer) {
		controllerutil.AddFinalizer(mdb, mongodbFinalizer)
		if err := r.Update(ctx, mdb); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Update status phase to Initializing if pending
	if mdb.Status.Phase == "" || mdb.Status.Phase == mongodbv1alpha1.PhasePending {
		mdb.Status.Phase = mongodbv1alpha1.PhaseInitializing
		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile resources in order

	// 1. Keyfile Secret
	if err := r.reconcileKeyfileSecret(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "KeyfileSecret", err)
	}

	// 2. ConfigMap
	if err := r.reconcileConfigMap(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "ConfigMap", err)
	}

	// 3. Headless Service
	if err := r.reconcileHeadlessService(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "HeadlessService", err)
	}

	// 4. Client Service
	if err := r.reconcileClientService(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "ClientService", err)
	}

	// 5. StatefulSet
	if err := r.reconcileStatefulSet(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "StatefulSet", err)
	}

	// 5.5. PodDisruptionBudget (opt-in, spec.podDisruptionBudget이 enabled일 때만 생성)
	if err := r.reconcilePDB(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "PodDisruptionBudget", err)
	}

	// 5.6. NetworkPolicy (opt-in, spec.networkPolicy가 enabled일 때만 생성)
	if err := r.reconcileNetworkPolicy(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "NetworkPolicy", err)
	}

	// 6. Wait for all pods to be ready
	allReady, err := r.areAllPodsReady(ctx, mdb)
	if err != nil {
		return r.updateStatusError(ctx, mdb, "PodReadiness", err)
	}
	if !allReady {
		logger.Info("Waiting for all pods to be ready")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// 7. Initialize replica set if not initialized
	if !mdb.Status.ReplicaSetInitialized {
		if err := r.reconcileReplicaSetInitialization(ctx, mdb); err != nil {
			return r.updateStatusError(ctx, mdb, "ReplicaSetInit", err)
		}
	}

	// 8. Wait for primary election.
	//
	// post-bootstrap(`AdminUserCreated=true`)은 인증 매니저로 정상 체크.
	//
	// pre-bootstrap(`AdminUserCreated=false`)에서는 익명 매니저로 검사하되
	// auth 거부(Unauthorized/AuthenticationFailed)는 *step 9 부트스트랩 진행*
	// 시그널로 해석한다 — RS가 이미 auth-on이라 익명으로 status를 못 읽는
	// 정상 케이스(부트스트랩 중단/외부 init/postStart hook 선행)이며,
	// `BootstrapAdminUser`가 driver의 server selection으로 primary 자동 라우팅을
	// 처리한다. 그 외 정상 미선출(election 진행 중)은 단순 requeue, connect/network
	// 실패는 PrimaryUnreachable condition으로 진단성 표면화 후 requeue.
	if mdb.Status.AdminUserCreated {
		hasPrimary, err := r.hasPrimary(ctx, mdb)
		if err != nil {
			logger.Info("Primary unreachable, will retry", "error", err)
			r.setPrimaryUnreachableCondition(ctx, mdb, err)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		if !hasPrimary {
			logger.Info("Waiting for primary election")
			r.clearPrimaryUnreachableCondition(ctx, mdb)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		r.clearPrimaryUnreachableCondition(ctx, mdb)
	} else {
		anonMgr := r.newAnonRSManager(mdb)
		firstPod := fmt.Sprintf("%s-0", mdb.Name)
		hasPrimary, err := anonMgr.HasPrimary(ctx, firstPod, mdb.Namespace)
		switch {
		case err != nil && mongodb.IsAuthRequiredErr(err):
			// auth-on RS — primary 알 수 없지만 부트스트랩 진행 가능.
			r.clearPrimaryUnreachableCondition(ctx, mdb)
		case err != nil:
			logger.Info("Primary unreachable (pre-bootstrap), will retry", "error", err)
			r.setPrimaryUnreachableCondition(ctx, mdb, err)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		case !hasPrimary:
			logger.Info("Waiting for primary election (pre-bootstrap)")
			r.clearPrimaryUnreachableCondition(ctx, mdb)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		default:
			r.clearPrimaryUnreachableCondition(ctx, mdb)
		}
	}

	// 9. Create admin user if not created
	if !mdb.Status.AdminUserCreated {
		if err := r.reconcileAdminUser(ctx, mdb); err != nil {
			// busy lease는 transient — phase=Failed로 전이시키지 않고 양보.
			if errors.Is(err, errBootstrapBusy) {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			return r.updateStatusError(ctx, mdb, "AdminUser", err)
		}
	}

	// 9.5. RS HPA (opt-in via Spec.AutoScaling.Enabled + ScalePolicy.Deliberate
	// 이중 가드 — ADR-0008). 가드 통과 시에만 HPA 생성.
	if err := r.reconcileRSHPA(ctx, mdb); err != nil {
		return r.updateStatusError(ctx, mdb, "RSHPA", err)
	}

	// 9.6. PendingScale 가드 — Spec.Members 변경이 deliberate=false 때문에 보류
	// 됐다면 status에 노출 + Event 발행.
	r.recordPendingScale(ctx, mdb)

	// 10. Update status
	if err := r.updateStatus(ctx, mdb); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled MongoDB")
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *MongoDBReconciler) handleDeletion(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling MongoDB deletion")

	if controllerutil.ContainsFinalizer(mdb, mongodbFinalizer) {
		// Perform cleanup logic here if needed

		// Remove finalizer
		controllerutil.RemoveFinalizer(mdb, mongodbFinalizer)
		if err := r.Update(ctx, mdb); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *MongoDBReconciler) reconcileKeyfileSecret(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	// Check if keyfile secret already exists - DO NOT regenerate if it exists
	// Keyfile must remain constant across all pods for replica set authentication
	existingSecret := &corev1.Secret{}
	secretName := mdb.Name + "-keyfile"
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: mdb.Namespace}, existingSecret)
	if err == nil {
		// Secret exists, do not update
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	// Secret doesn't exist, create it
	secret := resources.BuildKeyfileSecret(mdb)
	if err := controllerutil.SetControllerReference(mdb, secret, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, secret)
}

func (r *MongoDBReconciler) reconcileConfigMap(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	return applyConfigMap(ctx, r.Client, r.Scheme, mdb, resources.BuildMongoDBConfigMap(mdb))
}

func (r *MongoDBReconciler) reconcileHeadlessService(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	return applyService(ctx, r.Client, r.Scheme, mdb, resources.BuildHeadlessService(mdb))
}

func (r *MongoDBReconciler) reconcileClientService(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	return applyService(ctx, r.Client, r.Scheme, mdb, resources.BuildClientService(mdb))
}

func (r *MongoDBReconciler) reconcileStatefulSet(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	// HPA 활성 또는 ScalePolicy.Deliberate=false 시 STS replicas를 보존(ADR-0007/0008).
	preserve := resources.IsRSHPAActive(mdb) || !resources.IsRSScaleDeliberate(mdb)
	return applyStatefulSet(ctx, r.Client, r.Scheme, mdb, resources.BuildReplicaSetStatefulSet(mdb), preserve)
}

// reconcileNetworkPolicy는 spec.networkPolicy가 enabled일 때만 NetworkPolicy를 생성한다.
// disabled로 변경되면 기존 NetworkPolicy를 삭제(spec과 cluster 동기화).
func (r *MongoDBReconciler) reconcileNetworkPolicy(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	desired := resources.BuildMongoDBNetworkPolicy(mdb)
	if desired == nil {
		existing := &networkingv1.NetworkPolicy{}
		err := r.Get(ctx, types.NamespacedName{Name: mdb.Name + "-netpol", Namespace: mdb.Namespace}, existing)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return r.Delete(ctx, existing)
	}
	return applyNetworkPolicy(ctx, r.Client, r.Scheme, mdb, desired)
}

// reconcilePDB는 spec.podDisruptionBudget가 enabled일 때만 PDB를 만든다.
// 사용자가 enabled=false로 변경하거나 podDisruptionBudget 필드를 제거하면
// 기존 PDB를 삭제해 spec과 cluster 상태를 동기화한다.
func (r *MongoDBReconciler) reconcilePDB(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	desired := resources.BuildMongoDBPDB(mdb)
	if desired == nil {
		existing := &policyv1.PodDisruptionBudget{}
		err := r.Get(ctx, types.NamespacedName{Name: mdb.Name + "-pdb", Namespace: mdb.Namespace}, existing)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return r.Delete(ctx, existing)
	}
	return applyPDB(ctx, r.Client, r.Scheme, mdb, desired)
}

func (r *MongoDBReconciler) areAllPodsReady(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (bool, error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdb.Name, Namespace: mdb.Namespace}, sts); err != nil {
		return false, err
	}

	return sts.Status.ReadyReplicas == mdb.Spec.Members, nil
}

// newRSManager는 mongo-go-driver 기반 ReplicaSetManager를 만든다.
// admin password는 매 호출마다 Secret에서 fetch한다 (보안: in-memory 캐싱하지 않음).
//
// 인증 모드는 BootstrapAdminUser 호출 후, 즉 mdb.Status.AdminUserCreated=true
// 일 때만 사용 가능. 그 이전엔 newAnonRSManager()를 사용해야 함.
func (r *MongoDBReconciler) newRSManager(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (*mongodb.ReplicaSetManager, error) {
	pw, err := r.getAdminPassword(ctx, mdb)
	if err != nil {
		return nil, fmt.Errorf("get admin password: %w", err)
	}
	return mongodb.NewReplicaSetManagerWithFactory(
		mongodb.NewPodConnectFactory(mdb.Name+"-headless", 27017, "admin", pw, "admin"),
	), nil
}

// newAnonRSManager는 자격증명 없이 ReplicaSetManager를 만든다. RS init과
// admin user 생성 사이의 좁은 창에서 사용. 그 이전(mongod boot 직후)과
// 그 이후(admin user 생성 후) 모두 익명 호출은 거부됨.
func (r *MongoDBReconciler) newAnonRSManager(mdb *mongodbv1alpha1.MongoDB) *mongodb.ReplicaSetManager {
	return mongodb.NewReplicaSetManagerWithFactory(
		mongodb.NewPodConnectFactory(mdb.Name+"-headless", 27017, "", "", ""),
	)
}

func (r *MongoDBReconciler) reconcileReplicaSetInitialization(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	logger := log.FromContext(ctx)
	logger.Info("Initializing replica set")

	// pre-bootstrap 단계는 익명 connect. admin user는 RS init 후에 생성됨.
	rsManager := r.newAnonRSManager(mdb)

	// Check if already initialized by querying first pod.
	// 주의: IsInitialized는 "init이 안된" 정상 케이스를 (false, nil)로 반환한다
	// (notYetInitializedCode=94 분기). 따라서 여기서 에러는 connect/auth/network
	// 같은 진짜 결함이며, silently nil로 삼키면 안 됨. 호출자(Reconcile)가
	// updateStatusError로 status에 노출하도록 propagate.
	firstPod := fmt.Sprintf("%s-0", mdb.Name)
	initialized, err := rsManager.IsInitialized(ctx, firstPod, mdb.Namespace)
	if err != nil {
		return fmt.Errorf("check replica set init: %w", err)
	}

	if initialized {
		logger.Info("Replica set already initialized")
		mdb.Status.ReplicaSetInitialized = true
		return updateStatusWithRetry(ctx, r.Client, mdb)
	}

	// Build replica set configuration
	serviceName := mdb.Name + "-headless"
	config := mongodb.BuildReplicaSetConfig(
		mdb.Spec.ReplicaSetName,
		mdb.Name,
		serviceName,
		mdb.Namespace,
		int(mdb.Spec.Members),
		27017,
	)

	// Initialize replica set
	if err := rsManager.Initiate(ctx, firstPod, mdb.Namespace, config); err != nil {
		return fmt.Errorf("failed to initiate replica set: %w", err)
	}

	logger.Info("Replica set initialized successfully")
	mdb.Status.ReplicaSetInitialized = true
	return updateStatusWithRetry(ctx, r.Client, mdb)
}

func (r *MongoDBReconciler) hasPrimary(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (bool, error) {
	// admin user 생성 전이면 익명, 후면 인증된 매니저 사용.
	var rsManager *mongodb.ReplicaSetManager
	if mdb.Status.AdminUserCreated {
		var err error
		rsManager, err = r.newRSManager(ctx, mdb)
		if err != nil {
			return false, err
		}
	} else {
		rsManager = r.newAnonRSManager(mdb)
	}
	firstPod := fmt.Sprintf("%s-0", mdb.Name)
	return rsManager.HasPrimary(ctx, firstPod, mdb.Namespace)
}

func (r *MongoDBReconciler) reconcileAdminUser(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	logger := log.FromContext(ctx)

	// 분산 락(K8s Lease): 동일 CR에 대한 동시 reconcile race 차단. 다른 holder
	// 가 lease를 점유 중이면 errBootstrapBusy로 양보 — 호출자가 짧은 requeue.
	lease, ok, err := r.acquireBootstrapLease(ctx, mdb)
	if err != nil {
		return fmt.Errorf("acquire bootstrap lease: %w", err)
	}
	if !ok {
		logger.Info("Bootstrap lease busy, will retry next reconcile")
		return errBootstrapBusy
	}
	defer r.releaseBootstrapLease(ctx, lease)

	logger.Info("Bootstrapping admin user", "lease", lease.Name)

	adminPassword, err := r.getAdminPassword(ctx, mdb)
	if err != nil {
		return fmt.Errorf("failed to get admin password: %w", err)
	}

	// RS init 직후엔 mongod이 --auth+--replSet으로 떠 있고 user는 0명. 익명
	// 접근은 첫 user 생성에 한해 허용된다(localhost exception). createUser
	// 후엔 모든 connect가 SCRAM 인증 필요.
	firstHost := mongodb.GetPodFQDN(fmt.Sprintf("%s-0", mdb.Name), mdb.Name+"-headless", mdb.Namespace, 27017)
	if err := mongodb.BootstrapAdminUser(ctx, firstHost, "admin", adminPassword); err != nil {
		return fmt.Errorf("bootstrap admin user: %w", err)
	}

	// bootstrap 직후 인증된 매니저로 user 존재를 즉시 ping. 통과 시에만
	// AdminUserCreated=true로 영속화 — race 또는 partial-failure가 있으면
	// 다음 reconcile에서 재시도하도록 false 유지.
	if err := r.verifyAdminUser(ctx, mdb, adminPassword); err != nil {
		return fmt.Errorf("verify admin user post-bootstrap: %w", err)
	}

	logger.Info("Admin user bootstrap complete and verified")
	mdb.Status.AdminUserCreated = true
	return updateStatusWithRetry(ctx, r.Client, mdb)
}

// verifyAdminUser는 bootstrap 직후 인증된 매니저로 admin user 존재를 ping한다.
//
// secondary oplog 지연으로 인한 false negative 차단 — pod-0이 PRIMARY가 아니
// 면 createUser oplog 이벤트가 secondary에 도달할 때까지 짧은 retry. 5초
// 안에 oplog가 catch up하지 못하면(매우 비정상) error 전파해 다음 reconcile
// 에서 재시도. 정상 RS에서는 ms 단위로 동기화되므로 첫 시도에 통과.
func (r *MongoDBReconciler) verifyAdminUser(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, password string) error {
	factory := mongodb.NewPodConnectFactory(mdb.Name+"-headless", 27017, "admin", password, "admin")
	auth := mongodb.NewAuthManagerWithFactory(factory)
	firstPod := fmt.Sprintf("%s-0", mdb.Name)

	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		exists, err := auth.UserExists(ctx, firstPod, mdb.Namespace, "admin", "admin")
		if err != nil {
			lastErr = fmt.Errorf("usersInfo: %w", err)
		} else if exists {
			return nil
		} else {
			lastErr = fmt.Errorf("admin user not visible (oplog sync pending?)")
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("verify admin user timeout(5s): %w", lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

func (r *MongoDBReconciler) getAdminPassword(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (string, error) {
	secret := &corev1.Secret{}
	secretName := mdb.Spec.Auth.AdminCredentialsSecretRef.Name
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: mdb.Namespace}, secret); err != nil {
		return "", fmt.Errorf("failed to get admin credentials secret: %w", err)
	}

	password, ok := secret.Data["password"]
	if !ok {
		return "", fmt.Errorf("password key not found in secret %s", secretName)
	}

	return string(password), nil
}

func (r *MongoDBReconciler) updateStatus(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	// Get StatefulSet status
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdb.Name, Namespace: mdb.Namespace}, sts); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		mdb.Status.ReadyMembers = 0
	} else {
		mdb.Status.ReadyMembers = sts.Status.ReadyReplicas
	}

	// Update phase based on ready members and initialization status
	if mdb.Status.ReadyMembers == mdb.Spec.Members && mdb.Status.ReplicaSetInitialized && mdb.Status.AdminUserCreated {
		mdb.Status.Phase = mongodbv1alpha1.PhaseRunning
	} else if mdb.Status.ReadyMembers > 0 {
		mdb.Status.Phase = mongodbv1alpha1.PhaseInitializing
	}

	// Get current primary if replica set is initialized.
	// 인증된 매니저 생성/조회 실패는 PrimaryUnreachable=True condition으로 영속화.
	// 이전 silent skip은 phase=Running인 채로 primary 추적이 멈춘 상태를 운영자가
	// 인지할 수 없게 만들어 silent failure였다.
	if mdb.Status.ReplicaSetInitialized && mdb.Status.AdminUserCreated {
		rsManager, err := r.newRSManager(ctx, mdb)
		if err != nil {
			r.recordPrimaryUnreachable(mdb, "ManagerCreateFailed", err)
		} else {
			firstPod := fmt.Sprintf("%s-0", mdb.Name)
			primaryPod, lookupErr := rsManager.GetPrimaryPod(ctx, firstPod, mdb.Namespace)
			if lookupErr != nil {
				r.recordPrimaryUnreachable(mdb, "PrimaryLookupFailed", lookupErr)
			} else {
				mdb.Status.CurrentPrimary = primaryPod
			}
		}
	}

	// Set connection string
	mdb.Status.ConnectionString = fmt.Sprintf("mongodb://%s-headless.%s.svc.cluster.local:27017/?replicaSet=%s",
		mdb.Name, mdb.Namespace, mdb.Spec.ReplicaSetName)

	mdb.Status.Version = mdb.Spec.Version.Version
	mdb.Status.ObservedGeneration = mdb.Generation

	// Update conditions
	mdb.Status.Conditions = r.buildConditions(mdb)

	return updateStatusWithRetry(ctx, r.Client, mdb)
}

func (r *MongoDBReconciler) buildConditions(mdb *mongodbv1alpha1.MongoDB) []metav1.Condition {
	// 본 함수는 Ready / ReplicaSetInitialized / AuthenticationReady 3개 type만
	// 관리한다. 다른 type(PrimaryUnreachable, ReconcileError 등)은 외부에서
	// set한 상태를 그대로 보존해 silent로 사라지지 않게 한다.
	managedTypes := map[string]bool{
		"Ready":                 true,
		"ReplicaSetInitialized": true,
		"AuthenticationReady":   true,
	}
	conditions := []metav1.Condition{}
	for _, c := range mdb.Status.Conditions {
		if !managedTypes[c.Type] {
			conditions = append(conditions, c)
		}
	}

	// Ready condition
	readyStatus := metav1.ConditionFalse
	readyReason := "NotReady"
	readyMessage := fmt.Sprintf("%d/%d members ready", mdb.Status.ReadyMembers, mdb.Spec.Members)

	if mdb.Status.ReadyMembers == mdb.Spec.Members && mdb.Status.ReplicaSetInitialized && mdb.Status.AdminUserCreated {
		readyStatus = metav1.ConditionTrue
		readyReason = "Ready"
		readyMessage = "All members are ready and cluster is fully initialized"
	}

	conditions = append(conditions, metav1.Condition{
		Type:               "Ready",
		Status:             readyStatus,
		ObservedGeneration: mdb.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             readyReason,
		Message:            readyMessage,
	})

	// ReplicaSetInitialized condition
	rsInitStatus := metav1.ConditionFalse
	rsInitReason := "NotInitialized"
	rsInitMessage := "Replica set has not been initialized"
	if mdb.Status.ReplicaSetInitialized {
		rsInitStatus = metav1.ConditionTrue
		rsInitReason = "Initialized"
		rsInitMessage = "Replica set has been initialized"
	}

	conditions = append(conditions, metav1.Condition{
		Type:               "ReplicaSetInitialized",
		Status:             rsInitStatus,
		ObservedGeneration: mdb.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             rsInitReason,
		Message:            rsInitMessage,
	})

	// AuthenticationReady condition
	authStatus := metav1.ConditionFalse
	authReason := "NotConfigured"
	authMessage := "Admin user has not been created"
	if mdb.Status.AdminUserCreated {
		authStatus = metav1.ConditionTrue
		authReason = "Configured"
		authMessage = "Admin user has been created"
	}

	conditions = append(conditions, metav1.Condition{
		Type:               "AuthenticationReady",
		Status:             authStatus,
		ObservedGeneration: mdb.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             authReason,
		Message:            authMessage,
	})

	return conditions
}

// setPrimaryUnreachableCondition은 hasPrimary가 connect/network 에러로 실패했을 때
// status에 PrimaryUnreachable=True condition을 기록한다. message에는 err의 첫
// 줄만 노출(secret/긴 stack 누출 방지).
func (r *MongoDBReconciler) setPrimaryUnreachableCondition(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, err error) {
	logger := log.FromContext(ctx)
	msg := firstLine(err.Error())
	// 동일 type의 기존 condition은 제거하고 최신만 유지.
	mdb.Status.Conditions = filterConditionsByType(mdb.Status.Conditions, "PrimaryUnreachable")
	mdb.Status.Conditions = append(mdb.Status.Conditions, metav1.Condition{
		Type:               "PrimaryUnreachable",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: mdb.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "ConnectError",
		Message:            fmt.Sprintf("hasPrimary check failed: %s", msg),
	})
	if statusErr := updateStatusWithRetry(ctx, r.Client, mdb); statusErr != nil {
		logger.Error(statusErr, "Failed to update PrimaryUnreachable condition")
	}
}

// clearPrimaryUnreachableCondition은 connect 에러가 해소되었을 때(hasPrimary가
// 에러 없이 응답) status condition을 False로 갱신한다. 한 번도 set되지 않은
// 경우(슬라이스에 type이 아예 없으면) 굳이 추가하지 않아 noise를 줄인다.
func (r *MongoDBReconciler) clearPrimaryUnreachableCondition(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) {
	logger := log.FromContext(ctx)
	hasIt := false
	for _, c := range mdb.Status.Conditions {
		if c.Type == "PrimaryUnreachable" && c.Status == metav1.ConditionTrue {
			hasIt = true
			break
		}
	}
	if !hasIt {
		return
	}
	mdb.Status.Conditions = filterConditionsByType(mdb.Status.Conditions, "PrimaryUnreachable")
	mdb.Status.Conditions = append(mdb.Status.Conditions, metav1.Condition{
		Type:               "PrimaryUnreachable",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: mdb.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Reachable",
		Message:            "Primary check succeeded",
	})
	if statusErr := updateStatusWithRetry(ctx, r.Client, mdb); statusErr != nil {
		logger.Error(statusErr, "Failed to clear PrimaryUnreachable condition")
	}
}

// recordPrimaryUnreachable은 updateStatus 단계에서 primary probe 실패를
// PrimaryUnreachable=True condition으로 status에 기록한다(메모리 mutate만 — 호출
// 자가 buildConditions 후 updateStatusWithRetry로 영속화). reason은 호출자가
// 단계(ManagerCreateFailed / PrimaryLookupFailed)를 명시.
func (r *MongoDBReconciler) recordPrimaryUnreachable(mdb *mongodbv1alpha1.MongoDB, reason string, err error) {
	mdb.Status.Conditions = filterConditionsByType(mdb.Status.Conditions, "PrimaryUnreachable")
	mdb.Status.Conditions = append(mdb.Status.Conditions, metav1.Condition{
		Type:               "PrimaryUnreachable",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: mdb.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            firstLine(err.Error()),
	})
}

// firstLine은 multiline 에러 message에서 첫 줄만 잘라 반환한다.
func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

// filterConditionsByType은 주어진 type의 condition을 슬라이스에서 제거한다.
func filterConditionsByType(conds []metav1.Condition, t string) []metav1.Condition {
	out := conds[:0]
	for _, c := range conds {
		if c.Type != t {
			out = append(out, c)
		}
	}
	return out
}

func (r *MongoDBReconciler) updateStatusError(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, component string, err error) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(err, "Failed to reconcile component", "component", component)
	r.eventf(mdb, corev1.EventTypeWarning, "ReconcileError", "Failed to reconcile %s: %v", component, err)

	mdb.Status.Phase = mongodbv1alpha1.PhaseFailed
	// 동일 type append 시 condition이 무한 누적되는 P2 버그 차단 — 항상 1건만.
	mdb.Status.Conditions = filterConditionsByType(mdb.Status.Conditions, "ReconcileError")
	mdb.Status.Conditions = append(mdb.Status.Conditions, metav1.Condition{
		Type:               "ReconcileError",
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "ReconcileFailed",
		Message:            fmt.Sprintf("Failed to reconcile %s: %v", component, err),
	})

	if statusErr := updateStatusWithRetry(ctx, r.Client, mdb); statusErr != nil {
		logger.Error(statusErr, "Failed to update status")
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *MongoDBReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("mongodb-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDB{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Complete(r)
}

// reconcileRSHPA는 RS HPA를 reconcile한다(ADR-0008 이중 가드 통과 시에만 활성).
// builder의 BuildReplicaSetHPA가 가드 체크를 내장하므로 호출자는 nil/non-nil로
// 분기. nil이면 기존 HPA cleanup.
func (r *MongoDBReconciler) reconcileRSHPA(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	if !r.EnableAutoscaling {
		return nil
	}
	desired := resources.BuildReplicaSetHPA(mdb)
	if desired == nil {
		existing := &autoscalingv2.HorizontalPodAutoscaler{}
		err := r.Get(ctx, types.NamespacedName{Name: mdb.Name + "-hpa", Namespace: mdb.Namespace}, existing)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	if err := controllerutil.SetControllerReference(mdb, desired, r.Scheme); err != nil {
		return fmt.Errorf("set RS HPA owner ref: %w", err)
	}
	existing := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		return controllerutil.SetControllerReference(mdb, existing, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("apply RS HPA: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.FromContext(ctx).Info("RS HPA reconciled", "operation", op, "minReplicas", *desired.Spec.MinReplicas, "maxReplicas", desired.Spec.MaxReplicas)
	}
	return nil
}

// recordPendingScale는 spec.Members가 STS의 현재 replicas와 다르고 ScalePolicy.
// Deliberate=false인 경우 Status.PendingScale에 기록한다(ADR-0008). deliberate=true
// 이거나 변경 없으면 PendingScale을 nil로 정리.
func (r *MongoDBReconciler) recordPendingScale(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) {
	if resources.IsRSScaleDeliberate(mdb) {
		mdb.Status.PendingScale = nil
		return
	}
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdb.Name, Namespace: mdb.Namespace}, sts); err != nil || sts.Spec.Replicas == nil {
		return
	}
	current := *sts.Spec.Replicas
	desired := mdb.Spec.Members
	if current == desired {
		mdb.Status.PendingScale = nil
		return
	}
	if mdb.Status.PendingScale != nil &&
		mdb.Status.PendingScale.CurrentMembers == current &&
		mdb.Status.PendingScale.DesiredMembers == desired {
		return // 이미 기록됨, 시각 갱신 불필요
	}
	mdb.Status.PendingScale = &mongodbv1alpha1.PendingScale{
		CurrentMembers: current,
		DesiredMembers: desired,
		RequestedAt:    metav1.Now().UTC().Format(time.RFC3339),
		Reason:         "ScalePolicy.Deliberate=false — set spec.scalePolicy.deliberate=true to apply",
	}
	log.FromContext(ctx).Info("Pending scale recorded (deliberate=false guard)", "current", current, "desired", desired)
}
