/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongot.go — Phase 1.1 (Atlas 갭 클로징): MongoDB Search/Vector Search 엔진(mongot)
// **sidecar** 배포. Community mongot(mongodb-community-search)의 topology monitor 는
// localhost:27017(로컬 mongod)에 연결하므로 mongot 은 mongod pod 의 sidecar 여야 한다
// (별도 StatefulSet 비호환 — kind e2e 실증, memory mongot-search-e2e-findings).
//
// MongoDBSearch CR → search controller 가 mongot ConfigMap(localhost syncSource) 생성 +
// source MongoDB 에 sidecar annotation 설정 → mongod builder(BuildReplicaSetStatefulSet)가
// annotation 있을 때만 mongot sidecar 컨테이너 + init(password 0400) + volumes + mongod
// setParameter(mongotHost=localhost:27028) 주입. annotation 부재 시 무변경 = 무롤링.
//
// mongot: gRPC 27028(쿼리+인덱스관리) + 8080(health). mongo 8.2+ 필요.
// ⚠ public preview — config schema/이미지 태그 GA 시 변동 가능(spec.version.Image override).

package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

const (
	// mongotGRPCPort — mongod ↔ mongot 검색 쿼리 + 인덱스 관리(gRPC, localhost intra-pod).
	mongotGRPCPort = 27028
	// MongodReplicaSetPort — RS mongod listen 포트(mongot syncSource). controller 가 ConfigMap 빌드에 사용.
	MongodReplicaSetPort = 27017
	// MongodShardPort — Sharded shard mongod listen 포트(27018 — RS 와 다름, mongot syncSource).
	MongodShardPort = 27018
	// mongotHealthPort — mongot healthCheck(config.default.yml 기본 8080).
	mongotHealthPort = 8080
	// mongotBasePath — mongot data dir(serverId.txt + 인덱스 스토어). mongod data PVC 의
	// subPath 로 mount(노드 루트 디스크 종속 제거 — kind e2e 근본 원인: emptyDir 가 노드
	// 디스크 공유 → 노드 압박 시 mongot replication pause → 검색 silent 정지).
	mongotBasePath = "/var/lib/mongot"
	// mongodDataVolume — mongod data PVC VCT 이름(builder.go VolumeClaimTemplates). mongot 이
	// subPath 로 공유 → 별도 VCT 추가 불필요(commonsapply VCT immutable → 기존 MongoDB search
	// 활성화 호환) + 전용 스토리지(production 별도 볼륨) → 노드 디스크 독립 + 영속.
	mongodDataVolume = "data"
	// mongotDataSubPath — mongod data PVC 내 mongot 인덱스 subPath(mongod dbpath 와 분리).
	mongotDataSubPath = "search-index"
	// mongotConfigPath — config.yml mount dir.
	mongotConfigPath = "/etc/mongot/config"
	// mongotConfigFile — config 파일명.
	mongotConfigFile = "config.yml"
	// mongotSecretsPath — password(0400) mount dir(init container 복사 대상).
	mongotSecretsPath = "/etc/mongot/secrets"
	// mongotSyncRawPath — 원본 sync secret mount(init container 입력).
	mongotSyncRawPath = "/tmp/mongot-sync"
	// defaultMongotImage — Community self-managed mongot(검증: hub.docker.com).
	defaultMongotImage = "mongodb/mongodb-community-search:latest"
	// mongotBinary — 이미지 내 mongot 바이너리. 이미지 ENTRYPOINT 가 config.default.yml 을
	// 하드코딩(sh -c "/mongot-community/mongot --config /mongot-community/config.default.yml")
	// 하므로, 컨테이너 Command 로 override 해야 operator 생성 config 가 적용됨(kind e2e 실증).
	mongotBinary = "/mongot-community/mongot"
	// setParameterFlag — 반복 리터럴 const(goconst).
	setParameterFlag = "--setParameter"
	// mongotSecretsVolume — password emptyDir volume 이름(반복 const, goconst).
	mongotSecretsVolume = "mongot-secrets"

	// MongotSidecarImageAnnotation — search controller 가 source MongoDB 에 설정(존재=sidecar 활성).
	// 값 = mongot 이미지. mongod builder 가 읽어 sidecar 주입(부재 시 무변경=무롤링).
	MongotSidecarImageAnnotation = "search.mongodb.keiailab.com/mongot-image"
	// MongotSyncSecretAnnotation — searchCoordinator sync secret 이름.
	MongotSyncSecretAnnotation = "search.mongodb.keiailab.com/sync-secret"
	// MongotTLSModeAnnotation — mongod searchTLSMode + mongot config tls(disabled|requireTLS).
	MongotTLSModeAnnotation = "search.mongodb.keiailab.com/tls-mode"
)

