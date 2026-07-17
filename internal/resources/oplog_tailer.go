/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_tailer.go — PITR Oplog 증분 스트리밍 사이드카 빌더 (아키텍처 A / PBM 방식).
//
// 책임: BackupSpec.PITREnabled=true + S3 storage 인 클러스터의 각 mongod
// (RS member / shard member) pod 에 *함께 배치되는* oplog tailer 사이드카
// 컨테이너 정의를 생성한다.
//
// 사이드카는 mongod 의 local.oplog.rs 를 증분 tail 하여 **S3 로 직접
// 스트리밍**한다 (mongodump → gzip → aws s3 cp -). 중간 staging 파일도,
// 별도 상태 저장소도 없다:
//
//   - 증분: {ts: {$gt: HWM, $lte: NOW}} — 구 구현의 "매 배치 oplog.rs 전량
//     재덤프" 제거.
//   - HWM(resume token): S3 최신 세그먼트 키의 endTs 에서 부팅 시 복원.
//     ConfigMap/PVC/CR status 어디에도 별도 저장하지 않는다 (S3 가 진본).
//   - 원자성: capture 와 upload 가 한 파이프 — EmptyDir 경유가 없으므로
//     pod 재시작 유실창이 사라진다.
//
// S3 키 스킴(restore / uploader 트랙과 공유하는 계약)과 gap 처리 상세는
// internal/assets/scripts/oplog-stream.sh.tpl 헤더 주석 참조.
//
// 회귀 가드: oplog_tailer_test.go 가 본 함수의 반환값을 정합 검증.

package resources

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/assets"
)

const (
	// oplogStagingMount 는 tailer 사이드카의 HOME/TMPDIR 마운트 경로.
	//
	// 아키텍처 A 는 S3 직접 스트리밍이라 oplog batch 는 여기 남지 않는다.
	// 남는 것은 aws CLI 가 요구하는 쓰기 가능한 HOME(~/.aws/cli/cache) 뿐 —
	// non-root(999) 컨테이너에서 HOME 이 쓰기 불가일 때의 실패를 막는 용도.
	// (이름은 구 "staging" 유지 — builder.go 참조를 깨지 않기 위함.)
	oplogStagingMount = "/var/lib/mongodb-oplog-staging"

	// oplogStagingVolume 은 위 scratch volume 이름.
	oplogStagingVolume = "oplog-staging"

	// oplogStagingSizeLimit 은 scratch EmptyDir 상한. batch 를 쌓지 않으므로
	// 구 구현의 4Gi(= oplog batch staging 한도)는 불필요.
	oplogStagingSizeLimit = "1Gi"

	// oplogTailerBatchSeconds 는 한 oplog batch 의 회전 간격. 작을수록 RPO
	// 가 짧지만 S3 객체 수 증가. 30s 기본은 1 hour oplog 가 120 객체 정도.
	oplogTailerBatchSeconds = 30

	// oplogTailerImageEnv 는 tailer 사이드카 이미지 주입 env (operator
	// Deployment 에 지정). resolveOplogTailerImage 주석 참조.
	oplogTailerImageEnv = "OPLOG_TAILER_IMAGE"

	// oplogStorageTypeS3 는 PITR 이 요구하는 유일한 backup storage type.
	oplogStorageTypeS3 = "s3"
)

// IsOplogTailerEnabled 는 주어진 BackupSpec 이 PITR oplog tailer 를 요구하는지
// 검사. controller / builder 가 sidecar 추가 여부 판단에 사용.
//
// S3 storage 를 *요구*한다 — 아키텍처 A 의 사이드카는 oplog 를 S3 로 직접
// 스트리밍하므로 S3 가 아니면 업로드 대상 자체가 없다. PVC + PITREnabled
// 조합에 사이드카를 붙이면 세그먼트를 한 개도 못 쓰면서 도는 silent gap 이
// 되므로, 아예 붙이지 않는다 (webhook 이 이 조합을 거부하는 것은 별건).
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
	if spec.Storage.Type != oplogStorageTypeS3 || spec.Storage.S3 == nil {
		return false
	}
	return spec.Storage.S3.Bucket != ""
}

// resolveOplogTailerImage 는 tailer 사이드카 이미지와 *주입 가능 여부*를 반환한다.
// reason 이 빈 문자열이면 image 는 주입 가능하고, 비어있지 않으면 image 는 ""
// 이며 reason 이 왜 주입할 수 없는지(= fail-open skip 사유)를 설명한다.
//
// 아키텍처 A 는 S3 직접 업로드라 mongodump/mongosh **뿐 아니라 aws CLI 도** 한
// 컨테이너에 필요하다. 공식 mongo 이미지엔 aws CLI 가 없고, 사이드카는 mongod
// 와 같은 pod 의 non-root(999)라 런타임 apt-get 설치도 불가하다 (backup Job 은
// root 라 가능). 따라서 mongodump+mongosh+aws 를 미리 담은 이미지
// (oplog-tailer.Dockerfile)를 빌드해 operator Deployment 의 OPLOG_TAILER_IMAGE
// 로 주입해야 한다.
//
// 구 구현은 미설정 시 mongo 이미지로 *폴백*했으나 그 이미지엔 aws CLI 가 없어
// 스트림 스크립트가 즉시 죽고 → 같은 pod 의 mongod 까지 pod 미준비로 끌어내렸다
// (crash 전파). 그래서 폴백을 제거한다: 미설정이면 reason 을 돌려주고 호출자는
// 사이드카를 아예 주입하지 않는다 (fail-open — mongod 를 위협하지 않는다). 단
// 조용히 끄지 않도록 reason 을 노출해 호출자가 MongoDB status 로 드러내게 한다.
//
// spec.version.image 는 mongod 본체까지 함께 바꾸므로 분리 수단이 못 된다.
// CRD 필드를 새로 파는 대신 배포시점 knob(env)으로 표면을 최소화했다.
func resolveOplogTailerImage() (image string, reason string) {
	if img := os.Getenv(oplogTailerImageEnv); img != "" {
		return img, ""
	}
	return "", fmt.Sprintf(
		"oplog tailer 사이드카 미주입: %s 미설정 — PITR 증분 스트리밍 비활성. "+
			"mongodump+aws 통합 이미지(oplog-tailer.Dockerfile)를 빌드해 operator "+
			"Deployment 의 %s 로 주입해야 한다 (mongo 이미지 폴백은 aws CLI 부재로 "+
			"사이드카 크래시→pod 미준비를 유발하므로 제거됨).",
		oplogTailerImageEnv, oplogTailerImageEnv)
}

