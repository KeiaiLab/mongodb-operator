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
	mongodbFinalizer = "mongodb.keiailab.com/finalizer"
	requeueAfter     = 30 * time.Second
)

// MongoDBReconciler reconciles a MongoDB object
type MongoDBReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete

func (r *MongoDBReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling MongoDB", "namespace", req.Namespace, "name", req.Name)

	// Fetch MongoDB instance
	mdb := &mongodbv1alpha1.MongoDB{}
	if err := r.Get(ctx, req.NamespacedName, mdb); err != nil {
		if errors.IsNotFound(err) {
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
	// hasPrimary err가 nil이면서 false인 경우(정상 미선출): 단순 대기 후 requeue.
	// err non-nil(connect/auth/network 실패): silent로 두면 운영자가 진단 불가하므로
	// PrimaryUnreachable condition을 status에 기록하고 requeue.
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

	// 9. Create admin user if not created
	if !mdb.Status.AdminUserCreated {
		if err := r.reconcileAdminUser(ctx, mdb); err != nil {
			return r.updateStatusError(ctx, mdb, "AdminUser", err)
		}
	}

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
	if !errors.IsNotFound(err) {
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
	cm := resources.BuildMongoDBConfigMap(mdb)
	return r.createOrUpdate(ctx, mdb, cm)
}

func (r *MongoDBReconciler) reconcileHeadlessService(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	svc := resources.BuildHeadlessService(mdb)
	return r.createOrUpdate(ctx, mdb, svc)
}

func (r *MongoDBReconciler) reconcileClientService(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	svc := resources.BuildClientService(mdb)
	return r.createOrUpdate(ctx, mdb, svc)
}

func (r *MongoDBReconciler) reconcileStatefulSet(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	sts := resources.BuildReplicaSetStatefulSet(mdb)
	return r.createOrUpdate(ctx, mdb, sts)
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
// 주의: 이 함수는 admin user가 mongodb pod의 lifecycle.postStart에서 이미
// 만들어졌다고 가정한다. pod ready ≠ bootstrap done 이므로 호출 직후 인증
// 실패할 수 있고, 호출자는 reconcile requeue로 대응한다.
func (r *MongoDBReconciler) newRSManager(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (*mongodb.ReplicaSetManager, error) {
	pw, err := r.getAdminPassword(ctx, mdb)
	if err != nil {
		return nil, fmt.Errorf("get admin password: %w", err)
	}
	return mongodb.NewReplicaSetManagerWithFactory(
		mongodb.NewPodConnectFactory(mdb.Name+"-headless", 27017, "admin", pw, "admin"),
	), nil
}

func (r *MongoDBReconciler) reconcileReplicaSetInitialization(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	logger := log.FromContext(ctx)
	logger.Info("Initializing replica set")

	rsManager, err := r.newRSManager(ctx, mdb)
	if err != nil {
		return fmt.Errorf("failed to create replica set manager: %w", err)
	}

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
	rsManager, err := r.newRSManager(ctx, mdb)
	if err != nil {
		return false, err
	}
	firstPod := fmt.Sprintf("%s-0", mdb.Name)
	return rsManager.HasPrimary(ctx, firstPod, mdb.Namespace)
}

func (r *MongoDBReconciler) reconcileAdminUser(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating admin user")

	// Get admin credentials from secret
	adminPassword, err := r.getAdminPassword(ctx, mdb)
	if err != nil {
		return fmt.Errorf("failed to get admin password: %w", err)
	}

	// Find the primary pod
	rsManager, err := r.newRSManager(ctx, mdb)
	if err != nil {
		return fmt.Errorf("failed to create replica set manager: %w", err)
	}

	firstPod := fmt.Sprintf("%s-0", mdb.Name)
	primaryPod, err := rsManager.GetPrimaryPod(ctx, firstPod, mdb.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get primary pod: %w", err)
	}

	// admin user는 mongodb pod의 lifecycle.postStart bootstrap이 만든다.
	// operator는 driver로 인증 시도해 user 존재와 password 정합성을 검증만 한다.
	authManager := mongodb.NewAuthManagerWithFactory(
		mongodb.NewPodConnectFactory(mdb.Name+"-headless", 27017, "admin", adminPassword, "admin"),
	)

	exists, err := authManager.UserExists(ctx, primaryPod, mdb.Namespace, "admin", "admin")
	if err != nil {
		// 인증 실패 또는 connect 실패는 transient로 보고 requeue. pod의 postStart가
		// 아직 끝나지 않았을 가능성이 높음.
		return fmt.Errorf("verify admin user: %w", err)
	}
	if !exists {
		return fmt.Errorf("admin user not found — pod의 postStart bootstrap이 실행되지 않았거나 실패함")
	}

	logger.Info("Admin user verified (created by pod bootstrap)")
	mdb.Status.AdminUserCreated = true
	return updateStatusWithRetry(ctx, r.Client, mdb)
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

func (r *MongoDBReconciler) createOrUpdate(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, obj client.Object) error {
	// Set owner reference
	if err := controllerutil.SetControllerReference(mdb, obj, r.Scheme); err != nil {
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

func (r *MongoDBReconciler) updateStatus(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	// Get StatefulSet status
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: mdb.Name, Namespace: mdb.Namespace}, sts); err != nil {
		if !errors.IsNotFound(err) {
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

	// Get current primary if replica set is initialized
	if mdb.Status.ReplicaSetInitialized {
		rsManager, err := r.newRSManager(ctx, mdb)
		if err == nil {
			firstPod := fmt.Sprintf("%s-0", mdb.Name)
			if primaryPod, err := rsManager.GetPrimaryPod(ctx, firstPod, mdb.Namespace); err == nil {
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
	conditions := []metav1.Condition{}

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

	mdb.Status.Phase = mongodbv1alpha1.PhaseFailed
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDB{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
