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
// 로 사용자 제공(secret: username/password), 미지정 시 operator 가 searchCoordinator user
// (dual SCRAM-SHA-1+256) + <name>-search-sync secret 을 자동 생성·관리(ensureSyncUser).

package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonsapply "github.com/keiailab/keiailab-commons/pkg/apply"
	commonsfinalizer "github.com/keiailab/keiailab-commons/pkg/finalizer"
	commonsreconcile "github.com/keiailab/keiailab-commons/pkg/reconcile"
	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	defaultSyncUser         = "search-sync"
	searchFinalizer         = mongodbv1beta1.FinalizerMongoDBSearch
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

	labelManagedBy = "app.kubernetes.io/managed-by"
	managedByValue = "mongodb-operator"
)

// MongoDBSearchReconciler reconciles MongoDBSearch — mongot sidecar 활성화.
type MongoDBSearchReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches/finalizers,verbs=update
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch

func (r *MongoDBSearchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("mongodbsearch", req.NamespacedName)

	search := &mongodbv1beta1.MongoDBSearch{}
	if err := r.Get(ctx, req.NamespacedName, search); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 삭제 처리(finalizer): auto-create searchCoordinator user 를 source 에서 dropUser(privilege-leak
	// 방지). RS/Sharded 분기 + source 부재/연결 불가는 best-effort(finalizer 해제로 CR wedge 방지).
	if !search.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, search)
	}

	if search.Spec.Source.MongoDBResourceRef == nil {
		return r.fail(ctx, search, "source.mongodbResourceRef required")
	}
	// finalizer 부착(auto-create 경로만 — 사용자 제공 user(syncUserSecretRef)는 operator 미관리라 drop 제외).
	// Requeue 없이 이어서 reconcile — finalizer Update 가 search 객체(resourceVersion)를 갱신하고 이후
	// status 는 UpdateWithRetry 가 최신 fetch 로 충돌 처리하므로, 첫 reconcile 에서 본 로직(secret/user/
	// sidecar)까지 한 번에 진행한다(early Requeue 는 첫 reconcile 결과를 기대하는 기존 동작/테스트와 충돌).
	if search.Spec.SyncUserSecretRef == nil && !commonsfinalizer.Has(search, searchFinalizer) {
		commonsfinalizer.Add(search, searchFinalizer)
		if err := r.Update(ctx, search); err != nil {
			return ctrl.Result{}, err
		}
	}
	// Sharded source — shard 별 mongot sidecar(별도 경로). RS 는 이하 진행.
	if search.Spec.Source.Kind == kindMongoDBSharded {
		return r.reconcileSharded(ctx, search, logger)
	}
	source := &mongodbv1alpha1.MongoDB{}
	if err := r.Get(ctx, types.NamespacedName{Name: search.Spec.Source.MongoDBResourceRef.Name, Namespace: search.Namespace}, source); err != nil {
		if apierrors.IsNotFound(err) {
			return r.pending(ctx, search, "source MongoDB not found yet")
		}
		return ctrl.Result{}, err
	}

	// searchCoordinator sync 사용자 secret: spec.SyncUserSecretRef 제공 시 그 이름(사용자 관리),
	// 미지정 시 operator 가 <name>-search-sync secret 생성. mongod user 생성과 분리(secret 은
	// cluster 무관 eager) — sidecar 주입(annotate)이 mongod 연결 가용성에 의존하지 않게 한다.
	syncSecret, err := r.ensureSyncSecret(ctx, search)
	if err != nil {
		return r.fail(ctx, search, fmt.Sprintf("ensure sync secret: %v", err)) // secret 생성 실패 = 영구
	}
	var syncUser string
	if search.Spec.SyncUserSecretRef != nil {
		// 사용자 제공 secret — username 키 존중(사용자가 secret 전체를 신뢰 관리).
		syncUser, err = r.resolveSyncUser(ctx, syncSecret, search.Namespace)
		if err != nil {
			return r.fail(ctx, search, fmt.Sprintf("sync secret invalid: %v", err))
		}
	} else {
		// 보안: auto-create 경로는 mongot config 의 syncUser 도 고정 defaultSyncUser 로 강제한다
		// (secret username 키 불신뢰 — ensureSyncMongoUser 의 mongod user 이름과 일관). 공격자가
		// <name>-search-sync secret 을 pre-stage 해 username 을 바꿔도 mongot 은 search-sync 로만
		// 인증 시도하고 operator 도 search-sync user 만 관리 → privilege escalation 차단.
		syncUser = defaultSyncUser
	}

	tlsEnabled := source.Spec.TLS != nil && source.Spec.TLS.Enabled
	// searchTLSMode(mongod→mongot gRPC 채널)는 localhost(in-pod) 평문 → 항상 disabled.
	// cluster TLS 는 mongot syncSource(BuildMongotConfigMap 의 tlsEnabled)에만 적용한다.
	// (구버전 requireTLS 는 mongot server.grpc.tls.mode enum[DISABLED|TLS|MTLS]과 충돌해
	//  config-parse crash — internal/resources/mongot.go BuildMongotConfigMap 참조.)
	tlsMode := "disabled"
	image := resources.MongotImage(search.Spec.Version)

	// mongot config ConfigMap(sidecar, localhost syncSource). owner=search → CR 삭제 시 GC.
	// RS source → mongod 27017. ReplicaSet 토폴로지 — router(mongos) 없음(routerHostPort="").
	cm := resources.BuildMongotConfigMap(source.Name, search.Namespace, search.Name, syncUser, tlsEnabled, resources.MongodReplicaSetPort, "")
	if err := commonsapply.ConfigMap(ctx, r.Client, r.Scheme, search, cm); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply mongot configmap: %w", err)
	}

	// source mongod 에 sidecar annotation → mongod builder 가 mongot sidecar + setParameter 주입.
	if err := r.annotateSource(ctx, source, image, syncSecret, tlsMode); err != nil {
		return ctrl.Result{}, fmt.Errorf("annotate source mongodb: %w", err)
	}

	// searchCoordinator user 보장(auto-create 경로, source Running 시 best-effort). annotate 뒤라
	// user 생성이 transient 실패해도 sidecar 주입은 진행되고, user 부재는 mongot readiness 가
	// Degraded 로 반영(아래 status) + 다음 reconcile 재시도(EnsureSearchCoordinatorUser 멱등).
	if err := r.ensureSyncMongoUser(ctx, search, source, syncSecret); err != nil {
		logger.Error(err, "searchCoordinator user ensure 실패 — sidecar 진행, 다음 reconcile 재시도")
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

// ensureSyncUser — searchCoordinator sync 사용자 secret 이름을 반환한다. spec.SyncUserSecretRef
// 제공 시 그 이름(사용자 관리, operator 미개입). 미지정 시 operator 가 <name>-search-sync secret
// 을 생성(password 보존)하고, source mongod 가 Running 이면 searchCoordinator user(dual SCRAM)를
// 생성·보정한다. secret 은 cluster 의존 없이 eager 생성(annotate→sidecar 주입 트리거를 막지 않기
// 위함), mongod user 생성은 SCRAM 인증 가능한 Running gate 안에서만(미Running 이면 다음 reconcile).
func (r *MongoDBSearchReconciler) ensureSyncSecret(ctx context.Context, search *mongodbv1beta1.MongoDBSearch) (string, error) {
	if search.Spec.SyncUserSecretRef != nil {
		return search.Spec.SyncUserSecretRef.Name, nil // 사용자 제공 — operator 미관리
	}
	secretName := search.Name + "-search-sync"
	// 보안(privilege escalation 차단) + self-heal 균형: auto-create secret 은 operator 소유여야
	// 신뢰한다. 공격자가 <name>-search-sync 를 pre-staging 해 password 를 심으면 operator(admin
	// 권한)가 그 password 로 searchCoordinator 특권 user 를 만들어 자격증명을 넘기게 되므로, 신뢰
	// 판정은 3-way:
	//   ① owner-ref(IsControlledBy) 일치 → operator 소유 확정, password 보존(rotate 사고 방지).
	//   ② owner-ref 없으나 managed-by 라벨 보유 → operator 가 만들었으나 ref 유실(velero restore /
	//      kubectl apply / --cascade=orphan). re-adopt: owner-ref 복구 + password 재생성(공격자가
	//      라벨을 위조했더라도 operator 가 password 를 새로 써서 심어둔 값을 무효화 → escalation 불가).
	//   ③ owner-ref 도 managed-by 도 없음 → 진짜 foreign/pre-staged. adopt 거부(fail).
	// 없으면 owner-ref + managed-by 라벨 + operator 생성 random password 로 Create.
	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: search.Namespace}, existing)
	switch {
	case err == nil:
		if metav1.IsControlledBy(existing, search) {
			return secretName, nil // ① operator 소유 — password 보존
		}
		if existing.Labels[labelManagedBy] != managedByValue {
			return "", fmt.Errorf("sync secret %q 가 이미 존재하나 operator 소유 아님(owner-ref/managed-by 부재) — "+
				"foreign/pre-staged secret adopt 거부(privilege escalation 방지). 삭제하거나 spec.syncUserSecretRef 로 명시 지정", secretName)
		}
		// ② managed-by 라벨 보유, owner-ref 유실 → re-adopt(ref 복구 + password 재생성으로 위조 무효화).
		if err := controllerutil.SetControllerReference(search, existing, r.Scheme); err != nil {
			return "", fmt.Errorf("re-adopt sync secret owner ref: %w", err)
		}
		if existing.Data == nil {
			existing.Data = map[string][]byte{}
		}
		existing.Data["username"] = []byte(defaultSyncUser)
		existing.Data["password"] = []byte(generateSyncPassword())
		if err := r.Update(ctx, existing); err != nil {
			return "", fmt.Errorf("re-adopt sync secret update: %w", err)
		}
		return secretName, nil
	case apierrors.IsNotFound(err):
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: search.Namespace,
				Labels:    map[string]string{labelManagedBy: managedByValue, "mongodb.keiailab.com/search": search.Name},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"username": []byte(defaultSyncUser), "password": []byte(generateSyncPassword())},
		}
		if err := controllerutil.SetControllerReference(search, secret, r.Scheme); err != nil {
			return "", fmt.Errorf("set owner ref on sync secret: %w", err)
		}
		if err := r.Create(ctx, secret); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return r.ensureSyncSecret(ctx, search) // race: 막 생성됨 → 재검증(소유 확인)
			}
			return "", fmt.Errorf("create sync secret: %w", err)
		}
		return secretName, nil
	default:
		return "", fmt.Errorf("get sync secret: %w", err)
	}
}

