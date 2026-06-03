package controller

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongoclient "github.com/keiailab/mongodb-operator/internal/mongodb"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

// docCounter는 복원 대상에 연결해 컬렉션 문서 수를 세는 검증 인터페이스다.
// 프로덕션은 실제 mongo 연결(mongoDocCounter), 테스트는 mock을 주입한다.
type docCounter interface {
	// Count는 db.coll 에서 filterJSON(extended JSON) 에 매칭되는 문서 수를 반환.
	Count(ctx context.Context, db, coll, filterJSON string) (int64, error)
	Close(ctx context.Context)
}

type MongoDBBackupVerificationReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// NewDocCounter는 restore 대상 connectionString 에 연결하는 counter 를 만든다.
	// nil 이면 실제 mongo 연결(defaultDocCounter)을 사용 — 테스트에서 mock 주입용.
	NewDocCounter func(ctx context.Context, connStr string) (docCounter, error)
}

// mongoDocCounter는 실제 mongo 연결 기반 docCounter 구현.
type mongoDocCounter struct {
	client *mongo.Client
}

func (m *mongoDocCounter) Count(ctx context.Context, db, coll, filterJSON string) (int64, error) {
	if filterJSON == "" {
		filterJSON = "{}"
	}
	var filter bson.M
	if err := bson.UnmarshalExtJSON([]byte(filterJSON), true, &filter); err != nil {
		return 0, fmt.Errorf("filterJSON %q 파싱 실패: %w", filterJSON, err)
	}
	return m.client.Database(db).Collection(coll).CountDocuments(ctx, filter)
}

func (m *mongoDocCounter) Close(ctx context.Context) {
	_ = m.client.Disconnect(ctx)
}

// defaultDocCounter는 connectionString URI 로 실제 mongo 연결을 만든다.
func defaultDocCounter(ctx context.Context, connStr string) (docCounter, error) {
	cli, err := mongoclient.NewClientFromURI(ctx, connStr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return &mongoDocCounter{client: cli}, nil
}

// readRestoreConnString은 restore 가 복원한 대상의 connectionString 을
// <backupRef>-backup-uri secret 에서 읽는다 (restore job 과 동일 출처).
func (r *MongoDBBackupVerificationReconciler) readRestoreConnString(
	ctx context.Context, v *mongodbv1alpha1.MongoDBBackupVerification,
) (string, error) {
	secret := &corev1.Secret{}
	name := v.Spec.BackupRef.Name + "-backup-uri"
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: v.Namespace}, secret); err != nil {
		return "", err
	}
	connStr := string(secret.Data["connectionString"])
	if connStr == "" {
		return "", fmt.Errorf("secret %s 에 connectionString 없음", name)
	}
	return connStr, nil
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackupverifications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackupverifications/status,verbs=get;update;patch