// buildOplogS3EnvVars 는 tailer 사이드카의 S3 접속 env 를 반환.
// BuildBackupJob 의 S3 env 주입 블록과 동일한 계약 (변수명 6종 + Secret 키
// access-key / secret-key). spec 이 S3 가 아니면 nil.
//
// NOTE(통합): backup/restore 트랙이 동일 블록을 공용 헬퍼로 뽑으면 본 함수를
// 그쪽으로 접어라 — 지금은 트랙 간 심볼 충돌을 피해 oplog 전용 이름을 쓴다.
func buildOplogS3EnvVars(spec *mongodbv1alpha1.BackupSpec) []corev1.EnvVar {
	if spec == nil || spec.Storage.Type != oplogStorageTypeS3 || spec.Storage.S3 == nil {
		return nil
	}
	s3 := spec.Storage.S3
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

// BuildOplogTailerSidecar 는 PITR oplog 증분 스트리밍 사이드카 컨테이너 spec 을 반환.
//
// 호출 시점: BuildStatefulSet / BuildShardStatefulSet / BuildConfigServerStatefulSet
// 에서 IsOplogTailerEnabled(spec) 가 true 인 경우. 본 함수는 mongod 와 *동일 pod*
// 에 배치되며, mongod 의 admin 권한 secret 를 동일하게 mount 한다.
//
// clusterName 은 S3 키의 `<cluster>` 세그먼트가 되므로 클러스터마다 유일해야
// 한다. spec 은 S3 접속 env 주입에 쓰인다 (IsOplogTailerEnabled 로 사전 게이팅
// 되지 않으면 env 가 비어 스크립트가 S3_BUCKET 부재로 즉시 실패한다).
//
// image 는 mongodump+mongosh+aws 를 담은 통합 이미지다 (resolveOplogTailerImage
// 로 해결). 호출자는 주입 *전* resolveOplogTailerImage 로 image 를 확보하고 —
// 확보 실패(OPLOG_TAILER_IMAGE 미설정) 시 본 함수를 부르지 말고 사이드카를 통째로
// 건너뛴다 (fail-open). 이미지 해결 실패를 컨테이너로 만들지 않는 것이 aws 부재
// 크래시의 pod 전파를 막는 핵심이다.
//
// 반환 컨테이너는 다음 책임:
//  1. mongod localhost:port readiness 대기
//  2. PRIMARY 일 때만 {ts: {$gt: HWM, $lte: NOW}} 증분 dump
//  3. gzip → S3 직접 스트리밍 (성공해야 HWM 전진 — 실패 시 재시도, gap 방지)
//  4. 부팅 시 S3 최신 세그먼트 키에서 HWM 복원 / oplog rollover gap 감지
//
// 보안: securityContext 는 mongod 와 동일 (non-root 999).
// 자원: 64Mi / 50m baseline. 증분 tail 자체는 가벼움.
func BuildOplogTailerSidecar(
	image string,
	mongodPort int32,
	adminSecretMount bool,
	clusterName string,
	spec *mongodbv1alpha1.BackupSpec,
) corev1.Container {
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

	// buildBackupScript 와 동일 규약 — embed 된 template 의 렌더 실패는
	// 프로그래머 오류이므로 panic.
	script, err := assets.RenderOplogStream(int(port), clusterName, oplogStagingMount, oplogTailerBatchSeconds)
	if err != nil {
		panic(fmt.Sprintf("render oplog stream script: %v", err))
	}

	return corev1.Container{
		Name:    "oplog-tailer",
		Image:   image,
		Command: []string{binBash, "-c", script},
		Env:     buildOplogS3EnvVars(spec),
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

// BuildOplogStagingVolume 는 tailer 사이드카의 scratch(HOME/TMPDIR) EmptyDir
// volume spec 을 반환.
//
// 이름은 구 "staging" 이나 더 이상 oplog batch 를 staging 하지 않는다 —
// 아키텍처 A 는 S3 직접 스트리밍이라 batch 파일이 생기지 않는다. 남은 용도는
// aws CLI 의 쓰기 가능한 HOME 뿐이라 상한도 4Gi → 1Gi 로 줄였다.
//
// NOTE(통합): builder.go 가 이 volume 을 mongod 컨테이너에도 마운트하지만
// ("restore drill 용") 이제 batch 파일이 없으므로 그 마운트는 무의미하다 —
// 제거 대상.
func BuildOplogStagingVolume() corev1.Volume {
	return corev1.Volume{
		Name: oplogStagingVolume,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: resourceQuantityPtr(oplogStagingSizeLimit),
			},
		},
	}
}

// resourceQuantityPtr 은 resource.Quantity 의 pointer 를 반환하는 헬퍼.
func resourceQuantityPtr(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
