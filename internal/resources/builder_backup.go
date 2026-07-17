/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonsbatchjob "github.com/keiailab/keiailab-commons/pkg/batchjob"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/assets"
)

const (
	// storageTypeS3 는 S3 (호환 포함 — Ceph RGW 등) backup storage type.
	storageTypeS3 = "s3"

	// clusterKindSharded 는 ClusterReference.Kind 의 sharded 값.
	// sharded 는 MONGODB_URI 가 mongos 를 가리켜 local.oplog.rs 가 없다 →
	// mongodump --oplog 불가 → PITR 기점을 만들 수 없다 (RS 전용 제약).
	clusterKindSharded = "MongoDBSharded"

	// restoreSourceMount 는 restore Job 이 base 아카이브/덤프 + 연접된
	// oplog.bson 을 읽는 경로. S3 면 init container 가 채우는 EmptyDir,
	// PVC 면 backup PVC 가 여기 마운트된다.
	restoreSourceMount = "/data/source"

	// restoreSourceVolume 은 위 volume 이름.
	restoreSourceVolume = "source"

	// backupPVCMount 는 PVC 백업이 덤프를 떨어뜨리는 경로 (backup-pvc.sh.tpl 계약).
	backupPVCMount = "/backup"

	// binBash 는 backup/restore/oplog Job 컨테이너의 shell entrypoint.
	// 스크립트가 bash 전용 문법(pipefail 등)을 쓰므로 /bin/sh 아님.
	binBash = "/bin/bash"

	// backupPVCVolume 은 위 volume 이름.
	backupPVCVolume = "backup"

	// awsCLIImage 는 restore Job 의 fetch init container 이미지.
	//
	// mongo 이미지에는 aws CLI 가 없다. backup Job 은 root 라 런타임 apt-get
	// 으로 때우지만 restore pod 는 pod-level SecurityContext 가 non-root(999)
	// 라 그 경로가 막힌다. fetch(aws) 와 replay(mongorestore) 는 파이프가 아닌
	// *순차* 단계라 컨테이너를 나눌 수 있고, 나누면 각자 필요한 것만 든 이미지를
	// 쓰면서 PSA restricted 를 유지할 수 있다.
	//
	// keyfileInitImage(busybox) 와 동일한 관리 규약 — CVE 패치 시 본 const 만
	// 갱신한다. :latest 금지(이미지 불변성).
	awsCLIImage = "amazon/aws-cli:2.36.1"

	// envBackupName 은 백업 이름을 스크립트에 넘기는 env. 값은 MongoDBBackup
	// CR 이름이다 — 구 구현은 컨테이너 안에서 $(date) 로 지어 operator 도
	// restore 도 실제 S3 키/덤프 경로를 알 수 없었다 (복원 불가의 근본 원인).
	envBackupName = "BACKUP_NAME"

	// envSourceBackup / envStorageType / envOplogLimit 은 restore Job 계약.
	envSourceBackup = "SOURCE_BACKUP"
	envStorageType  = "STORAGE_TYPE"
	envOplogLimit   = "OPLOG_LIMIT"
)

// isS3Storage 는 storage 가 S3 인지 판정. Type 만 보는 곳과 S3 != nil 까지
// 보는 곳이 갈리면 "s3 스크립트인데 S3 env 가 없는" 조합이 생기므로 단일화한다.
func isS3Storage(storage mongodbv1alpha1.BackupStorageSpec) bool {
	return storage.Type == storageTypeS3 && storage.S3 != nil
}

// buildS3EnvVars 는 backup / restore Job 공용 S3 접속 env 를 반환.
// 변수명 6종 + Secret 키(access-key / secret-key)가 스크립트와의 계약이다.
func buildS3EnvVars(s3 *mongodbv1alpha1.S3StorageSpec) []corev1.EnvVar {
	if s3 == nil {
		return nil
	}
	return []corev1.EnvVar{
		{Name: "S3_BUCKET", Value: s3.Bucket},
		{Name: "S3_ENDPOINT", Value: s3.Endpoint},
		{Name: "S3_REGION", Value: s3.Region},
		{Name: "S3_PREFIX", Value: s3.Prefix},
		{
			Name: "AWS_ACCESS_KEY_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: s3.CredentialsRef,
					Key:                  "access-key",
				},
			},
		},
		{
			Name: "AWS_SECRET_ACCESS_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: s3.CredentialsRef,
					Key:                  "secret-key",
				},
			},
		},
	}
}

// buildMongoDBURIEnv 는 auth Secret 의 connectionString 을 MONGODB_URI 로 노출.
func buildMongoDBURIEnv(authSecretName string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: envMongoDBURI,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: authSecretName,
				},
				Key: "connectionString",
			},
		},
	}
}

