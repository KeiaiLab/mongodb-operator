/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbsearchindex_controller.go — MongoDBSearchIndex reconcile. MongoDBSearch 가 mongot 을
// *배포* 한다면, MongoDBSearchIndex 는 그 위에 $search/$vectorSearch 인덱스를 *선언* 한다.
// controller 는 SearchRef→MongoDBSearch→source MongoDB(admin) 로 연결해 source mongod 에
// createSearchIndex/updateSearchIndex 를 reconcile 하고(mongod 가 mongot sidecar 로 forward),
// $listSearchIndexes 로 상태를 polling 한다. 삭제 시 finalizer 가 dropSearchIndex 한다.
// MVP: source = MongoDB(ReplicaSet). Sharded 는 후속(mongos 경유 create).
package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"go.mongodb.org/mongo-driver/v2/bson"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonsfinalizer "github.com/keiailab/keiailab-commons/pkg/finalizer"
	commonsreconcile "github.com/keiailab/keiailab-commons/pkg/reconcile"
	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
)

const (
	searchIndexFinalizer = mongodbv1beta1.FinalizerMongoDBSearchIndex

	siPhasePending  = "Pending"
	siPhaseBuilding = "Building"
	siPhaseReady    = "Ready"
	siPhaseFailed   = "Failed"
	siPhaseDeleting = "Deleting"

	siConditionReady = "Ready"
)

// searchIndexOps — 컨트롤러가 의존하는 search index 작업(SearchIndexManager 가 구현).
// 인터페이스로 추출해 테스트에서 fake 주입 가능(repo 에 mongo mock 부재).
type searchIndexOps interface {
	List(ctx context.Context, podName, namespace, db, coll, name string) ([]mongodb.SearchIndexInfo, error)
	Create(ctx context.Context, podName, namespace, db, coll, name, indexType string, def bson.M) (string, error)
	Update(ctx context.Context, podName, namespace, db, coll, name string, def bson.M) error
	Drop(ctx context.Context, podName, namespace, db, coll, name string) error
}

// searchIndexManagerFactory — source mongod 연결 자격으로 searchIndexOps 를 만든다.
// 테스트에서 fake 로 교체(주입 패턴).
type searchIndexManagerFactory func(podName, serviceName, namespace, username, password string) searchIndexOps

// MongoDBSearchIndexReconciler reconciles MongoDBSearchIndex — 선언적 search index lifecycle.
type MongoDBSearchIndexReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// NewManager — nil 이면 기본(실 mongod 연결). 테스트가 override.
	NewManager searchIndexManagerFactory
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearchindices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearchindices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearchindices/finalizers,verbs=update
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbsearches,verbs=get;list;watch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs,verbs=get;list;watch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbshardeds,verbs=get;list;watch

