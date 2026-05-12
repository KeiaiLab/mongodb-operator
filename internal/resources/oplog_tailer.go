/*
Copyright 2026 Keiailab.

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