// MongotImage — spec.version 에서 mongot 이미지 결정(controller 가 annotation 설정에 사용).
func MongotImage(v mongodbv1beta1.MongotVersion) string {
	if v.Image != "" {
		return v.Image
	}
	if v.Version != "" {
		return "mongodb/mongodb-community-search:" + v.Version
	}
	return defaultMongotImage
}

// mongotResources — v1beta1.ResourcesSpec → corev1. 현재 MongotSidecar 미연결(PR3 도입 후 미사용):
// spec.resources 를 mongot 컨테이너에 적용하려면 search controller→source annotation 전달 메커니즘이
// 필요(sidecar 는 source annotation 기반 빌드). GA flip(PR6) 전 처리 대상 — 발견사항.
//
//nolint:unused // spec.resources mongot 적용은 annotation 전달 메커니즘 후속(GA flip 전 처리).
func mongotResources(spec mongodbv1beta1.ResourcesSpec) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Requests: spec.Requests, Limits: spec.Limits}
}

// mongotLabels — mongot ConfigMap 라벨(component=mongot).
func mongotLabels(searchName string) map[string]string {
	return buildLabels(searchName, "mongot")
}

// MongotConfigMapName — mongot config ConfigMap 이름(mdb 기준 — sidecar 가 마운트).
func MongotConfigMapName(mdbName string) string { return mdbName + "-mongot-config" }

// BuildMongotConfigMap — mongot config.yml(sidecar). syncSource=localhost:<mongodPort>(로컬 mongod).
// mongodPort 는 topology 별로 다르다: ReplicaSet=27017, Sharded shard=27018(shard mongod 가 27018
// listen — 27017 하드코딩 시 shard mongot sync 연결 실패). config server 는 mongot 미배포(메타데이터만).
// schema SSOT = mongot config.default.yml(kind e2e 실측): hostAndPort 단수 / dataPath=base /
// server.grpc.tls / healthCheck.address / password owner-only.
func BuildMongotConfigMap(mdbName, namespace, searchName, syncUser string, tlsEnabled bool, mongodPort int) *corev1.ConfigMap {
	// mongod↔mongot gRPC 는 in-pod localhost(같은 pod) → 평문(DISABLED). cluster TLS 는
	// mongot→mongod syncSource(아래 `tls: %t` = tlsEnabled)에만 적용된다. mongot
	// `server.grpc.tls.mode` enum = DISABLED|TLS|MTLS 이고 mongod searchTLSMode enum
	// (disabled|requireTLS)과 다르며, 본 config 에 gRPC cert 필드가 없어 TLS/MTLS 불가.
	// (구버전: tlsEnabled 시 "requireTLS" 주입 → mongot "must be one of [DISABLED, MTLS, TLS]"
	//  config-parse crash. 발견: prod sharded(TLS) search 활성화 2026-06-24.)
	grpcTLSMode := "DISABLED"
	cfg := fmt.Sprintf(`# operator-generated mongot sidecar config — %s/%s (preview)
syncSource:
  replicaSet:
    hostAndPort: "localhost:%d"
    username: %q
    passwordFile: %s/passwordFile
    tls: %t
storage:
  dataPath: %q
server:
  grpc:
    address: "0.0.0.0:%d"
    tls:
      mode: %q
metrics:
  enabled: true
  address: "0.0.0.0:9946"
healthCheck:
  address: "0.0.0.0:%d"
logging:
  verbosity: INFO
`, namespace, searchName, mongodPort, syncUser, mongotSecretsPath, tlsEnabled, mongotBasePath,
		mongotGRPCPort, grpcTLSMode, mongotHealthPort)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MongotConfigMapName(mdbName),
			Namespace: namespace,
			Labels:    mongotLabels(searchName),
		},
		Data: map[string]string{mongotConfigFile: cfg},
	}
}