// ensureSyncMongoUser — auto-create 경로에서 source mongod 에 searchCoordinator user(dual SCRAM)를
// 생성·보정한다. SyncUserSecretRef 제공(사용자 관리) 또는 source 미Running 시 no-op. 호출자가
// best-effort 로 부르며(실패해도 sidecar 진행), EnsureSearchCoordinatorUser 가 usersInfo precheck
// 로 멱등 — 이미 정상이면 mongod write 없음.
func (r *MongoDBSearchReconciler) ensureSyncMongoUser(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, source *mongodbv1alpha1.MongoDB, secretName string) error {
	if search.Spec.SyncUserSecretRef != nil {
		return nil // 사용자 제공 user — operator 가 mongod user 미관리
	}
	if source.Status.Phase != mongodbPhaseRunning {
		return nil // SCRAM 인증 가능 시점 아님 — 다음 reconcile
	}
	s := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: search.Namespace}, s); err != nil {
		return fmt.Errorf("get sync secret: %w", err)
	}
	// 보안: auto-create 경로의 mongod user 이름은 항상 고정 defaultSyncUser 로 강제한다.
	// secret 의 username 키를 신뢰해 mongod user 를 만들면, 공격자가 <name>-search-sync secret 을
	// 미리 staging 해 username=admin(등 특권 user) 으로 두면 operator(admin 권한)가 그 user 에
	// searchCoordinator role + 공격자 password 를 부여 → 계정 탈취/권한 상승(privilege escalation).
	// operator 가 생성·관리하는 user 의 이름은 operator 가 결정하지 secret 입력에 의존하지 않는다.
	pw := string(s.Data["password"])
	adminPw, err := r.sourceAdminPassword(ctx, source)
	if err != nil {
		return err
	}
	factory := mongodb.NewPodConnectFactory(source.Name+"-headless", 27017, "admin", adminPw, "admin")
	conn, err := factory(ctx, source.Name+"-0", source.Namespace, false) // direct=false → write 를 primary 로 라우팅
	if err != nil {
		return fmt.Errorf("connect source mongod: %w", err)
	}
	defer func() { _ = conn.Disconnect(ctx) }()
	return mongodb.EnsureSearchCoordinatorUser(ctx, conn, defaultSyncUser, pw)
}

