/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// builder_backup_test.go — backup / restore Job 빌더 회귀 가드.
//
// 여기 있는 대부분은 "PITR 은 이름뿐이고 실제 복원은 불가능하다" 던 상태의
// 근본 결함들을 못 박는 테스트다. 각 테스트 주석의 "구 동작" 이 그 결함이다.

package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// envOf 는 컨테이너 env 에서 이름으로 값을 찾는다. 없으면 ("", false).
func envOf(c corev1.Container, name string) (string, bool) {
	for _, e := range c.Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

// s3Storage 는 테스트용 S3 backup storage spec.
func s3Storage() mongodbv1alpha1.BackupStorageSpec {
	return mongodbv1alpha1.BackupStorageSpec{
		Type: "s3",
		S3: &mongodbv1alpha1.S3StorageSpec{
			Bucket:         "mongo-backups",
			Endpoint:       "https://rgw.example.com",
			Region:         "us-east-1",
			Prefix:         "prod/",
			CredentialsRef: corev1.LocalObjectReference{Name: "s3-creds"},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// BuildBackupJob
// ─────────────────────────────────────────────────────────────────────────────

// TestBuildBackupJob_BackupNameEnv — 구 동작: 스크립트가 컨테이너 안에서
// "<cluster>-$(date +%Y%m%d-%H%M%S)" 로 이름을 지어 operator 도 restore 도
// 실제 S3 키/덤프 경로를 알 수 없었다 (= 복원 불가의 근본 원인).
func TestBuildBackupJob_BackupNameEnv(t *testing.T) {
	backup := &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly-1", Namespace: "data"},
		Spec: mongodbv1alpha1.MongoDBBackupSpec{
			ClusterRef: mongodbv1alpha1.ClusterReference{Name: "rs0", Kind: "MongoDB"},
			Storage:    s3Storage(),
		},
	}
	job := BuildBackupJob(backup, "rs0-auth")
	require.Len(t, job.Spec.Template.Spec.Containers, 1)
	c := job.Spec.Template.Spec.Containers[0]

	got, ok := envOf(c, envBackupName)
	require.True(t, ok, "BACKUP_NAME env 필수 — 스크립트가 키를 짓는 근거")
	assert.Equal(t, "nightly-1", got, "BACKUP_NAME = MongoDBBackup CR 이름")
}

// TestBuildBackupJob_PVCVolumeMounted — 구 동작: backup-pvc.sh.tpl 는 /backup 에
// 덤프를 쓰는데 Job 에 volume 이 없어 컨테이너 임시 FS 로 갔다 → Job 종료와
// 함께 증발하면서 성공을 보고했다 (백업이 남지 않는 백업).
func TestBuildBackupJob_PVCVolumeMounted(t *testing.T) {
	backup := &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-backup", Namespace: "data"},
		Spec: mongodbv1alpha1.MongoDBBackupSpec{
			ClusterRef: mongodbv1alpha1.ClusterReference{Name: "rs0", Kind: "MongoDB"},
			Storage:    mongodbv1alpha1.BackupStorageSpec{Type: "pvc"},
		},
	}
	job := BuildBackupJob(backup, "rs0-auth")

	require.Len(t, job.Spec.Template.Spec.Volumes, 1, "PVC 백업은 backup volume 필수")
	vol := job.Spec.Template.Spec.Volumes[0]
	require.NotNil(t, vol.PersistentVolumeClaim)
	assert.Equal(t, "pvc-backup", vol.PersistentVolumeClaim.ClaimName,
		"ClaimName = backup CR 이름 (BuildRestoreJob 의 SourceBackupName 과 같은 규약)")

	c := job.Spec.Template.Spec.Containers[0]
	require.Len(t, c.VolumeMounts, 1)
	assert.Equal(t, backupPVCMount, c.VolumeMounts[0].MountPath,
		"스크립트가 쓰는 /backup 에 마운트돼야 한다")
	assert.False(t, c.VolumeMounts[0].ReadOnly, "백업은 써야 한다")
}

// TestBuildBackupJob_S3NoPVCVolume — S3 백업은 스트리밍 업로드라 로컬 볼륨이
// 필요 없다 (있으면 쓸데없는 PVC 를 요구해 Job 이 Pending 에 걸린다).
func TestBuildBackupJob_S3NoPVCVolume(t *testing.T) {
	backup := &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "s3-backup", Namespace: "data"},
		Spec: mongodbv1alpha1.MongoDBBackupSpec{
			ClusterRef: mongodbv1alpha1.ClusterReference{Name: "rs0", Kind: "MongoDB"},
			Storage:    s3Storage(),
		},
	}
	job := BuildBackupJob(backup, "rs0-auth")
	assert.Empty(t, job.Spec.Template.Spec.Volumes, "S3 백업은 볼륨 불필요")

	c := job.Spec.Template.Spec.Containers[0]
	bucket, ok := envOf(c, "S3_BUCKET")
	require.True(t, ok)
	assert.Equal(t, "mongo-backups", bucket)
	prefix, ok := envOf(c, "S3_PREFIX")
	require.True(t, ok)
	assert.Equal(t, "prod/", prefix)
}

