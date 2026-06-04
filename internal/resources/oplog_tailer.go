/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_tailer.go — F02 (cycle 1) PITR Oplog tailing sidecar 빌더.
//
// 책임: BackupSpec.PITREnabled=true 인 MongoDB / MongoDBSharded 클러스터의
// 각 mongod (RS member / shard member) pod 에 *함께 배치되는* oplog tailer
// 사이드카 컨테이너 정의를 생성한다. 사이드카는 mongod 의 local.oplog.rs 를
// 지속 tail 하여 oplog batch 를 *임시 EmptyDir volume* 에 떨어뜨리고, S3
// uploader controller (F03, internal/controller/oplog_uploader.go) 가
// rollover 시점에 객체를 업로드한다.
//
// 본 파일은 *컨테이너 spec 만* 정의한다 — 실제 tailing 명령은 mongo-go-driver
// 가 아닌 mongo:8.2 base image 의 `mongodump --oplog` 패턴 (반복) 으로
// 구현 (CronJob 보다 sidecar 가 RPO 최소화에 유리).
//
// 회귀 가드: oplog_tailer_test.go 가 본 함수의 반환값을 정합 검증.

package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	// oplogStagingMount 는 EmptyDir staging volume 의 mount path.
	// uploader controller 가 본 path 를 watch 하여 rollover detection.
	oplogStagingMount = "/var/lib/mongodb-oplog-staging"

	// oplogStagingVolume 은 staging volume 이름. mongod 컨테이너에도 마운트
	// (uploader 가 mongorestore --oplogReplay 시 동일 path 접근 필요).
	oplogStagingVolume = "oplog-staging"

	// oplogTailerBatchSeconds 는 한 oplog batch 의 회전 간격. 작을수록 RPO
	// 가 짧지만 S3 객체 수 증가. 30s 기본은 1 hour oplog 가 120 객체 정도.
	oplogTailerBatchSeconds = 30

	// oplogUploadIntervalSeconds 는 uploader sidecar 가 staging → S3 업로드를
	// 시도하는 주기. tailer batch 와 동일 (rollover 직후 업로드, RPO 최소화).
	oplogUploadIntervalSeconds = 30

	// oplogUploaderImage 는 uploader sidecar image. mongodump 불필요 (tailer 가
	// dump) → 경량 aws-cli (backup-s3.sh.tpl 의 aws s3 cp 패턴 재사용). AWS 공식
	// image — ADR-0136 Bitnami portfolio 금지 무관.
	oplogUploaderImage = "amazon/aws-cli:2.27.49"
)

// IsOplogTailerEnabled 는 주어진 BackupSpec 이 PITR 활성 + retention 양수인지
// 검사. controller / builder 가 sidecar 추가 여부 판단에 사용.
func IsOplogTailerEnabled(spec *mongodbv1alpha1.BackupSpec) bool {
	if spec == nil {
		return false
	}
	if !spec.Enabled || !spec.PITREnabled {
		return false
	}
	if spec.OplogRetentionHours <= 0 {
		return false
	}
	return true
}