// handleDeletion — finalizer cleanup: operator auto-create searchCoordinator user 를 source 에서
// dropUser(멱등). drop 대상 판정 = <name>-search-sync managed-by secret(auto-create 흔적) 존재 여부 —
// spec.SyncUserSecretRef *현재값* 으로 판정하면 nil→set 변경 시 과거 auto-create user 가 drop 누락
// → leak(review #3). 사용자 제공 user(foreign secret, managed-by 부재)는 제외. drop 함수는 source
// 소멸만 best-effort nil, transient/연결 불가/drop 실패는 재시도(leak-free, review #1).
func (r *MongoDBSearchReconciler) handleDeletion(ctx context.Context, search *mongodbv1beta1.MongoDBSearch) (ctrl.Result, error) {
	cleanup := func(ctx context.Context) error {
		if search.Spec.Source.MongoDBResourceRef == nil {
			return nil // source ref 부재 — drop 불가(best-effort)
		}
		// operator-managed user 존재 여부 = auto-create 흔적 secret(managed-by 라벨)로 판정.
		managed := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: search.Name + "-search-sync", Namespace: search.Namespace}, managed); err != nil {
			return client.IgnoreNotFound(err) // 흔적 secret 부재(NotFound)→사용자 제공/미생성, drop 대상 아님; 그 외 API err→재시도
		}
		if managed.Labels[labelManagedBy] != managedByValue {
			return nil // foreign secret — operator 미관리(privilege escalation 방지 정합)
		}
		if search.Spec.Source.Kind == kindMongoDBSharded {
			return r.dropSyncMongoUserSharded(ctx, search)
		}
		return r.dropSyncMongoUser(ctx, search)
	}
	return commonsreconcile.HandleFinalizerCleanup(ctx, r.Client, search, searchFinalizer, cleanup)
}