// ─────────────────────────────────────────────────────────────────────────────
// buildBackupScript — 플래그 선택
// ─────────────────────────────────────────────────────────────────────────────

// TestBuildBackupScript_AlwaysGzip — 구 동작: CompressionType=zstd (그런데 이게
// CRD **기본값**이다) 면 compressionFlag 가 "--archive" 가 되어
//   - s3 : `mongodump --archive --archive` → 압축 없이 `.gz` 이름으로 업로드 →
//     restore 의 --gzip 이 "invalid header" 로 반드시 깨졌다
//   - pvc: `mongodump --out=... --archive` → 두 옵션 배타라 mongodump 가 거부
//
// 즉 기본 CR 로는 S3 복원도 PVC 백업도 성립하지 않았다. mongodump 의 압축
// 코덱은 gzip 뿐이라 항상 --gzip 이어야 한다.
func TestBuildBackupScript_AlwaysGzip(t *testing.T) {
	for _, ct := range []string{"zstd", "snappy", "gzip", ""} {
		t.Run("compressionType="+ct, func(t *testing.T) {
			backup := &mongodbv1alpha1.MongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "data"},
				Spec: mongodbv1alpha1.MongoDBBackupSpec{
					ClusterRef:      mongodbv1alpha1.ClusterReference{Name: "rs0", Kind: "MongoDB"},
					Storage:         s3Storage(),
					CompressionType: ct,
				},
			}
			script := buildBackupScript(backup)
			assert.Contains(t, script, `mongodump --uri="${MONGODB_URI}" --gzip --oplog --archive`,
				"mongodump 는 gzip 코덱만 지원 — 항상 --gzip")
			assert.NotContains(t, script, "--archive --archive",
				"중복 --archive = 압축 없이 .gz 이름으로 업로드 → restore 파탄")
		})
	}
}

// TestBuildBackupScript_OplogByClusterKind — --oplog 는 dump 중 write 를 캡처해
// *시점 일관* 스냅샷을 만든다 (PITR 기점). 다만 sharded 의 URI 는 mongos 를
// 가리키고 mongos 에는 local.oplog.rs 가 없어 mongodump 가 거부한다.
func TestBuildBackupScript_OplogByClusterKind(t *testing.T) {
	t.Run("ReplicaSet → --oplog", func(t *testing.T) {
		backup := &mongodbv1alpha1.MongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "data"},
			Spec: mongodbv1alpha1.MongoDBBackupSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "rs0", Kind: "MongoDB"},
				Storage:    s3Storage(),
			},
		}
		assert.True(t, backupWithOplog(backup))
		assert.Contains(t, buildBackupScript(backup), "--oplog --archive")
	})

	t.Run("Sharded → --oplog 없음 (mongos 에는 oplog 가 없다)", func(t *testing.T) {
		backup := &mongodbv1alpha1.MongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "data"},
			Spec: mongodbv1alpha1.MongoDBBackupSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "sh0", Kind: clusterKindSharded},
				Storage:    s3Storage(),
			},
		}
		assert.False(t, backupWithOplog(backup))
		assert.Contains(t, buildBackupScript(backup), `--gzip --archive`)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// BuildRestoreJob
// ─────────────────────────────────────────────────────────────────────────────

func restoreCR(name string, storage mongodbv1alpha1.BackupStorageSpec, r *mongodbv1alpha1.RestoreSpec) *mongodbv1alpha1.MongoDBBackup {
	return &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "data"},
		Spec: mongodbv1alpha1.MongoDBBackupSpec{
			ClusterRef: mongodbv1alpha1.ClusterReference{Name: "rs0", Kind: "MongoDB"},
			Storage:    storage,
			Restore:    r,
		},
	}
}