func (r *MongoDBBackupVerificationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("verification", req.NamespacedName)

	v := &mongodbv1alpha1.MongoDBBackupVerification{}
	if err := r.Get(ctx, req.NamespacedName, v); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	switch v.Status.Phase {
	case "", "Pending":
		backup := &mongodbv1alpha1.MongoDBBackup{}
		if err := r.Get(ctx, types.NamespacedName{
			Name: v.Spec.BackupRef.Name, Namespace: v.Namespace,
		}, backup); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("backup not found", "backup", v.Spec.BackupRef.Name)
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		if backup.Status.Phase != backupPhaseCompleted {
			logger.Info("backup not completed yet", "phase", backup.Status.Phase)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		now := metav1.Now()
		v.Status.Phase = backupPhaseRestoring
		v.Status.StartTime = &now

		// Queryable backup (ROADMAP §3.1.1): Spec.Queryable 활성 시 장기 read-only
		// mongod StatefulSet 을 생성하여 백업을 ad-hoc 조회 가능하게 한다. owner-ref
		// → verification CR GC. BuildQueryableStatefulSet 은 nil/disabled 시 nil 반환.
		// 데이터 복원 drill (mongorestore → instance) 은 후속 운영 강화 (deferred).
		if sts := resources.BuildQueryableStatefulSet(backup, v.Spec.Queryable); sts != nil {
			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
				return controllerutil.SetControllerReference(v, sts, r.Scheme)
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("create queryable statefulset: %w", err)
			}
			v.Status.QueryableInstance = sts.Name
			logger.Info("queryable read-only instance 생성 (복원 drill deferred)", "statefulset", sts.Name)
		}

		if err := r.Status().Update(ctx, v); err != nil {
			return ctrl.Result{}, err
		}

		restoreJob, err := resources.BuildRestoreJob(backup, backup.Name+"-backup-uri")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("build restore job: %w", err)
		}
		restoreJob.Name = v.Name + "-verify-restore"
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, restoreJob, func() error {
			return controllerutil.SetControllerReference(v, restoreJob, r.Scheme)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("create restore job: %w", err)
		}

		logger.Info("restore job created for verification", "job", restoreJob.Name)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

	case "Restoring":
		jobName := v.Name + "-verify-restore"
		job := &batchv1.Job{}
		if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: v.Namespace}, job); err != nil {
			// Job 부재(NotFound)는 아직 생성 전파 중일 수 있으므로 재큐만,
			// 그 외 일시적 API 오류는 error 를 반환해 backoff/관찰 가능하게 한다.
			if apierrors.IsNotFound(err) {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}

		for _, c := range job.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				v.Status.Phase = "Verifying"
				if err := r.Status().Update(ctx, v); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{Requeue: true}, nil
			}
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				now := metav1.Now()
				v.Status.Phase = backupPhaseFailed
				v.Status.CompletionTime = &now
				v.Status.QueryResults = append(v.Status.QueryResults, mongodbv1alpha1.VerificationQueryResult{
					DB: "restore", Collection: "job", DocCount: 0,
				})
				if err := r.Status().Update(ctx, v); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

	case "Verifying":
		// restore 가 복원한 대상에 실제로 연결해 SampleQueries 를 실행하고,
		// 각 쿼리의 문서 수가 MinExpectedDocs 이상인지 검증한다(실 검증).
		connStr, err := r.readRestoreConnString(ctx, v)
		if err != nil {
			// secret 전파 지연(NotFound)은 재큐, 그 외는 error 로 표면화.
			if apierrors.IsNotFound(err) {
				return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
			}
			return ctrl.Result{}, fmt.Errorf("restore connectionString 조회: %w", err)
		}

		newCounter := r.NewDocCounter
		if newCounter == nil {
			newCounter = defaultDocCounter
		}
		counter, err := newCounter(ctx, connStr)
		if err != nil {
			// 복원 대상이 아직 준비 안 됐을 수 있으므로 재큐(일시 연결 실패).
			logger.Info("verify 대상 연결 실패 — 재시도", "error", err.Error())
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		defer counter.Close(ctx)

		// SampleQueries 미지정 시 기본 sanity 검증(admin.system.version 1+).
		queries := v.Spec.SampleQueries
		if len(queries) == 0 {
			queries = []mongodbv1alpha1.VerificationQuery{
				{DB: "admin", Collection: "system.version", FilterJSON: "{}", MinExpectedDocs: 1},
			}
		}

		now := metav1.Now()
		allPassed := true
		results := make([]mongodbv1alpha1.VerificationQueryResult, 0, len(queries))
		for _, q := range queries {
			count, qErr := counter.Count(ctx, q.DB, q.Collection, q.FilterJSON)
			passed := qErr == nil && count >= q.MinExpectedDocs
			if !passed {
				allPassed = false
				if qErr != nil {
					logger.Info("verify 쿼리 실패", "db", q.DB, "collection", q.Collection, "error", qErr.Error())
				}
			}
			results = append(results, mongodbv1alpha1.VerificationQueryResult{
				DB: q.DB, Collection: q.Collection,
				DocCount: count, Passed: passed,
			})
		}
		v.Status.QueryResults = append(v.Status.QueryResults, results...)

		if allPassed {
			v.Status.Phase = "Passed"
		} else {
			v.Status.Phase = backupPhaseFailed
		}
		v.Status.CompletionTime = &now
		if err := r.Status().Update(ctx, v); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("verification completed", "phase", v.Status.Phase, "queries", len(results))
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *MongoDBBackupVerificationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBBackupVerification{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