// dropSyncMongoUser — RS source pod-0 에서 searchCoordinator user drop(ensureSyncMongoUser 거울).
// leak-free 정책(adversarial review #1): source 소멸(Get NotFound)만 best-effort nil(user 도 함께 소멸).
// transient non-Running(rolling/failover)/연결 불가/drop 실패는 *error 반환* → HandleFinalizerCleanup 가
// finalizer 유지 + 재시도 → 살아있는 source 의 searchCoordinator 특권 user orphan(privilege-leak) 방지.
func (r *MongoDBSearchReconciler) dropSyncMongoUser(ctx context.Context, search *mongodbv1beta1.MongoDBSearch) error {
	source := &mongodbv1alpha1.MongoDB{}
	if err := r.Get(ctx, types.NamespacedName{Name: search.Spec.Source.MongoDBResourceRef.Name, Namespace: search.Namespace}, source); err != nil {
		return client.IgnoreNotFound(err) // source 소멸(NotFound)만 best-effort nil; 그 외 API err → 재시도
	}
	if source.Status.Phase != mongodbPhaseRunning {
		return fmt.Errorf("source MongoDB %q not Running(phase=%q) — searchCoordinator drop 재시도 대기", source.Name, source.Status.Phase)
	}
	adminPw, err := r.sourceAdminPassword(ctx, source)
	if err != nil {
		return fmt.Errorf("source admin password(drop): %w", err)
	}
	factory := mongodb.NewPodConnectFactory(source.Name+"-headless", 27017, "admin", adminPw, "admin")
	conn, err := factory(ctx, source.Name+"-0", source.Namespace, false)
	if err != nil {
		return fmt.Errorf("connect source mongod(drop): %w", err)
	}
	defer func() { _ = conn.Disconnect(ctx) }()
	return mongodb.DropSearchCoordinatorUser(ctx, conn, defaultSyncUser)
}

