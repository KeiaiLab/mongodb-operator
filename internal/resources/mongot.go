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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

const (
	// mongotGRPCPort — mongod ↔ mongot 검색 쿼리 + 인덱스 관리(gRPC, localhost intra-pod).
	mongotGRPCPort = 27028
	// MongodReplicaSetPort — RS mongod listen 포트(mongot syncSource). controller 가 ConfigMap 빌드에 사용.
	MongodReplicaSetPort = 27017
	// MongodShardPort — Sharded shard mongod listen 포트(27018 — RS 와 다름, mongot syncSource).
	MongodShardPort = 27018
	// MongosPort — Sharded mongos(router) listen 포트(27017). mongot syncSource.router(Sharded 전용).
	MongosPort = 27017
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
	// MongotRouterShardAnnotation — mongos 가 연결할 mongot 의 shard pin(예 "shard-0").
	// search controller 가 MongoDBSearch.spec.router.mongotShard 를 그대로 전달한다.
	// mongot Service 의 selector(= 단일 shard) 를 결정한다 — 부재 시 DefaultMongotRouterShard.
	MongotRouterShardAnnotation = "search.mongodb.keiailab.com/router-shard"
	// DefaultMongotRouterShard — router shard 미지정 시 기본 pin.
	DefaultMongotRouterShard = "shard-0"

	// MongotPodLabel — mongot sidecar 를 *실제로 보유한* pod 표식(값 "true"). Sharded 의 shard pod
	// 는 component 라벨이 shard-N 으로 shard 마다 달라 공통 selector 가 불가능하다(Service selector 는
	// equality-only). 전 shard 의 mongot 을 하나의 Service 엔드포인트로 묶기 위해 shard pod template
	// 에만(= annotation 활성 시에만) 부착하는 공통 표식. mongos/config server pod 는 부착 대상 아님.
	// STS Selector(immutable)에는 넣지 않는다 — template label 만 추가(기존 STS in-place 갱신 가능).
	MongotPodLabel = "search.mongodb.keiailab.com/mongot"
	// MongotPodLabelValue — MongotPodLabel 의 유일 값(Service selector ↔ pod template 동일 상수 사용).
	MongotPodLabelValue = "true"
	// mongotServiceSuffix — mongot ClusterIP Service 이름 suffix(<cluster>-mongot).
	mongotServiceSuffix = "-mongot"
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
//lint:ignore U1000 spec.resources mongot 적용은 annotation 전달 메커니즘 후속(GA flip 전 처리, staticcheck 억제).
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
//
// routerHostPort: Sharded 일 때만 비어있지 않다(mongos host:port). 비어있으면 ReplicaSet 토폴로지로
// router 블록 생략. Sharded 에서 router 부재 시 mongot 은 "cluster is sharded but syncSource.router
// is not configured" 로 CommunityConfigUpdater 정지(검색 silent 미동작) — mongot 0.69.1 실측. router
// 인증 user = mongos 경유 생성된 search-sync(ensureSyncMongoUserSharded), replicaSet 와 동일 syncUser.
func BuildMongotConfigMap(mdbName, namespace, searchName, syncUser string, tlsEnabled bool, mongodPort int, routerHostPort string) *corev1.ConfigMap {
	// mongot↔mongod 양방향 연결은 in-pod localhost(같은 pod) → 평문이다(tlsEnabled 무관):
	// ① mongod→mongot gRPC: server.grpc.tls.mode="DISABLED" (enum DISABLED|TLS|MTLS, config 에
	//    gRPC cert 필드 없어 TLS 불가; mongod searchTLSMode enum disabled|requireTLS 와 다름).
	// ② mongot→mongod syncSource: tls:false (mongod preferTLS 가 localhost 평문 수락; TLS 시
	//    cert-manager 내부 CA 검증에 CAFile 필요한데 config 에 부재). cluster TLS 는 client/
	//    replication 경로에만 — localhost sidecar 채널과 무관.
	// (구버전: tlsEnabled 시 grpc="requireTLS"[무효 enum] + syncSource tls=true[CA 부재] →
	//  mongot config-parse crash + sync 실패. 발견: prod sharded(preferTLS) search 활성화 2026-06-24.)
	grpcTLSMode := "DISABLED"
	// Sharded: replicaSet(로컬 shard mongod) 뒤에 router(mongos) 블록을 삽입한다. mongot 은 router
	// 로 cluster topology/메타데이터를, replicaSet 으로 데이터 sync 를 한다(양쪽 모두 필요 — 실측).
	// mongos preferTLS 가 평문을 수락 → tls:false(replicaSet localhost 채널과 동일 평문 정책; cluster
	// TLS 검증용 CAFile 부재 회피). routerHostPort 빈 문자열(ReplicaSet)이면 블록 생략.
	routerBlock := ""
	if routerHostPort != "" {
		routerBlock = fmt.Sprintf(`  router:
    hostAndPort: %q
    username: %q
    passwordFile: %s/passwordFile
    tls: false
`, routerHostPort, syncUser, mongotSecretsPath)
	}
	cfg := fmt.Sprintf(`# operator-generated mongot sidecar config — %s/%s (preview)
syncSource:
  replicaSet:
    hostAndPort: "localhost:%d"
    username: %q
    passwordFile: %s/passwordFile
    tls: false
%sstorage:
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
`, namespace, searchName, mongodPort, syncUser, mongotSecretsPath, routerBlock, mongotBasePath,
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

// mongot 기본 자원. mongot 은 JVM 이고 `-Xmx` 를 주지 않는다 — 힙 상한을 컨테이너
// cgroup 에서 읽는다(UseContainerSupport). 따라서 **limit 부재 = 힙 상한 부재**이고, JVM 은
// 노드 전체 RAM 을 기준으로 잡는다. 라이브 실측 2026-08-25: limit 없는 mongot 15본이
// 571Mi~3956Mi 로 제각각 부풀었고, 상한을 아무도 몰랐다. 파드 QoS 도 이 한 컨테이너 때문에
// Burstable 로 떨어져 노드 압박 시 mongod 까지 함께 축출 후보가 된다.
//
// limit 은 mongod(6Gi)와 같은 눈금으로 둔다 — mongot 은 Lucene mmap 이라 페이지 캐시가
// cgroup 에 함께 계상되므로, 여유 없는 상한은 인덱스 페이지를 계속 회수시켜 검색을 느리게 한다.
// request 는 실측 하위값(571Mi) 근처로 낮춰 스케줄러 과예약을 피한다.
const (
	mongotCPURequest    = "50m"
	mongotMemoryRequest = "512Mi"
	mongotCPULimit      = "1"
	mongotMemoryLimit   = "6Gi"
)

func defaultMongotResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(mongotCPURequest),
			corev1.ResourceMemory: resource.MustParse(mongotMemoryRequest),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(mongotCPULimit),
			corev1.ResourceMemory: resource.MustParse(mongotMemoryLimit),
		},
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
		Resources:       defaultMongotResources(),
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

// MongotServiceName — Sharded mongot ClusterIP Service 이름(mongos → mongot 진입점).
func MongotServiceName(clusterName string) string { return clusterName + mongotServiceSuffix }

// MongotServiceEndpoint — mongos setParameter(mongotHost / searchIndexManagementHostAndPort)에 넣을
// mongot Service FQDN:port. shard mongod 는 localhost:27028(자기 사이드카)이지만, mongos 는 mongot
// 사이드카가 없으므로 pod 밖 Service 를 경유해야 한다.
func MongotServiceEndpoint(clusterName, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", MongotServiceName(clusterName), namespace, mongotGRPCPort)
}

// MongotRouterShard — mongos 가 연결할 mongot 의 shard(annotation, 미지정 시 shard-0).
func MongotRouterShard(mdbsh *mongodbv1alpha1.MongoDBSharded) string {
	if s := mdbsh.Annotations[MongotRouterShardAnnotation]; s != "" {
		return s
	}
	return DefaultMongotRouterShard
}

// BuildMongotService — Sharded 전용 mongot ClusterIP Service(gRPC 27028), **단일 shard pin**.
//
// 왜 필요한가: mongos 는 mongot 사이드카를 갖지 않는데, MongoDBSearchIndex 컨트롤러는 인덱스 관리
// 명령($listSearchIndexes / createSearchIndex)을 *mongos 경유*로 보낸다. mongos 에 mongot 엔드포인트
// (mongotHost / searchIndexManagementHostAndPort)가 비어 있으면 SearchNotEnabled 로 거부되어 CR 이
// Pending 고착한다(라이브 실측 2026-07-14). 따라서 mongos 가 도달 가능한 *pod 밖* mongot 진입점이
// 필요하다 — mongot 컨테이너가 27028(mongot-grpc)을 containerPort 로 선언하므로 Service 로 노출 가능.
//
// 왜 *단일 shard* 인가: mongos 는 mongotHost 로 준 엔드포인트에 **직접 연결**하며 broadcast/fan-out
// 하지 않는다(ADR-0039 #7 — dummy endpoint 실측 errCode:125 / 데이터가 다른 shard 면 빈 결과 VS:[]).
// 따라서 전 shard mongot 을 묶은 로드밸런싱 ClusterIP 를 주면 연결마다 임의 shard 의 mongot 으로
// 라우팅되어 **비결정적 빈 결과(조용한 오답)** 가 나온다 — 최악의 실패 모드. selector 를 pin 대상
// shard(MongotRouterShardAnnotation, 기본 shard-0) 의 pod 로 좁혀 결정적 엔드포인트를 만든다.
// ⛔ 이 배선은 검색 대상 컬렉션이 그 shard 에 상주할 때만(=unsharded 컬렉션의 primary shard) 올바른
// 결과를 준다. multi-shard 분산 컬렉션 검색은 ADR-0039 대로 upstream 한계로 **여전히 미해결**.
//
// annotation(MongotSidecarImageAnnotation) 부재 = search 비활성 → nil(생성 안 함, 기존 opt-in 규율).
func BuildMongotService(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.Service {
	if mdbsh.Annotations[MongotSidecarImageAnnotation] == "" {
		return nil
	}
	// pin 대상 shard 의 pod 라벨(component=shard-N) + mongot 표식(사이드카 실보유 pod 만).
	selector := buildLabels(mdbsh.Name, MongotRouterShard(mdbsh))
	selector[MongotPodLabel] = MongotPodLabelValue

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MongotServiceName(mdbsh.Name),
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, "mongot"),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selector,
			Ports: []corev1.ServicePort{
				{Name: "mongot-grpc", Port: mongotGRPCPort, TargetPort: intstr.FromString("mongot-grpc")},
			},
		},
	}
}

// MongotSetParameterArgs — source mongod/mongos 에 주입할 mongot 연동 setParameter.
// endpoint 비어있으면 nil → template 무변경(무롤링). sidecar 보유 mongod 는 endpoint=localhost:27028,
// sidecar 없는 mongos 는 endpoint=<cluster>-mongot.<ns>.svc.cluster.local:27028(MongotServiceEndpoint).
// 4 파라미터 모두 mongos 바이너리에 정식 등록돼 있다(mongo 8.3 라이브 getParameter ok=1 — mongotHost /
// searchIndexManagementHostAndPort / searchTLSMode / useGrpcForSearch).
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
