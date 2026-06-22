/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongot.go — Phase 1 (Atlas 갭 클로징): MongoDB Search/Vector Search 엔진(mongot)
// 배포 빌더. MongoDBSearch CR 1건 당 mongot StatefulSet + headless Service +
// config ConfigMap + NetworkPolicy 를 생성하고, source mongod 에 주입할
// setParameter 인자를 제공한다.
//
// mongot 은 gRPC 27028(검색 쿼리 + 인덱스 관리) + 27029(health) 를 listen 하고,
// source replica set(mongod)을 sync source 로 색인을 빌드한다. mongo 8.2+ 필요.
//
// ⚠ MongoDB Search self-managed 는 public preview — config.yml schema / 이미지
// 태그는 GA 시 변동 가능. spec.version.Image 로 override 가능하게 둔다.

package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

const (
	// mongotGRPCPort — mongod ↔ mongot 검색 쿼리 + 인덱스 관리(gRPC).
	mongotGRPCPort = 27028
	// mongotHealthPort — health/status.
	mongotHealthPort = 27029
	// mongotDataPath — 인덱스 스토어 PVC mount.
	mongotDataPath = "/var/lib/mongot/data"
	// mongotConfigPath — config.yml mount dir.
	mongotConfigPath = "/etc/mongot/config"
	// mongotConfigFile — config 파일명.
	mongotConfigFile = "config.yml"
	// defaultMongotImage — Community self-managed mongot(검증: hub.docker.com).
	// override: spec.version.Image. (preview — GA 시 태그 확정.)
	defaultMongotImage = "mongodb/mongodb-community-search:latest"
	// MongotSearchEndpointAnnotation — source MongoDB CR 에 search controller 가
	// 설정하는 mongot endpoint. mongod builder 가 읽어 setParameter 주입(있을 때만 →
	// search 미사용 시 mongod template 무변경 = 무롤링).
	MongotSearchEndpointAnnotation = "search.mongodb.keiailab.com/mongot-endpoint"
	// MongotTLSModeAnnotation — mongod 의 searchTLSMode(disabled|requireTLS).
	MongotTLSModeAnnotation = "search.mongodb.keiailab.com/tls-mode"
	// setParameterFlag / mongotDataVolume — 반복 리터럴 const 화(goconst, CI cross-review).
	setParameterFlag = "--setParameter"
	mongotDataVolume = "data"
)

// mongotImage — spec.version 에서 mongot 이미지 결정.
func mongotImage(v mongodbv1beta1.MongotVersion) string {
	if v.Image != "" {
		return v.Image
	}
	if v.Version != "" {
		return "mongodb/mongodb-community-search:" + v.Version
	}
	return defaultMongotImage
}

// mongotResources — v1beta1.ResourcesSpec → corev1 (builder.go 의 v1alpha1
// buildResourceRequirements 와 별개 — version mismatch 회피).
func mongotResources(spec mongodbv1beta1.ResourcesSpec) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Requests: spec.Requests, Limits: spec.Limits}
}

// mongotLabels — mongot 리소스 라벨(component=mongot). operator buildLabels 위임
// (commonslabels — 라벨 리터럴 중복 회피, goconst).
func mongotLabels(searchName string) map[string]string {
	return buildLabels(searchName, "mongot")
}

// MongotServiceName — mongot headless service 이름.
func MongotServiceName(searchName string) string { return searchName + "-mongot" }

// MongotEndpoint — mongod 가 연결할 mongot endpoint(host:27028, in-cluster DNS).
func MongotEndpoint(searchName, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", MongotServiceName(searchName), namespace, mongotGRPCPort)
}

// BuildMongotConfigMap — mongot config.yml(operator 생성). source mongod 를 sync
// source 로, searchCoordinator 사용자로 인증. (preview schema — kind PoC 에서 검증.)
func BuildMongotConfigMap(search *mongodbv1beta1.MongoDBSearch, sourceHosts []string, syncUser string, tlsEnabled bool) *corev1.ConfigMap {
	hostsYAML := ""
	for _, h := range sourceHosts {
		hostsYAML += fmt.Sprintf("      - host: %q\n", h)
	}
	tlsMode := "disabled"
	if tlsEnabled {
		tlsMode = "requireTLS"
	}
	cfg := fmt.Sprintf(`# operator-generated — MongoDBSearch %s/%s (preview)
syncSource:
  replicaSet:
    hostAndPorts:
%s    username: %q
    authSource: admin
    passwordFile: /etc/mongot/secret/password
    tls: %t
storage:
  dataPath: %q
server:
  grpc:
    address: "0.0.0.0:%d"
  health:
    address: "0.0.0.0:%d"
  tls:
    mode: %s
metrics:
  enabled: true
logging:
  verbosity: INFO
`, search.Namespace, search.Name, hostsYAML, syncUser, tlsEnabled, mongotDataPath,
		mongotGRPCPort, mongotHealthPort, tlsMode)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      search.Name + "-mongot-config",
			Namespace: search.Namespace,
			Labels:    mongotLabels(search.Name),
		},
		Data: map[string]string{mongotConfigFile: cfg},
	}
}