// dropSyncMongoUserSharded — Sharded source 의 mongos(config server)+각 shard 에서 searchCoordinator
// user drop(ensureSyncMongoUserSharded ①+② 거울). leak-free 정책(#1): source 소멸(NotFound)만 best-effort,
// transient non-Running/연결 불가/drop 실패는 첫 error 반환 → finalizer 유지 + 재시도(orphan 제거 보장).
// drop 은 멱등(UserNotFound→nil)이라 재시도 안전.
func (r *MongoDBSearchReconciler) dropSyncMongoUserSharded(ctx context.Context, search *mongodbv1beta1.MongoDBSearch) error {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{}
	if err := r.Get(ctx, types.NamespacedName{Name: search.Spec.Source.MongoDBResourceRef.Name, Namespace: search.Namespace}, mdbsh); err != nil {
		return client.IgnoreNotFound(err) // source 소멸(NotFound)만 best-effort nil
	}
	if mdbsh.Status.Phase != mongodbv1alpha1.ShardedPhaseRunning {
		return fmt.Errorf("source MongoDBSharded %q not Running(phase=%q) — drop 재시도 대기", mdbsh.Name, mdbsh.Status.Phase)
	}
	adminSecretName := mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name
	if adminSecretName == "" {
		return fmt.Errorf("source MongoDBSharded %q admin credentials secret 미설정 — drop 불가", mdbsh.Name)
	}
	adminS := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: adminSecretName, Namespace: mdbsh.Namespace}, adminS); err != nil {
		return fmt.Errorf("get admin secret(drop): %w", err)
	}
	adminPw := string(adminS.Data["password"])
	if adminPw == "" {
		return fmt.Errorf("admin secret %q password 키 없음(drop)", adminSecretName)
	}
	var dropErr error
	setErr := func(e error) {
		if e != nil && dropErr == nil {
			dropErr = e
		}
	}
	// ① mongos(config server) — syncSource.router 인증용 user drop.
	factory := mongodb.NewServiceConnectFactory(mdbsh.Name+"-mongos", mdbsh.Namespace, resources.MongosPort, "admin", adminPw, "admin")
	if conn, err := factory(ctx, "", mdbsh.Namespace, false); err != nil {
		setErr(fmt.Errorf("connect mongos(drop): %w", err))
	} else {
		setErr(mongodb.DropSearchCoordinatorUser(ctx, conn, defaultSyncUser))
		_ = conn.Disconnect(ctx)
	}
	// ② 각 shard RS mongod 직접(:27018) — syncSource.replicaSet 인증용 user drop.
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shardSvc := fmt.Sprintf("%s-shard-%d-headless", mdbsh.Name, i)
		shardSeed := fmt.Sprintf("%s-shard-%d-0", mdbsh.Name, i)
		sf := mongodb.NewPodConnectFactory(shardSvc, resources.MongodShardPort, "admin", adminPw, "admin")
		if sconn, serr := sf(ctx, shardSeed, mdbsh.Namespace, false); serr != nil {
			setErr(fmt.Errorf("connect shard %d(drop): %w", i, serr))
		} else {
			setErr(mongodb.DropSearchCoordinatorUser(ctx, sconn, defaultSyncUser))
			_ = sconn.Disconnect(ctx)
		}
	}
	return dropErr // 연결/drop 실패 → 재시도(orphan 제거 보장). source 소멸만 위에서 nil.
}