// BuildBackupJob creates a Job for MongoDB backup
func BuildBackupJob(backup *mongodbv1alpha1.MongoDBBackup, authSecretName string) *batchv1.Job {
	labels := buildLabels(backup.Name, "backup")

	backoff := int32(3)
	ttl := int32(86400) // 24 hours

	envVars := []corev1.EnvVar{
		buildMongoDBURIEnv(authSecretName),
		// 스크립트가 S3 키 / 덤프 디렉터리를 결정론적으로 짓는 근거.
		{Name: envBackupName, Value: backup.Name},
	}

	// S3 storage configuration
	if isS3Storage(backup.Spec.Storage) {
		envVars = append(envVars, buildS3EnvVars(backup.Spec.Storage.S3)...)
	}

	// Build backup script
	script := buildBackupScript(backup)

	// PVC 백업은 /backup 에 덤프를 쓴다 — volume 이 없으면 컨테이너 임시 FS 에
	// 쓰고 Job 종료와 함께 증발한다 (= 백업이 남지 않는데 성공을 보고).
	// PVC 이름은 backup CR 이름 — BuildRestoreJob 의 ClaimName 과 같은 규약.
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	if !isS3Storage(backup.Spec.Storage) {
		volumes = []corev1.Volume{{
			Name: backupPVCVolume,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: backup.Name,
				},
			},
		}}
		mounts = []corev1.VolumeMount{{Name: backupPVCVolume, MountPath: backupPVCMount}}
	}

	// Job 엔벨로프는 keiailab-commons/pkg/batchjob.Build 에 위임. 컨테이너(mongodump
	// 스크립트) 조립은 mongo 도메인 잔류.
	return commonsbatchjob.Build(commonsbatchjob.Params{
		Name:                    backup.Name,
		Namespace:               backup.Namespace,
		Labels:                  labels,
		BackoffLimit:            &backoff,
		TTLSecondsAfterFinished: &ttl,
		Containers: []corev1.Container{
			{
				Name:         "backup",
				Image:        defaultImage,
				Command:      []string{binBash, "-c"},
				Args:         []string{script},
				Env:          envVars,
				VolumeMounts: mounts,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
		},
		Volumes: volumes,
	})
}

// BuildBackupCronJob creates a CronJob that periodically creates MongoDBBackup CRs.
func BuildBackupCronJob(clusterName, namespace, schedule, clusterKind string, backupSpec mongodbv1alpha1.BackupSpec) *batchv1.CronJob {
	labels := buildLabels(clusterName, "backup-scheduler")
	historyLimit := int32(3)
	failedLimit := int32(1)

	backupName := fmt.Sprintf("%s-scheduled-$(date +%%Y%%m%%d-%%H%%M%%S)", clusterName)
	storageType := "pvc"
	if backupSpec.Storage.Type != "" {
		storageType = backupSpec.Storage.Type
	}

	script := fmt.Sprintf(`#!/bin/sh
set -e
BACKUP_NAME="%s-scheduled-$(date +%%Y%%m%%d-%%H%%M%%S)"
cat <<MANIFEST | kubectl apply -f -
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: ${BACKUP_NAME}
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: backup-scheduler
    app.kubernetes.io/instance: %s
spec:
  clusterRef:
    name: %s
    kind: %s
  type: full
  compression: true
  storage:
    type: %s
MANIFEST
echo "Created backup ${BACKUP_NAME}"
`, clusterName, namespace, clusterName, clusterName, clusterKind, storageType)

	_ = backupName // used in script template above

	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName + "-backup-schedule",
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   schedule,
			SuccessfulJobsHistoryLimit: &historyLimit,
			FailedJobsHistoryLimit:     &failedLimit,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy:      corev1.RestartPolicyOnFailure,
							ServiceAccountName: clusterName + "-backup-scheduler",
							Containers: []corev1.Container{
								{
									Name:    "scheduler",
									Image:   "registry.k8s.io/kubectl:v1.31.0",
									Command: []string{"/bin/sh", "-c"},
									Args:    []string{script},
									// 결함 #2 sister / 결함 #4: scheduler 컨테이너도
									// PSA restricted 충족 (Bitnami 이미지 교체와 함께).
									SecurityContext: buildDefaultContainerSecurityContext(),
								},
							},
							SecurityContext: buildDefaultSecurityContext(),
						},
					},
				},
			},
		},
	}
}