// BuildMongotService — mongot headless service(gRPC 27028 + health 27029).
func BuildMongotService(search *mongodbv1beta1.MongoDBSearch) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MongotServiceName(search.Name),
			Namespace: search.Namespace,
			Labels:    mongotLabels(search.Name),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                corev1.ClusterIPNone,
			Selector:                 mongotLabels(search.Name),
			PublishNotReadyAddresses: true,
			Ports: []corev1.ServicePort{
				{Name: "grpc", Port: mongotGRPCPort, TargetPort: intstr.FromInt(mongotGRPCPort)},
				{Name: "health", Port: mongotHealthPort, TargetPort: intstr.FromInt(mongotHealthPort)},
			},
		},
	}
}

// BuildMongotStatefulSet — mongot StatefulSet(인덱스 스토어 PVC 전용).
func BuildMongotStatefulSet(search *mongodbv1beta1.MongoDBSearch, syncSecretName string) *appsv1.StatefulSet {
	labels := mongotLabels(search.Name)
	replicas := search.Spec.Replicas
	if replicas < 1 {
		replicas = 1
	}

	volumes := []corev1.Volume{
		{Name: "config", VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: search.Name + "-mongot-config"},
			},
		}},
	}
	volumeMounts := []corev1.VolumeMount{
		{Name: "config", MountPath: mongotConfigPath, ReadOnly: true},
		{Name: mongotDataVolume, MountPath: mongotDataPath},
	}
	if syncSecretName != "" {
		volumes = append(volumes, corev1.Volume{Name: "sync-secret", VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: syncSecretName, DefaultMode: ptr.To[int32](0400)},
		}})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "sync-secret", MountPath: "/etc/mongot/secret", ReadOnly: true})
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      search.Name + "-mongot",
			Namespace: search.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: MongotServiceName(search.Name),
			Replicas:    ptr.To(replicas),
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					SecurityContext:              buildDefaultSecurityContext(),
					AutomountServiceAccountToken: ptr.To(false),
					Volumes:                      volumes,
					Containers: []corev1.Container{{
						Name:  "mongot",
						Image: mongotImage(search.Spec.Version),
						Args:  []string{"--config", mongotConfigPath + "/" + mongotConfigFile},
						Ports: []corev1.ContainerPort{
							{Name: "grpc", ContainerPort: mongotGRPCPort},
							{Name: "health", ContainerPort: mongotHealthPort},
						},
						Resources:       mongotResources(search.Spec.Resources),
						SecurityContext: buildDefaultContainerSecurityContext(),
						VolumeMounts:    volumeMounts,
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(mongotGRPCPort)}},
							InitialDelaySeconds: 10, PeriodSeconds: 10,
						},
					}},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: mongotDataVolume},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: storageClassPtr(search.Spec.Storage.StorageClassName),
					Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: search.Spec.Storage.Size}},
				},
			}},
		},
	}
}

// MongotSetParameterArgs — source mongod 에 주입할 mongot 연동 setParameter.
// endpoint 비어있으면 빈 slice → mongod template 무변경(무롤링).
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

// storageClassPtr — 빈 문자열이면 nil(기본 StorageClass).
func storageClassPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// sourceMongodSelector — mongot netpol peer 로 쓸 source mongod RS pod selector.
// buildLabels(replicaset) 로 *해당 source* mongod 만 한정(cluster-wide 노출 회피 + goconst).
func sourceMongodSelector(search *mongodbv1beta1.MongoDBSearch) map[string]string {
	name := ""
	if search.Spec.Source.MongoDBResourceRef != nil {
		name = search.Spec.Source.MongoDBResourceRef.Name
	}
	return buildLabels(name, "replicaset")
}

// BuildMongotNetworkPolicy — mongot 의 ingress/egress allow(peer 제한).
// 컷오버 교훈(default-deny namespace 가 워크로드 차단) 정합 — data ns 가 default-deny
// 여도 mongot↔mongod 통신만 명시 allow. ingress: source mongod → 27028/27029,
// egress: source mongod 27017(동기) + 53(DNS). peer 없는 전체 허용 금지(cross-review).
func BuildMongotNetworkPolicy(search *mongodbv1beta1.MongoDBSearch) *netv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	udp := corev1.ProtocolUDP
	grpcPort := intstr.FromInt(mongotGRPCPort)
	healthPort := intstr.FromInt(mongotHealthPort)
	mongodPort := intstr.FromInt(mongoDBPort)
	dnsPort := intstr.FromInt(53)
	mongodPeer := []netv1.NetworkPolicyPeer{{
		PodSelector: &metav1.LabelSelector{MatchLabels: sourceMongodSelector(search)},
	}}
	return &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      search.Name + "-mongot",
			Namespace: search.Namespace,
			Labels:    mongotLabels(search.Name),
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: mongotLabels(search.Name)},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress},
			Ingress: []netv1.NetworkPolicyIngressRule{{
				From: mongodPeer,
				Ports: []netv1.NetworkPolicyPort{
					{Protocol: &tcp, Port: &grpcPort},
					{Protocol: &tcp, Port: &healthPort},
				},
			}},
			Egress: []netv1.NetworkPolicyEgressRule{
				{To: mongodPeer, Ports: []netv1.NetworkPolicyPort{{Protocol: &tcp, Port: &mongodPort}}},
				{Ports: []netv1.NetworkPolicyPort{{Protocol: &udp, Port: &dnsPort}, {Protocol: &tcp, Port: &dnsPort}}},
			},
		},
	}
}