// sourceAdminPassword — source MongoDB 의 admin credential secret 에서 password 를 읽는다
// (mongodb_controller.go getAdminPassword 패턴). admin username 은 "admin" 고정.
func (r *MongoDBSearchReconciler) sourceAdminPassword(ctx context.Context, source *mongodbv1alpha1.MongoDB) (string, error) {
	secretName := source.Spec.Auth.AdminCredentialsSecretRef.Name
	if secretName == "" {
		return "", fmt.Errorf("source MongoDB %q 에 admin credentials secret 미설정", source.Name)
	}
	s := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: source.Namespace}, s); err != nil {
		return "", fmt.Errorf("get admin secret %s: %w", secretName, err)
	}
	pw := string(s.Data["password"])
	if pw == "" {
		return "", fmt.Errorf("admin secret %s 에 password 키 없음", secretName)
	}
	return pw, nil
}

// generateSyncPassword — searchCoordinator user 용 랜덤 password(crypto/rand 32byte → base64url).
func generateSyncPassword() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
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

// reconcileSharded — MongoDBSharded source 의 search 활성화. shard replicaSet 마다 mongot sidecar 를
// 주입한다(각 shard 가 자기 데이터만 인덱싱, mongos 가 $search fan-out). shard 별 mongot ConfigMap
// (port 27018)을 생성하고 MongoDBSharded CR 에 annotation → shard STS builder 가 sidecar 주입.
// config server 는 mongot 미배포(메타데이터만). 무롤링: annotation 부재 시 shard STS 무변경.
func (r *MongoDBSearchReconciler) reconcileSharded(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, logger logr.Logger) (ctrl.Result, error) {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{}
	if err := r.Get(ctx, types.NamespacedName{Name: search.Spec.Source.MongoDBResourceRef.Name, Namespace: search.Namespace}, mdbsh); err != nil {
		if apierrors.IsNotFound(err) {
			return r.pending(ctx, search, "source MongoDBSharded not found yet")
		}
		return ctrl.Result{}, err
	}

	syncSecret, err := r.ensureSyncSecret(ctx, search) // RS 와 공통(secret 이름 = search 기준)
	if err != nil {
		return r.fail(ctx, search, fmt.Sprintf("ensure sync secret: %v", err))
	}
	syncUser := defaultSyncUser
	if search.Spec.SyncUserSecretRef != nil {
		if syncUser, err = r.resolveSyncUser(ctx, syncSecret, search.Namespace); err != nil {
			return r.fail(ctx, search, fmt.Sprintf("sync secret invalid: %v", err))
		}
	}

	tlsEnabled := mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled
	// searchTLSMode(mongod→mongot gRPC 채널)는 localhost(in-pod) 평문 → 항상 disabled.
	// cluster TLS 는 mongot syncSource(BuildMongotConfigMap 의 tlsEnabled)에만 적용한다.
	// (구버전 requireTLS 는 mongot server.grpc.tls.mode enum[DISABLED|TLS|MTLS]과 충돌해
	//  config-parse crash — internal/resources/mongot.go BuildMongotConfigMap 참조.)
	tlsMode := "disabled"
	image := resources.MongotImage(search.Spec.Version)

	// shard 별 mongot ConfigMap(port 27018 — shard mongod listen). owner=search → GC.
	// Sharded: mongot 은 router(mongos)로 cluster topology, replicaSet(로컬 shard)로 데이터 sync.
	// router 부재 시 mongot 이 "cluster is sharded but syncSource.router is not configured" 로 정지.
	routerHostPort := fmt.Sprintf("%s-mongos.%s.svc.cluster.local:%d", mdbsh.Name, search.Namespace, resources.MongosPort)
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shardName := fmt.Sprintf("%s-shard-%d", mdbsh.Name, i)
		cm := resources.BuildMongotConfigMap(shardName, search.Namespace, search.Name, syncUser, tlsEnabled, resources.MongodShardPort, routerHostPort)
		if err := commonsapply.ConfigMap(ctx, r.Client, r.Scheme, search, cm); err != nil {
			return ctrl.Result{}, fmt.Errorf("apply shard mongot configmap %s: %w", shardName, err)
		}
	}

	// MongoDBSharded CR annotate → shard STS builder 가 mongot sidecar 주입(모든 shard).
	if err := r.annotateShardedSource(ctx, mdbsh, image, syncSecret, tlsMode); err != nil {
		return ctrl.Result{}, fmt.Errorf("annotate source mongodbsharded: %w", err)
	}

	// searchCoordinator user 보장(auto-create 경로, mongos 경유 best-effort).
	if err := r.ensureSyncMongoUserSharded(ctx, search, mdbsh, syncSecret); err != nil {
		logger.Error(err, "searchCoordinator user(sharded) ensure 실패 — sidecar 진행, 다음 reconcile 재시도")
	}

	// status: mongot 은 shard mongod pod 의 sidecar 컨테이너 — RS 와 동일하게 *실제 sidecar readiness*
	// 를 집계해 phase 결정(cluster Running 만으로 조기 Ready 승격 금지 — sidecar 롤링 전 SearchIndex 가
	// mongot 미서빙 상태에서 인덱스를 조기 시도하는 것 방지). cluster not Running → Provisioning,
	// shard sidecar 일부 ready → Syncing, 전부 ready → Ready, sidecar 있으나 0 ready → Degraded.
	var readyReplicas, totalSidecars int32
	phase := searchPhaseProvisioning
	if mdbsh.Status.Phase == mongodbv1alpha1.ShardedPhaseRunning {
		readyReplicas, totalSidecars, err = r.countReadyMongotSidecarsSharded(ctx, mdbsh)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("count sharded mongot sidecars: %w", err)
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
	logger.Info("reconciled sharded (per-shard sidecar)", "phase", phase, "shards", mdbsh.Spec.Shards.Count, "readyMongot", readyReplicas, "totalMongot", totalSidecars, "image", image)
	if phase != searchPhaseReady {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// countReadyMongotSidecarsSharded — 모든 shard STS 의 mongot sidecar ready 집계(countReadyMongotSidecars
// 의 sharded 판 — shard 0..Count-1 순회). shard STS 미생성(롤링 전)은 skip(graceful). config server/
// mongos 는 mongot 미배포라 집계 대상 아님.
func (r *MongoDBSearchReconciler) countReadyMongotSidecarsSharded(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ready, total int32, err error) {
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shardName := fmt.Sprintf("%s-shard-%d", mdbsh.Name, i)
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: shardName, Namespace: mdbsh.Namespace}, sts); err != nil {
			if apierrors.IsNotFound(err) {
				continue // shard STS 아직 미생성(롤링 전)
			}
			return 0, 0, err
		}
		pods := &corev1.PodList{}
		if err := r.List(ctx, pods, client.InNamespace(mdbsh.Namespace), client.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
			return 0, 0, err
		}
		for j := range pods.Items {
			for _, cs := range pods.Items[j].Status.ContainerStatuses {
				if cs.Name == mongotContainerName {
					total++
					if cs.Ready {
						ready++
					}
				}
			}
		}
	}
	return ready, total, nil
}