// backupWithOplog 는 이 백업을 `mongodump --oplog` 로 떠야 하는지 판정한다.
//
// --oplog 는 dump 중 들어온 write 를 함께 캡처해 *시점 일관* 스냅샷을 만든다
// (그리고 그것이 PITR 의 기점이다). 다만 oplog 를 가진 ReplicaSet 멤버 접속에서만
// 유효하다 — sharded 의 MONGODB_URI 는 mongos 를 가리키고 mongos 에는
// local.oplog.rs 가 없어 mongodump 가 거부한다.
//
// PITREnabled 로 게이팅하지 않는 이유: --oplog 없는 덤프는 애초에 crash-consistent
// 하지 않다(dump 중 write 가 반쯤 섞인다). RS 라면 PITR 을 안 쓰더라도 항상 켜는
// 것이 옳고, 부담도 dump 구간 oplog 뿐이다. 게다가 PITREnabled 는 클러스터 CR 의
// BackupSpec 에 있어 MongoDBBackup CR 만 받는 여기서는 보이지도 않는다.
func backupWithOplog(backup *mongodbv1alpha1.MongoDBBackup) bool {
	return backup.Spec.ClusterRef.Kind != clusterKindSharded
}

func buildBackupScript(backup *mongodbv1alpha1.MongoDBBackup) string {
	// mongodump 의 압축 코덱은 **gzip 뿐**이다 (zstd / snappy 플래그가 없다).
	// 구 구현은 CompressionType=zstd — 그런데 이게 CRD 기본값이다 — 일 때
	// compressionFlag 를 "--archive" 로 바꿔 다음을 만들었다:
	//   s3 : `mongodump --archive --archive` → 압축 없이 `.archive.gz` 이름으로
	//        업로드 → restore 의 --gzip 이 "invalid header" 로 반드시 깨진다.
	//   pvc: `mongodump --out=... --archive` → 두 옵션 배타라 mongodump 가 거부.
	// 즉 기본 CR 로는 S3 복원도 PVC 백업도 성립하지 않았다. 코덱 현실에 맞춰
	// 항상 gzip 을 쓴다 — Spec.CompressionType 의 zstd/snappy 는 표현할 수단이
	// 없다(API 표면 자체의 문제라 webhook/API 트랙 몫으로 남긴다).
	compressionFlag := "--gzip"
	// S3 변형은 mongodump --archive를 stdin으로 piping해 stdout에 쓴 뒤 aws s3 cp -.
	// PVC 변형은 --out으로 directory에 직접 출력. assets/scripts/backup-{s3,pvc}.sh.tpl 분기.
	out, err := assets.RenderBackup(
		backup.Spec.Storage.Type,
		backup.Spec.ClusterRef.Name,
		compressionFlag,
		backupWithOplog(backup),
	)
	if err != nil {
		panic(fmt.Sprintf("render backup script: %v", err))
	}
	return out
}

