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

// BuildBackupJob creates a Job for MongoDB backup
func BuildBackupJob(backup *mongodbv1alpha1.MongoDBBackup, authSecretName string) *batchv1.Job {
	labels := buildLabels(backup.Name, "backup")

	backoff := int32(3)
	ttl := int32(86400) // 24 hours

	var envVars []corev1.EnvVar
	envVars = append(envVars, corev1.EnvVar{
		Name: "MONGODB_URI",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: authSecretName,
				},
				Key: "connectionString",
			},
		},
	})

	// S3 storage configuration
	if backup.Spec.Storage.Type == "s3" && backup.Spec.Storage.S3 != nil {
		s3 := backup.Spec.Storage.S3
		envVars = append(envVars,
			corev1.EnvVar{Name: "S3_BUCKET", Value: s3.Bucket},
			corev1.EnvVar{Name: "S3_ENDPOINT", Value: s3.Endpoint},
			corev1.EnvVar{Name: "S3_REGION", Value: s3.Region},
			corev1.EnvVar{Name: "S3_PREFIX", Value: s3.Prefix},
			corev1.EnvVar{
				Name: "AWS_ACCESS_KEY_ID",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: s3.CredentialsRef,
						Key:                  "access-key",
					},
				},
			},
			corev1.EnvVar{
				Name: "AWS_SECRET_ACCESS_KEY",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: s3.CredentialsRef,
						Key:                  "secret-key",
					},
				},
			},
		)
	}

	// Build backup script
	script := buildBackupScript(backup)

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
				Name:    "backup",
				Image:   defaultImage,
				Command: []string{"/bin/bash", "-c"},
				Args:    []string{script},
				Env:     envVars,
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

func buildBackupScript(backup *mongodbv1alpha1.MongoDBBackup) string {
	compressionFlag := "--gzip"
	if backup.Spec.CompressionType == "zstd" {
		compressionFlag = "--archive"
	}
	// S3 변형은 mongodump --archive를 stdin으로 piping해 stdout에 쓴 뒤 aws s3 cp -.
	// PVC 변형은 --out으로 directory에 직접 출력. assets/scripts/backup-{s3,pvc}.sh.tpl 분기.
	out, err := assets.RenderBackup(backup.Spec.Storage.Type, backup.Spec.ClusterRef.Name, compressionFlag)
	if err != nil {
		panic(fmt.Sprintf("render backup script: %v", err))
	}
	return out
}

// BuildRestoreJob — cycle 15. mongorestore Job 을 생성. Spec.Restore 가 nil
// 이 아닌 MongoDBBackup CR 에 대해 controller 가 호출.
//
// 동작:
//  1. SourceBackupName 의 PVC 또는 S3 location 에서 dump 데이터 read
//  2. mongorestore --uri <target> --archive=<source> [--oplogReplay] 실행
//  3. PointInTime 이 설정되면 --oplogLimit <ts> 추가 (PITR)
//
// 본 cycle 의 acceptance: Job 객체 생성 + controller 가 spawn. 실제 oplog
// archive 의 S3 fetch + mongorestore 실행 정합은 cycle 16 운영 강화 시점.
func BuildRestoreJob(backup *mongodbv1alpha1.MongoDBBackup, authSecretName string) (*batchv1.Job, error) {
	// 결함 #1: 일반 백업 (Spec.Restore=nil) verify 시 nil 역참조 panic 차단.
	// 본 함수는 restore 작업 (Spec.Restore != nil) 에 대해서만 호출되어야 한다.
	if backup.Spec.Restore == nil {
		return nil, fmt.Errorf("backup %s: Spec.Restore is nil", backup.Name)
	}
	labels := buildLabels(backup.Name, "restore")
	backoff := int32(3)
	ttl := int32(86400)

	envVars := []corev1.EnvVar{
		{Name: "MONGODB_URI", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName}, Key: "connectionString"}}},
		{Name: "SOURCE_BACKUP", Value: backup.Spec.Restore.SourceBackupName},
	}
	if backup.Spec.Restore.PointInTime != nil {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "POINT_IN_TIME",
			Value: backup.Spec.Restore.PointInTime.Format("2006-01-02T15:04:05Z"),
		})
	}

	// Restore script — mongorestore + --oplogReplay --oplogLimit
	script := `set -eu
echo "[restore] source=${SOURCE_BACKUP} pit=${POINT_IN_TIME:-none}"
RESTORE_FLAGS="--archive=/data/source/dump.archive --gzip --drop"
if [ -n "${POINT_IN_TIME:-}" ]; then
  EPOCH=$(date -u -d "${POINT_IN_TIME}" +%s)
  RESTORE_FLAGS="${RESTORE_FLAGS} --oplogReplay --oplogLimit=${EPOCH}:0"
fi
mongorestore --uri "${MONGODB_URI}" ${RESTORE_FLAGS}
echo "[restore] completed"
`
	// Job 엔벨로프는 keiailab-commons/pkg/batchjob.Build 에 위임. 컨테이너(mongorestore)/볼륨 잔류.
	return commonsbatchjob.Build(commonsbatchjob.Params{
		Name:                    backup.Name + "-restore",
		Namespace:               backup.Namespace,
		Labels:                  labels,
		BackoffLimit:            &backoff,
		TTLSecondsAfterFinished: &ttl,
		Containers: []corev1.Container{
			{
				Name:    "mongorestore",
				Image:   getMongoDBImage(mongodbv1alpha1.MongoDBVersion{Version: "8.2"}),
				Command: []string{"sh", "-c", script},
				Env:     envVars,
				VolumeMounts: []corev1.VolumeMount{
					{Name: "source", MountPath: "/data/source", ReadOnly: true},
				},
				SecurityContext: buildDefaultContainerSecurityContext(),
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: "source",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: backup.Spec.Restore.SourceBackupName,
						ReadOnly:  true,
					},
				},
			},
		},
		PodSecurityContext: buildDefaultSecurityContext(),
	}), nil
}