func (r *MongoDBSearchIndexReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("mongodbsearchindex", req.NamespacedName)

	idx := &mongodbv1beta1.MongoDBSearchIndex{}
	if err := r.Get(ctx, req.NamespacedName, idx); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// resolve: SearchRef → MongoDBSearch → source MongoDB(admin).
	search := &mongodbv1beta1.MongoDBSearch{}
	if err := r.Get(ctx, types.NamespacedName{Name: idx.Spec.SearchRef.Name, Namespace: idx.Namespace}, search); err != nil {
		if apierrors.IsNotFound(err) {
			if !idx.DeletionTimestamp.IsZero() {
				return r.handleDeletion(ctx, idx, nil, "") // search 없으면 best-effort(연결 불가) → finalizer 해제
			}
			return r.pending(ctx, idx, "MongoDBSearch not found yet")
		}
		return ctrl.Result{}, err
	}
	if search.Spec.Source.MongoDBResourceRef == nil {
		return r.fail(ctx, idx, "MongoDBSearch.spec.source.mongodbResourceRef required")
	}

	// source 연결 해소(RS=pod-0 직결 / Sharded=mongos service). connectPod 는 connect factory 의 podName
	// (Sharded 는 service connect 라 무시). pendingReason!="" = source/자격증명 미가용(연결 불가).
	mgr, connectPod, sourceRunning, pendingReason, err := r.resolveSourceManager(ctx, idx, search)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pendingReason != "" {
		if !idx.DeletionTimestamp.IsZero() {
			return r.handleDeletion(ctx, idx, nil, "") // 연결 불가 → best-effort finalizer 해제(wedge 방지)
		}
		return r.pending(ctx, idx, pendingReason)
	}

	// 삭제 처리(finalizer): dropSearchIndex.
	if !idx.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, idx, mgr, connectPod)
	}

	// finalizer 부착(삭제 시 drop 보장).
	if !commonsfinalizer.Has(idx, searchIndexFinalizer) {
		commonsfinalizer.Add(idx, searchIndexFinalizer)
		if err := r.Update(ctx, idx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// readiness gate: source Running + search Ready 아니면 mongot 미서빙 → 대기.
	if !sourceRunning || search.Status.Phase != searchPhaseReady {
		return r.building(ctx, idx, "source mongod / mongot sidecar not ready yet")
	}

	// 인덱스 정의 파싱.
	def := bson.M{}
	if err := bson.UnmarshalExtJSON([]byte(idx.Spec.DefinitionJSON), true, &def); err != nil {
		return r.fail(ctx, idx, fmt.Sprintf("invalid definitionJSON: %v", err))
	}

	return r.ensureIndex(ctx, idx, connectPod, mgr, def, logger)
}

// ensureIndex — source 의 search index 를 desired 로 reconcile + status polling. connectPod 는 connect
// factory 의 podName 인자(RS=<name>-0 직결 / Sharded=""=mongos service connect 가 라우팅).
func (r *MongoDBSearchIndexReconciler) ensureIndex(ctx context.Context, idx *mongodbv1beta1.MongoDBSearchIndex,
	connectPod string, mgr searchIndexOps, def bson.M, logger logr.Logger) (ctrl.Result, error) {
	existing, err := mgr.List(ctx, connectPod, idx.Namespace, idx.Spec.Database, idx.Spec.Collection, idx.Spec.IndexName)
	if err != nil {
		return r.pending(ctx, idx, fmt.Sprintf("list search index: %v", err))
	}

	if len(existing) == 0 {
		// 생성.
		if _, err := mgr.Create(ctx, connectPod, idx.Namespace, idx.Spec.Database, idx.Spec.Collection, idx.Spec.IndexName, indexType(idx), def); err != nil {
			return r.fail(ctx, idx, fmt.Sprintf("create search index: %v", err))
		}
		logger.Info("search index 생성", "name", idx.Spec.IndexName, "type", indexType(idx))
		return r.building(ctx, idx, "search index created, building")
	}

	// 존재 — status 매핑. (definition drift 갱신은 후속: canonical 비교 필요.)
	info := existing[0]
	phase := string(mongodb.ClassifyMongotStatus(info.Status, info.Queryable))
	if err := r.applyStatus(ctx, idx, phase, info.ID, info.Queryable, ""); err != nil {
		return ctrl.Result{}, err
	}
	switch phase {
	case siPhaseReady:
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil // 안정 — 느린 polling
	case siPhaseFailed:
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	default: // Building/Pending — 빌드 진행 polling
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
}

// handleDeletion — finalizer cleanup: dropSearchIndex(멱등). mgr nil(연결 불가)이면 drop skip +
// finalizer 해제(클러스터 소멸 시 CR wedge 방지).
func (r *MongoDBSearchIndexReconciler) handleDeletion(ctx context.Context, idx *mongodbv1beta1.MongoDBSearchIndex, mgr searchIndexOps, sourcePod string) (ctrl.Result, error) {
	_ = r.applyStatus(ctx, idx, siPhaseDeleting, idx.Status.IndexID, false, "")
	cleanup := func(ctx context.Context) error {
		if mgr == nil {
			return nil // source 소멸/연결 불가 → best-effort skip(CR wedge 방지)
		}
		// drop 은 source mongod 의 pod-0 기준(Reconcile 이 source 를 이미 해소). 멱등(없으면 성공).
		return mgr.Drop(ctx, sourcePod, idx.Namespace, idx.Spec.Database, idx.Spec.Collection, idx.Spec.IndexName)
	}
	return commonsreconcile.HandleFinalizerCleanup(ctx, r.Client, idx, searchIndexFinalizer, cleanup)
}

// resolveSourceManager — search.source.Kind(MongoDB|MongoDBSharded)별로 searchIndexOps + connectPod +
// running 을 해소한다. RS=pod-0 직결(NewPodConnectFactory), Sharded=mongos service(NewServiceConnectFactory
// — mongos 가 per-shard mongot 으로 전파). pendingReason!="" = source 부재/자격증명 미가용(호출자가
// deletion 이면 best-effort finalizer 해제, 아니면 pending). err!=nil = API 조회 실패(재큐).
func (r *MongoDBSearchIndexReconciler) resolveSourceManager(ctx context.Context, idx *mongodbv1beta1.MongoDBSearchIndex, search *mongodbv1beta1.MongoDBSearch) (mgr searchIndexOps, connectPod string, running bool, pendingReason string, err error) {
	if search.Spec.Source.Kind == kindMongoDBSharded {
		mdbsh := &mongodbv1alpha1.MongoDBSharded{}
		if e := r.Get(ctx, types.NamespacedName{Name: search.Spec.Source.MongoDBResourceRef.Name, Namespace: idx.Namespace}, mdbsh); e != nil {
			if apierrors.IsNotFound(e) {
				return nil, "", false, "source MongoDBSharded not found yet", nil
			}
			return nil, "", false, "", e
		}
		m, e := r.managerForShardedSource(ctx, mdbsh)
		if e != nil {
			return nil, "", false, fmt.Sprintf("source connection unavailable: %v", e), nil
		}
		// Sharded 인덱스 명령은 mongos 경유(connectPod 무시 — service connect 가 라우팅).
		return m, "", mdbsh.Status.Phase == mongodbv1alpha1.ShardedPhaseRunning, "", nil
	}
	source := &mongodbv1alpha1.MongoDB{}
	if e := r.Get(ctx, types.NamespacedName{Name: search.Spec.Source.MongoDBResourceRef.Name, Namespace: idx.Namespace}, source); e != nil {
		if apierrors.IsNotFound(e) {
			return nil, "", false, "source MongoDB not found yet", nil
		}
		return nil, "", false, "", e
	}
	m, e := r.managerForSource(ctx, source)
	if e != nil {
		return nil, "", false, fmt.Sprintf("source connection unavailable: %v", e), nil
	}
	return m, source.Name + "-0", source.Status.Phase == mongodbPhaseRunning, "", nil
}

// managerForSource — source MongoDB 의 admin 자격으로 SearchIndexManager 생성.
func (r *MongoDBSearchIndexReconciler) managerForSource(ctx context.Context, source *mongodbv1alpha1.MongoDB) (searchIndexOps, error) {
	adminPw, err := r.sourceAdminPasswordForIndex(ctx, source)
	if err != nil {
		return nil, err
	}
	if r.NewManager != nil {
		return r.NewManager(source.Name+"-0", source.Name+"-headless", source.Namespace, "admin", adminPw), nil
	}
	factory := mongodb.NewPodConnectFactory(source.Name+"-headless", 27017, "admin", adminPw, "admin")
	return mongodb.NewSearchIndexManagerWithFactory(factory), nil
}

// managerForShardedSource — sharded source 의 admin 자격으로 SearchIndexManager 생성. 인덱스 명령은
// mongos(<name>-mongos:27017) 경유(mongos 가 config+shard 로 전파 — 개별 shard 직접 아님). NewManager
// 주입 시 테스트 fake.
func (r *MongoDBSearchIndexReconciler) managerForShardedSource(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (searchIndexOps, error) {
	adminPw, err := r.adminPasswordFor(ctx, mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name, mdbsh.Namespace)
	if err != nil {
		return nil, err
	}
	if r.NewManager != nil {
		return r.NewManager("", mdbsh.Name+"-mongos", mdbsh.Namespace, "admin", adminPw), nil
	}
	factory := mongodb.NewServiceConnectFactory(mdbsh.Name+"-mongos", mdbsh.Namespace, 27017, "admin", adminPw, "admin")
	return mongodb.NewSearchIndexManagerWithFactory(factory), nil
}

// sourceAdminPasswordForIndex — source MongoDB admin secret password 읽기(getAdminPassword 패턴).
func (r *MongoDBSearchIndexReconciler) sourceAdminPasswordForIndex(ctx context.Context, source *mongodbv1alpha1.MongoDB) (string, error) {
	return r.adminPasswordFor(ctx, source.Spec.Auth.AdminCredentialsSecretRef.Name, source.Namespace)
}

// adminPasswordFor — admin credential secret 에서 password 읽기(RS/Sharded 공용, getAdminPassword 패턴).
func (r *MongoDBSearchIndexReconciler) adminPasswordFor(ctx context.Context, secretName, namespace string) (string, error) {
	if secretName == "" {
		return "", fmt.Errorf("admin credentials secret 미설정")
	}
	s := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, s); err != nil {
		return "", fmt.Errorf("get admin secret %s: %w", secretName, err)
	}
	pw := string(s.Data["password"])
	if pw == "" {
		return "", fmt.Errorf("admin secret %s 에 password 키 없음", secretName)
	}
	return pw, nil
}

// indexType — spec.Type 기본값 search.
func indexType(idx *mongodbv1beta1.MongoDBSearchIndex) string {
	if idx.Spec.Type == "" {
		return "search"
	}
	return idx.Spec.Type
}

func (r *MongoDBSearchIndexReconciler) pending(ctx context.Context, idx *mongodbv1beta1.MongoDBSearchIndex, msg string) (ctrl.Result, error) {
	_ = r.applyStatus(ctx, idx, siPhasePending, idx.Status.IndexID, false, msg)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *MongoDBSearchIndexReconciler) building(ctx context.Context, idx *mongodbv1beta1.MongoDBSearchIndex, msg string) (ctrl.Result, error) {
	_ = r.applyStatus(ctx, idx, siPhaseBuilding, idx.Status.IndexID, false, msg)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *MongoDBSearchIndexReconciler) fail(ctx context.Context, idx *mongodbv1beta1.MongoDBSearchIndex, msg string) (ctrl.Result, error) {
	_ = r.applyStatus(ctx, idx, siPhaseFailed, idx.Status.IndexID, false, msg)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// applyStatus — Phase/IndexID/Queryable/Error/ObservedGeneration + Ready condition 설정.
func (r *MongoDBSearchIndexReconciler) applyStatus(ctx context.Context, idx *mongodbv1beta1.MongoDBSearchIndex, phase, indexID string, queryable bool, errMsg string) error {
	apply := func() {
		idx.Status.Phase = phase
		idx.Status.IndexID = indexID
		idx.Status.Queryable = queryable
		idx.Status.ObservedGeneration = idx.Generation
		idx.Status.Error = errMsg
		readyStatus := metav1.ConditionFalse
		if phase == siPhaseReady {
			readyStatus = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&idx.Status.Conditions, metav1.Condition{
			Type: siConditionReady, Status: readyStatus, ObservedGeneration: idx.Generation,
			Reason: "Phase" + phase, Message: phaseMessage(phase, errMsg),
		})
	}
	apply()
	return commonsstatus.UpdateWithRetry(ctx, r.Client, idx, apply)
}

func phaseMessage(phase, errMsg string) string {
	if errMsg != "" {
		return errMsg
	}
	return "search index phase=" + phase
}

func (r *MongoDBSearchIndexReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1beta1.MongoDBSearchIndex{}).
		Complete(r)
}