// TestBuildRestoreJob_S3TwoStage — 구 동작: restore 는 PVC 소스 전용이었고
// S3 fetch 가 아예 없었다. 게다가 존재한 적 없는 /data/source/dump.archive 를
// 읽었다 (백업은 <name>.archive.gz 로 올렸다).
func TestBuildRestoreJob_S3TwoStage(t *testing.T) {
	pit := metav1.Time{Time: time.Unix(1752710400, 0).UTC()}
	backup := restoreCR("r-s3", s3Storage(), &mongodbv1alpha1.RestoreSpec{
		SourceBackupName: "nightly-1",
		PointInTime:      &pit,
	})

	job, err := BuildRestoreJob(backup, "rs0-auth")
	require.NoError(t, err)

	// init container = fetch
	require.Len(t, job.Spec.Template.Spec.InitContainers, 1, "S3 restore 는 fetch init 필수")
	fetch := job.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, "fetch", fetch.Name)
	assert.Equal(t, awsCLIImage, fetch.Image,
		"mongo 이미지에는 aws CLI 가 없고 restore pod 는 non-root 라 런타임 설치도 불가")
	assert.NotContains(t, fetch.Image, ":latest", ":latest 금지 — 이미지 불변성")
	assert.Equal(t, buildDefaultContainerSecurityContext(), fetch.SecurityContext,
		"fetch 도 PSA restricted 를 지켜야 한다")

	// fetch 는 /data/source 에 *써야* 한다.
	require.Len(t, fetch.VolumeMounts, 1)
	assert.Equal(t, restoreSourceMount, fetch.VolumeMounts[0].MountPath)
	assert.False(t, fetch.VolumeMounts[0].ReadOnly, "fetch 는 받아서 써야 한다")

	// fetch 는 S3 접속 정보 + PIT 를 알아야 세그먼트를 고른다.
	bucket, ok := envOf(fetch, "S3_BUCKET")
	require.True(t, ok, "fetch 에 S3 env 필수")
	assert.Equal(t, "mongo-backups", bucket)
	src, ok := envOf(fetch, envSourceBackup)
	require.True(t, ok)
	assert.Equal(t, "nightly-1", src)
	limit, ok := envOf(fetch, envOplogLimit)
	require.True(t, ok, "PIT 지정 시 fetch 가 OPLOG_LIMIT 을 알아야 세그먼트를 고른다")
	assert.Equal(t, "1752710401:0", limit, "PIT 초를 포함 → epoch+1:0 (limit 은 배타)")

	// source volume = EmptyDir (Ceph RBD RWO 라 PVC 를 두 pod 가 못 나눠 쓴다.
	// 같은 pod 안 init ↔ main 공유면 EmptyDir 로 충분하다.)
	require.Len(t, job.Spec.Template.Spec.Volumes, 1)
	vol := job.Spec.Template.Spec.Volumes[0]
	require.NotNil(t, vol.EmptyDir, "S3 restore 의 source 는 EmptyDir")
	assert.Nil(t, vol.EmptyDir.SizeLimit,
		"sizeLimit 을 임의로 잡으면 큰 DB 복원이 evict 로 조용히 깨진다")

	// main = replay
	require.Len(t, job.Spec.Template.Spec.Containers, 1)
	main := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "mongorestore", main.Name)
	assert.True(t, main.VolumeMounts[0].ReadOnly, "main 은 읽기만")
	mlimit, ok := envOf(main, envOplogLimit)
	require.True(t, ok)
	assert.Equal(t, "1752710401:0", mlimit)
	st, ok := envOf(main, envStorageType)
	require.True(t, ok)
	assert.Equal(t, "s3", st)
}

// TestBuildRestoreJob_PVCNoInitContainer — PVC 소스는 받아올 것이 없다.
// 기존 동작(단일 volume = 소스 PVC) 보존.
func TestBuildRestoreJob_PVCNoInitContainer(t *testing.T) {
	backup := restoreCR("r-pvc", mongodbv1alpha1.BackupStorageSpec{Type: "pvc"},
		&mongodbv1alpha1.RestoreSpec{SourceBackupName: "src-backup"})

	job, err := BuildRestoreJob(backup, "rs0-auth")
	require.NoError(t, err)
	assert.Empty(t, job.Spec.Template.Spec.InitContainers, "PVC 소스는 fetch 불필요")

	require.Len(t, job.Spec.Template.Spec.Volumes, 1)
	pvc := job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim
	require.NotNil(t, pvc)
	assert.Equal(t, "src-backup", pvc.ClaimName)

	main := job.Spec.Template.Spec.Containers[0]
	st, ok := envOf(main, envStorageType)
	require.True(t, ok)
	assert.Equal(t, "pvc", st)
	_, ok = envOf(main, envOplogLimit)
	assert.False(t, ok, "PIT 미지정 → OPLOG_LIMIT env 없음 (base 시점 복원)")
}