// MongotSidecar — mongod pod 에 주입할 mongot sidecar: (mongot 컨테이너, init 컨테이너, volumes).
// init(999)가 sync secret 의 password 를 emptyDir 로 cp+chmod 0400(mongot owner-only 요구).
// mongot data(인덱스 스토어) = mongod data PVC 의 subPath(search-index) 공유 — 노드 루트
// 디스크 종속 제거(kind e2e 근본 원인: emptyDir 가 노드 디스크 공유 → 노드 압박 시 mongot
// disk-usage pause → 검색 silent 정지). 별도 VCT 불가(commonsapply VCT immutable → 기존
// MongoDB search 활성화 시 pod 기동 실패) → 기존 data VCT 재사용. 영속(pod 재시작 인덱스 보존).
func MongotSidecar(mdbName, image, syncSecretName string) (corev1.Container, corev1.Container, []corev1.Volume) {
	// "data" volume 은 mongod STS VolumeClaimTemplates 가 제공(여기서 추가 X — 중복 방지).
	volumes := []corev1.Volume{
		{Name: "mongot-config", VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: MongotConfigMapName(mdbName)}},
		}},
		{Name: "mongot-sync-raw", VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: syncSecretName, DefaultMode: ptr.To[int32](0400)},
		}},
		{Name: mongotSecretsVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
	initC := corev1.Container{
		Name:            "copy-mongot-password",
		Image:           keyfileInitImage,
		Command:         []string{"sh", "-c", "cp " + mongotSyncRawPath + "/password " + mongotSecretsPath + "/passwordFile && chmod 0400 " + mongotSecretsPath + "/passwordFile"},
		SecurityContext: buildKeyfileInitContainerSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "mongot-sync-raw", MountPath: mongotSyncRawPath, ReadOnly: true},
			{Name: mongotSecretsVolume, MountPath: mongotSecretsPath},
		},
	}
	mongotC := corev1.Container{
		Name:    "mongot",
		Image:   image,
		Command: []string{mongotBinary, "--config", mongotConfigPath + "/" + mongotConfigFile},
		Ports: []corev1.ContainerPort{
			{Name: "mongot-grpc", ContainerPort: mongotGRPCPort},
			{Name: "mongot-health", ContainerPort: mongotHealthPort},
		},
		SecurityContext: buildDefaultContainerSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "mongot-config", MountPath: mongotConfigPath, ReadOnly: true},
			// mongod data PVC 공유(subPath) — 노드 디스크 독립 + 영속.
			{Name: mongodDataVolume, MountPath: mongotBasePath, SubPath: mongotDataSubPath},
			{Name: mongotSecretsVolume, MountPath: mongotSecretsPath, ReadOnly: true},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(mongotGRPCPort)}},
			InitialDelaySeconds: 15, PeriodSeconds: 10,
		},
	}
	return mongotC, initC, volumes
}

// MongotSetParameterArgs — source mongod 에 주입할 mongot 연동 setParameter.
// endpoint 비어있으면 nil → mongod template 무변경(무롤링). sidecar 는 endpoint=localhost:27028.
func MongotSetParameterArgs(endpoint, tlsMode string) []string {
	if endpoint == "" {
		return nil
	}
	if tlsMode == "" {
		tlsMode = "disabled"
	}
	return []string{
		setParameterFlag, "mongotHost=" + endpoint,
		setParameterFlag, "searchIndexManagementHostAndPort=" + endpoint,
		setParameterFlag, "searchTLSMode=" + tlsMode,
		setParameterFlag, "useGrpcForSearch=true",
	}
}