// annotateShardedSource — MongoDBSharded 에 sidecar annotation(idempotent). annotateSource 의 sharded 판.
func (r *MongoDBSearchReconciler) annotateShardedSource(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, image, syncSecret, tlsMode string) error {
	if mdbsh.Annotations[resources.MongotSidecarImageAnnotation] == image &&
		mdbsh.Annotations[resources.MongotSyncSecretAnnotation] == syncSecret &&
		mdbsh.Annotations[resources.MongotTLSModeAnnotation] == tlsMode {
		return nil
	}
	patched := mdbsh.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	patched.Annotations[resources.MongotSidecarImageAnnotation] = image
	patched.Annotations[resources.MongotSyncSecretAnnotation] = syncSecret
	patched.Annotations[resources.MongotTLSModeAnnotation] = tlsMode
	return r.Patch(ctx, patched, client.MergeFrom(mdbsh))
}

// ensureSyncMongoUserSharded — sharded source 의 searchCoordinator user 를 *두 경로* 모두에 생성한다:
// ① mongos 경유(config server 저장) — mongot syncSource.router(mongos) 인증용
// ② 각 shard RS mongod 직접(:27018) — mongot syncSource.replicaSet(로컬 shard) 인증용
// mongos 경유 생성 user 는 config server 에만 저장되어 *shard-direct SCRAM 인증에는 부재*하다(MongoDB
// sharded 동작 — direct shard 연결은 shard-local admin.system.users 만 인증). mongot 은 per-shard
// replicaSet 에 shard mongod 로 *직접* 인증하므로 각 shard 에도 user 가 필요하다(admin 은 shard
// postStart hook 이 로컬 생성 → shard-direct 연결 가능). ①만 했던 구버전 = mongot AuthenticationFailed
// (prod sharded search 활성화 2026-06-24 실측). SyncUserSecretRef 제공/cluster 미Running 시 no-op. best-effort.
func (r *MongoDBSearchReconciler) ensureSyncMongoUserSharded(ctx context.Context, search *mongodbv1beta1.MongoDBSearch, mdbsh *mongodbv1alpha1.MongoDBSharded, secretName string) error {
	if search.Spec.SyncUserSecretRef != nil {
		return nil
	}
	if mdbsh.Status.Phase != mongodbv1alpha1.ShardedPhaseRunning {
		return nil
	}
	s := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: search.Namespace}, s); err != nil {
		return fmt.Errorf("get sync secret: %w", err)
	}
	pw := string(s.Data["password"]) // 보안: username 은 항상 defaultSyncUser 고정(RS 와 동일).
	adminSecretName := mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name
	if adminSecretName == "" {
		return fmt.Errorf("source MongoDBSharded %q 에 admin credentials secret 미설정", mdbsh.Name)
	}
	adminS := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: adminSecretName, Namespace: mdbsh.Namespace}, adminS); err != nil {
		return fmt.Errorf("get admin secret %s: %w", adminSecretName, err)
	}
	adminPw := string(adminS.Data["password"])
	if adminPw == "" {
		return fmt.Errorf("admin secret %s 에 password 키 없음", adminSecretName)
	}
	// ① mongos 경유 — config server 저장, mongot syncSource.router(mongos) 인증용.
	factory := mongodb.NewServiceConnectFactory(mdbsh.Name+"-mongos", mdbsh.Namespace, resources.MongosPort, "admin", adminPw, "admin")
	conn, err := factory(ctx, "", mdbsh.Namespace, false) // service connect — podName 무시, mongos 가 라우팅
	if err != nil {
		return fmt.Errorf("connect mongos: %w", err)
	}
	rerr := mongodb.EnsureSearchCoordinatorUser(ctx, conn, defaultSyncUser, pw)
	_ = conn.Disconnect(ctx)
	if rerr != nil {
		return fmt.Errorf("ensure search-sync via mongos(router): %w", rerr)
	}
	// ② 각 shard RS mongod 직접(:27018) — mongot syncSource.replicaSet(로컬 shard) 인증용.
	// NewPodConnectFactory(headless, 27018) + shard-<i>-0 seed + direct=false → shard RS primary 라우팅
	// (RS 경로 ensureSyncMongoUser 와 동일 패턴). EnsureSearchCoordinatorUser 멱등(usersInfo precheck).
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shardSvc := fmt.Sprintf("%s-shard-%d-headless", mdbsh.Name, i)
		shardSeed := fmt.Sprintf("%s-shard-%d-0", mdbsh.Name, i)
		sf := mongodb.NewPodConnectFactory(shardSvc, resources.MongodShardPort, "admin", adminPw, "admin")
		sconn, serr := sf(ctx, shardSeed, mdbsh.Namespace, false)
		if serr != nil {
			return fmt.Errorf("connect shard %d mongod: %w", i, serr)
		}
		serr = mongodb.EnsureSearchCoordinatorUser(ctx, sconn, defaultSyncUser, pw)
		_ = sconn.Disconnect(ctx)
		if serr != nil {
			return fmt.Errorf("ensure search-sync on shard %d: %w", i, serr)
		}
	}
	return nil
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