// TestBuildRestoreJob_PVCWithPITRejected — oplog 세그먼트는 S3 에만 있다.
// PVC + PIT 를 받아들이면 Job 을 띄워놓고 "PITR 인 척" 하다 base 시점으로
// 복원해버린다 (조용한 요청 위반) → 빌드 시점에 거절한다.
func TestBuildRestoreJob_PVCWithPITRejected(t *testing.T) {
	pit := metav1.Time{Time: time.Unix(1752710400, 0).UTC()}
	backup := restoreCR("r-pvc-pit", mongodbv1alpha1.BackupStorageSpec{Type: "pvc"},
		&mongodbv1alpha1.RestoreSpec{SourceBackupName: "src", PointInTime: &pit})

	job, err := BuildRestoreJob(backup, "rs0-auth")
	require.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "storage.type=s3 전용")
}

// TestBuildRestoreJob_S3WithoutS3SpecRejected — type=s3 인데 s3 블록이 비면
// 접속 정보가 없어 fetch 가 반드시 실패한다. 미리 거절한다.
func TestBuildRestoreJob_S3WithoutS3SpecRejected(t *testing.T) {
	backup := restoreCR("r-bad", mongodbv1alpha1.BackupStorageSpec{Type: "s3"},
		&mongodbv1alpha1.RestoreSpec{SourceBackupName: "src"})

	job, err := BuildRestoreJob(backup, "rs0-auth")
	require.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "storage.s3")
}

// TestBuildRestoreJob_InvalidPITTimestampRejected — 잘못된 ts 로 Job 을 띄우면
// mongorestore 가 이상한 지점에서 끊는다. Go 에서 먼저 막는다.
func TestBuildRestoreJob_InvalidPITTimestampRejected(t *testing.T) {
	bad := "not-a-timestamp"
	backup := restoreCR("r-bad-ts", s3Storage(),
		&mongodbv1alpha1.RestoreSpec{SourceBackupName: "src", PointInTimeTimestamp: &bad})

	job, err := BuildRestoreJob(backup, "rs0-auth")
	require.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "pointInTimeTimestamp")
}

// TestBuildRestoreJob_PITTimestampWins — 초 내 순번까지 지정한 경우 그대로
// 전달돼야 한다 (구 동작은 ordinal 을 0 으로 고정해 그 초 전체를 잘랐다).
func TestBuildRestoreJob_PITTimestampWins(t *testing.T) {
	pit := metav1.Time{Time: time.Unix(1752710400, 0).UTC()}
	exact := "1752710400:7"
	backup := restoreCR("r-exact", s3Storage(), &mongodbv1alpha1.RestoreSpec{
		SourceBackupName:     "src",
		PointInTime:          &pit,
		PointInTimeTimestamp: &exact,
	})

	job, err := BuildRestoreJob(backup, "rs0-auth")
	require.NoError(t, err)
	main := job.Spec.Template.Spec.Containers[0]
	limit, ok := envOf(main, envOplogLimit)
	require.True(t, ok)
	assert.Equal(t, "1752710400:7", limit, "PointInTimeTimestamp 가 PointInTime 을 이긴다")
}

// TestBuildRestoreJob_NoDateInScript — 구 동작: restore 스크립트가
// `date -u -d "${POINT_IN_TIME}" +%s` 로 epoch 를 계산하고 ordinal 을 0 으로
// 고정했다. GNU date 의존 + 그 초 전체 절단 + 정밀도 상실.
func TestBuildRestoreJob_NoDateInScript(t *testing.T) {
	pit := metav1.Time{Time: time.Unix(1752710400, 0).UTC()}
	backup := restoreCR("r", s3Storage(), &mongodbv1alpha1.RestoreSpec{
		SourceBackupName: "src", PointInTime: &pit,
	})
	job, err := BuildRestoreJob(backup, "rs0-auth")
	require.NoError(t, err)

	main := job.Spec.Template.Spec.Containers[0]
	require.Len(t, main.Command, 3)
	script := main.Command[2]
	assert.NotContains(t, script, "date -u -d", "ts 계산은 Go(OplogLimitArg) 책임")
	assert.NotContains(t, script, "POINT_IN_TIME", "구 env 는 OPLOG_LIMIT 으로 대체됐다")
	assert.NotContains(t, script, "dump.archive", "존재한 적 없는 파일명")

	_, ok := envOf(main, "POINT_IN_TIME")
	assert.False(t, ok, "POINT_IN_TIME env 는 사라져야 한다")
}