// BuildRestoreJob 은 mongorestore Job 을 생성. Spec.Restore 가 nil 이 아닌
// MongoDBBackup CR 에 대해 controller 가 호출한다.
//
// # 구조 (S3 = 2 단계, PVC = 1 단계)
//
// S3 는 init container(fetch) + main(replay) 2 단이다:
//
//	init "fetch"    — restore-fetch.sh.tpl  (amazon/aws-cli 이미지)
//	  base.archive.gz + base.meta.json 다운로드, PIT 지정 시 oplog 세그먼트를
//	  선택·검증·연접해 oplog.bson 구성. gap / PIT 도달 실패는 여기서 죽는다.
//	main "mongorestore" — restore-replay.sh.tpl (mongo 이미지)
//	  base 복원(임베드 oplog replay) → oplog.bson 을 --oplogLimit 까지 replay.
//
// 두 컨테이너는 EmptyDir 을 /data/source 로 공유한다. 스토리지가 Ceph RBD RWO
// 라 PVC 는 두 pod 가 못 나눠 쓰지만, 같은 pod 안의 init ↔ main 공유는
// EmptyDir 로 충분하다. (EmptyDir 에 sizeLimit 을 두지 않는 이유는 volume
// 정의부 주석 참조.)
//
// PVC 는 백업 PVC 를 그대로 /data/source 에 마운트해 main 만 돈다 — 받아올
// 것이 없다. PITR 은 S3 전용이라(oplog 세그먼트가 S3 에만 있다) PVC + PIT
// 조합은 Job 을 띄우지 않고 여기서 거절한다.
func BuildRestoreJob(backup *mongodbv1alpha1.MongoDBBackup, authSecretName string) (*batchv1.Job, error) {
	// 결함 #1: 일반 백업 (Spec.Restore=nil) verify 시 nil 역참조 panic 차단.
	// 본 함수는 restore 작업 (Spec.Restore != nil) 에 대해서만 호출되어야 한다.
	if backup.Spec.Restore == nil {
		return nil, fmt.Errorf("backup %s: Spec.Restore is nil", backup.Name)
	}

	// PIT → --oplogLimit 변환. 둘 다 미지정이면 "" (base 시점 복원).
	oplogLimit, err := OplogLimitArg(backup.Spec.Restore.PointInTime, backup.Spec.Restore.PointInTimeTimestamp)
	if err != nil {
		return nil, fmt.Errorf("backup %s: %w", backup.Name, err)
	}

	s3Source := isS3Storage(backup.Spec.Storage)
	if backup.Spec.Storage.Type == storageTypeS3 && backup.Spec.Storage.S3 == nil {
		return nil, fmt.Errorf("backup %s: storage.type=s3 인데 storage.s3 가 비었다", backup.Name)
	}
	if !s3Source && oplogLimit != "" {
		return nil, fmt.Errorf(
			"backup %s: PITR(pointInTime)은 storage.type=s3 전용이다 (oplog 세그먼트가 S3 에만 있다) — 현재 %q",
			backup.Name, backup.Spec.Storage.Type)
	}

	labels := buildLabels(backup.Name, "restore")
	backoff := int32(3)
	ttl := int32(86400)

	storageType := backup.Spec.Storage.Type
	if storageType == "" {
		storageType = "pvc" // RenderBackup 의 "s3 아니면 pvc" fallback 과 동일 규약
	}

	envVars := []corev1.EnvVar{
		buildMongoDBURIEnv(authSecretName),
		{Name: envSourceBackup, Value: backup.Spec.Restore.SourceBackupName},
		{Name: envStorageType, Value: storageType},
	}
	if oplogLimit != "" {
		envVars = append(envVars, corev1.EnvVar{Name: envOplogLimit, Value: oplogLimit})
	}

	replayScript, err := assets.RenderRestoreReplay(restoreSourceMount)
	if err != nil {
		return nil, fmt.Errorf("backup %s: render restore replay script: %w", backup.Name, err)
	}

	job := commonsbatchjob.Build(commonsbatchjob.Params{
		Name:                    backup.Name + "-restore",
		Namespace:               backup.Namespace,
		Labels:                  labels,
		BackoffLimit:            &backoff,
		TTLSecondsAfterFinished: &ttl,
		Containers: []corev1.Container{
			{
				Name:    "mongorestore",
				Image:   getMongoDBImage(mongodbv1alpha1.MongoDBVersion{Version: "8.2"}),
				Command: []string{binBash, "-c", replayScript},
				Env:     envVars,
				VolumeMounts: []corev1.VolumeMount{
					{Name: restoreSourceVolume, MountPath: restoreSourceMount, ReadOnly: true},
				},
				SecurityContext: buildDefaultContainerSecurityContext(),
			},
		},
		Volumes:            []corev1.Volume{buildRestoreSourceVolume(backup, s3Source)},
		PodSecurityContext: buildDefaultSecurityContext(),
	})

	if s3Source {
		fetchScript, ferr := assets.RenderRestoreFetch(restoreSourceMount)
		if ferr != nil {
			return nil, fmt.Errorf("backup %s: render restore fetch script: %w", backup.Name, ferr)
		}
		fetchEnv := []corev1.EnvVar{
			{Name: envSourceBackup, Value: backup.Spec.Restore.SourceBackupName},
		}
		if oplogLimit != "" {
			fetchEnv = append(fetchEnv, corev1.EnvVar{Name: envOplogLimit, Value: oplogLimit})
		}
		fetchEnv = append(fetchEnv, buildS3EnvVars(backup.Spec.Storage.S3)...)

		job.Spec.Template.Spec.InitContainers = []corev1.Container{
			{
				Name:    "fetch",
				Image:   awsCLIImage,
				Command: []string{binBash, "-c", fetchScript},
				Env:     fetchEnv,
				VolumeMounts: []corev1.VolumeMount{
					// init 은 써야 한다 — main 만 ReadOnly.
					{Name: restoreSourceVolume, MountPath: restoreSourceMount},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
				SecurityContext: buildDefaultContainerSecurityContext(),
			},
		}
	}

	return job, nil
}

// buildRestoreSourceVolume 은 restore Job 의 /data/source volume 을 반환.
//
// S3: init container 가 받아 채우는 EmptyDir. **sizeLimit 을 두지 않는다** —
// base 아카이브 크기는 DB 크기에 비례해 상한을 미리 알 수 없고, 임의로 잡으면
// 큰 DB 의 복원이 evict 로 조용히 깨진다. 대신 노드 ephemeral storage 를
// 그만큼 쓴다는 뜻이므로, 큰 클러스터에서는 restore Job 이 뜰 노드의 여유
// 디스크를 확인해야 한다.
//
// PVC: 백업이 덤프를 떨어뜨린 그 PVC (이름 = 소스 backup CR 이름).
func buildRestoreSourceVolume(backup *mongodbv1alpha1.MongoDBBackup, s3Source bool) corev1.Volume {
	if s3Source {
		return corev1.Volume{
			Name:         restoreSourceVolume,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}
	}
	return corev1.Volume{
		Name: restoreSourceVolume,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: backup.Spec.Restore.SourceBackupName,
				ReadOnly:  true,
			},
		},
	}
}
