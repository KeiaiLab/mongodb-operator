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
	// mongotHealthPort — mongot healthCheck(config.default.yml 기본 8080).
	mongotHealthPort = 8080
	// mongotBasePath — mongot data dir(emptyDir mount; serverId.txt + 인덱스 스토어).
	mongotBasePath = "/var/lib/mongot"
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

// mongotResources — v1beta1.ResourcesSpec → corev1.
func mongotResources(spec mongodbv1beta1.ResourcesSpec) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Requests: spec.Requests, Limits: spec.Limits}
}

// mongotLabels — mongot ConfigMap 라벨(component=mongot).
func mongotLabels(searchName string) map[string]string {
	return buildLabels(searchName, "mongot")
}

// MongotConfigMapName — mongot config ConfigMap 이름(mdb 기준 — sidecar 가 마운트).
func MongotConfigMapName(mdbName string) string { return mdbName + "-mongot-config" }

// BuildMongotConfigMap — mongot config.yml(sidecar). syncSource=localhost:27017(로컬 mongod).
// schema SSOT = mongot config.default.yml(kind e2e 실측): hostAndPort 단수 / dataPath=base /
// server.grpc.tls / healthCheck.address / password owner-only.
func BuildMongotConfigMap(mdbName, namespace, searchName, syncUser string, tlsEnabled bool) *corev1.ConfigMap {
	tlsMode := "disabled"
	if tlsEnabled {
		tlsMode = "requireTLS"
	}
	cfg := fmt.Sprintf(`# operator-generated mongot sidecar config — %s/%s (preview)
syncSource:
  replicaSet:
    hostAndPort: "localhost:27017"
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
`, namespace, searchName, syncUser, mongotSecretsPath, tlsEnabled, mongotBasePath,
		mongotGRPCPort, tlsMode, mongotHealthPort)

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
// mongot data 는 emptyDir(oplog 재구축 가능 — mongod STS VCT immutable 회피).
func MongotSidecar(mdbName, image, syncSecretName string) (corev1.Container, corev1.Container, []corev1.Volume) {
	volumes := []corev1.Volume{
		{Name: "mongot-config", VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: MongotConfigMapName(mdbName)}},
		}},
		{Name: "mongot-data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
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
		Name:  "mongot",
		Image: image,
		Args:  []string{"--config", mongotConfigPath + "/" + mongotConfigFile},
		Ports: []corev1.ContainerPort{
			{Name: "mongot-grpc", ContainerPort: mongotGRPCPort},
			{Name: "mongot-health", ContainerPort: mongotHealthPort},
		},
		SecurityContext: buildDefaultContainerSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "mongot-config", MountPath: mongotConfigPath, ReadOnly: true},
			{Name: "mongot-data", MountPath: mongotBasePath},
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