// BuildOplogTailerSidecar 는 PITR oplog tailing sidecar 컨테이너 spec 을 반환.
//
// 호출 시점: BuildStatefulSet / BuildShardStatefulSet / BuildConfigServerStatefulSet
// 에서 BackupSpec.PITREnabled 가 true 인 경우. 본 함수는 mongod 와 *동일 pod*
// 에 배치되며, mongod 의 admin 권한 secret 를 동일하게 mount 한다.
//
// 반환 컨테이너는 다음 책임:
//  1. mongosh / mongodump 로 mongod localhost:port 에 접속
//  2. local.oplog.rs 를 batch 단위로 dump (`--oplog --readPreference=secondary`)
//  3. oplogStagingMount 에 timestamped 파일 작성 (rollover)
//  4. 컨테이너 사망 시 자동 재시작 (sidecar pattern)
//
// 보안: securityContext 는 mongod 와 동일 (non-root, readOnlyRootFilesystem).
// 자원: 64Mi / 50m baseline. tailing 자체는 가벼움.
func BuildOplogTailerSidecar(version mongodbv1alpha1.MongoDBVersion, mongodPort int32, adminSecretMount bool) corev1.Container {
	port := mongodPort
	if port <= 0 {
		port = mongoDBPort
	}

	mounts := []corev1.VolumeMount{
		{Name: oplogStagingVolume, MountPath: oplogStagingMount},
	}
	if adminSecretMount {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "admin-credentials",
			MountPath: "/etc/mongodb-admin",
			ReadOnly:  true,
		})
	}

	// mongod 의 admin password 로 mongodump 호출. backoff loop 로 mongod
	// readiness 대기 + rollover 마다 timestamped 파일 생성.
	//
	// shell script 내 변수는 envsubst 가 아닌 shell parameter expansion 사용.
	// $(date) 와 ${MONGO_PASS} 가 컨테이너 entrypoint 안에서 평가됨.
	script := `set -eu
PASS_FILE=/etc/mongodb-admin/password
ADMIN_USER=admin
if [ -f "${PASS_FILE}" ]; then
  ADMIN_PASS=$(cat "${PASS_FILE}")
else
  ADMIN_PASS=""
fi
until mongosh --quiet --port ` + portString(port) + ` -u "${ADMIN_USER}" -p "${ADMIN_PASS}" --eval 'db.adminCommand({ping:1})' >/dev/null 2>&1; do
  echo "[oplog-tailer] waiting for mongod..."
  sleep 5
done
echo "[oplog-tailer] mongod ready, starting tail loop (batch=` + intString(oplogTailerBatchSeconds) + `s)"
while true; do
  TS=$(date -u +%Y%m%dT%H%M%SZ)
  OUT=` + oplogStagingMount + `/oplog-${TS}.bson
  mongodump --port ` + portString(port) + ` \
    -u "${ADMIN_USER}" -p "${ADMIN_PASS}" \
    --authenticationDatabase=admin \
    --db=local --collection=oplog.rs \
    --readPreference=secondary \
    --quiet --archive="${OUT}" || echo "[oplog-tailer] dump failed (will retry)"
  sleep ` + intString(oplogTailerBatchSeconds) + `
done
`

	return corev1.Container{
		Name:    "oplog-tailer",
		Image:   getMongoDBImage(version),
		Command: []string{"sh", "-c", script},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
		VolumeMounts:    mounts,
		SecurityContext: buildDefaultContainerSecurityContext(),
	}
}

// BuildOplogStagingVolume 는 oplog tailer 가 batch 를 떨어뜨릴 EmptyDir
// volume spec 을 반환. uploader sidecar (또는 controller-side CronJob) 가
// 동일 volume 을 읽어 S3 업로드.
//
// EmptyDir 사용 이유: oplog batch 는 *짧은 시간* 유지 (rollover 후 즉시
// S3 업로드 → 로컬 삭제). 별도 PVC 는 비용/관리 부담.
func BuildOplogStagingVolume() corev1.Volume {
	return corev1.Volume{
		Name: oplogStagingVolume,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: resourceQuantityPtr("4Gi"),
			},
		},
	}
}

// IsOplogUploaderEnabled 는 PITR oplog uploader sidecar 추가 대상인지 검사.
// tailer 활성 (IsOplogTailerEnabled) + Storage.Type=="s3" 양쪽 충족 시에만
// uploader 를 추가한다. PVC storage 는 staging EmptyDir 가 곧 최종 저장이
// 아니므로 (별 retention 설계) 본 cycle uploader 대상 외 — S3 전용.
func IsOplogUploaderEnabled(spec *mongodbv1alpha1.BackupSpec) bool {
	if !IsOplogTailerEnabled(spec) {
		return false
	}
	return spec.Storage.Type == "s3" && spec.Storage.S3 != nil
}

// BuildOplogUploaderSidecar 는 PITR oplog S3 업로드 sidecar 컨테이너 spec 을
// 반환. tailer 가 oplogStagingMount EmptyDir 에 떨어뜨린 oplog-*.bson 을
// oplogUploadIntervalSeconds 주기로 S3 (BackupSpec.Storage.S3) 에 업로드 +
// 업로드 성공 시 로컬 삭제 (staging 공간 회수). mongod / tailer 와 동일 pod.
//
// 호출 시점: BuildStatefulSet / BuildShardStatefulSet / BuildConfigServerStatefulSet
// 에서 IsOplogUploaderEnabled 가 true 인 경우만 (PVC storage 는 uploader 불필요).
//
// image: aws-cli (mongodump 불필요 — tailer 가 dump). credential: S3.CredentialsRef
// 의 access-key / secret-key (BuildBackupJob 의 S3 env 패턴 동일).
// 보안: securityContext 는 tailer 와 동일 (non-root 999, capabilities drop ALL).
// aws cli 는 env credential 사용 (~/.aws 불필요) + HOME=/tmp (temp 파일).
func BuildOplogUploaderSidecar(spec *mongodbv1alpha1.BackupSpec) corev1.Container {
	s3 := spec.Storage.S3

	// aws cli 는 AWS_DEFAULT_REGION 을 요구 — S3_REGION 커스텀 env 는 cli 가
	// 보지 않으므로, 미설정 시 InvalidLocationConstraint / region None 오류.
	// RGW (Ceph) 는 zonegroup 무관하나 cli signing 에 region 문자열이 필수 →
	// 빈 값이면 dummy us-east-1 사용 (e2e 라이브 RGW 검증으로 발견, 2026-06-04).
	region := s3.Region
	if region == "" {
		region = "us-east-1"
	}

	env := []corev1.EnvVar{
		{Name: "S3_BUCKET", Value: s3.Bucket},
		{Name: "S3_ENDPOINT", Value: s3.Endpoint},
		{Name: "S3_REGION", Value: s3.Region},
		{Name: "AWS_DEFAULT_REGION", Value: region},
		{Name: "S3_PREFIX", Value: s3.Prefix},
		{Name: "HOME", Value: "/tmp"},
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

	tlsFlag := ""
	if s3.InsecureSkipTLS {
		tlsFlag = " --no-verify-ssl"
	}

	// staging 의 oplog-*.bson 을 S3 로 cp + 성공 시 삭제. 실패 객체는 다음
	// cycle 재시도 (rm 안 함). aws cli 는 AWS_ACCESS_KEY_ID 등 env credential
	// 자동 사용. ${S3_PREFIX} 에 oplog/ subkey 부착해 backup archive 와 분리.
	script := `set -eu
echo "[oplog-uploader] S3 upload loop interval=` + intString(oplogUploadIntervalSeconds) + `s dest=s3://${S3_BUCKET}/${S3_PREFIX}oplog/"
while true; do
  for f in ` + oplogStagingMount + `/oplog-*.bson; do
    [ -e "$f" ] || continue
    base=$(basename "$f")
    if aws s3 cp "$f" "s3://${S3_BUCKET}/${S3_PREFIX}oplog/${base}" --endpoint-url="${S3_ENDPOINT}"` + tlsFlag + `; then
      rm -f "$f"
      echo "[oplog-uploader] uploaded ${base}"
    else
      echo "[oplog-uploader] upload failed: ${base} (retry next cycle)"
    fi
  done
  sleep ` + intString(oplogUploadIntervalSeconds) + `
done
`

	return corev1.Container{
		Name:    "oplog-uploader",
		Image:   oplogUploaderImage,
		Command: []string{"/bin/sh", "-c", script},
		Env:     env,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: oplogStagingVolume, MountPath: oplogStagingMount},
		},
		SecurityContext: buildDefaultContainerSecurityContext(),
	}
}

// resourceQuantityPtr 은 resource.Quantity 의 pointer 를 반환하는 헬퍼.
func resourceQuantityPtr(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

// portString / intString 는 int32 / int 를 shell-safe 문자열로 변환.
// `fmt.Sprintf` 의존을 피해 본 파일 self-contained.
func portString(p int32) string { return intString(int(p)) }

func intString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
