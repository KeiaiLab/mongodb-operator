/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	commonslabels "github.com/keiailab/keiailab-commons/pkg/labels"
	commonsnp "github.com/keiailab/keiailab-commons/pkg/networkpolicy"
	"github.com/keiailab/keiailab-commons/pkg/probes"
	commonstopology "github.com/keiailab/keiailab-commons/pkg/topology"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/assets"
	auditpkg "github.com/keiailab/mongodb-operator/internal/controller/audit"
	authpkg "github.com/keiailab/mongodb-operator/internal/controller/auth"
	encryptionpkg "github.com/keiailab/mongodb-operator/internal/controller/encryption"
	secv2 "github.com/keiailab/mongodb-operator/internal/security"
)

const (
	mongoDBPort  = 27017
	metricsPort  = 9216
	defaultImage = "mongo:8.3.1"
	// exporterImage — chart values / examples (0.51.0) 와 코드 const (0.40) 의
	// 3-way drift 를 최신 쪽 0.51.0 단일 진실원으로 통일 (kubebuilder default 동기).
	exporterImage = "percona/mongodb_exporter:0.51.0"
	// keyfileInitImage 는 copy-keyfile init container (4곳: replicaset / cfg / shard / mongos)
	// 의 단일 진실원. busybox 만 사용 (chmod + cp), CVE 패치 시 본 const 만 갱신.
	keyfileInitImage = "busybox:1.37"
)

// Helper functions
func generateRandomKey(length int) string {
	bytes := make([]byte, length)
	_, _ = rand.Read(bytes)
	return base64.StdEncoding.EncodeToString(bytes)
}

func getMongoDBImage(version mongodbv1alpha1.MongoDBVersion) string {
	if version.Image != "" {
		return version.Image
	}
	return fmt.Sprintf("mongo:%s", version.Version)
}

// MongoTLSMountPath 는 cert-manager 발급 Secret 의 raw mount 경로.
const MongoTLSMountPath = "/etc/ssl/mongo"

// MongoTLSPEMPath 는 init container 가 만든 PEM merge file 의 경로.
// mongod --tlsCertificateKeyFile 가 단일 PEM (cert + key) 를 요구.
const MongoTLSPEMPath = "/etc/ssl/mongo-pem"

// BuildPEMMergeInitContainer 는 cert-manager Secret 의 tls.crt + tls.key 를
// 단일 PEM file 로 합치는 init container 를 반환한다 (mongod 의 --tlsCertificateKeyFile
// 호환). PSA restricted 정합 (busybox + RunAsUser 999 + drop ALL + seccomp RuntimeDefault).
//
// Ownership 은 root chown 이 아니라 PodSecurityContext.FSGroup (=999,
// buildDefaultSecurityContext) 에 위임한다. fsGroup 이 emptyDir `/tls-pem` 을
// gid 999 로 group-writable 하게 만들어 UID 999 init container 가 server.pem 을
// 직접 write → file owner 999:999 → mongod (UID 999) read 가능. copy-keyfile init
// 과 동일 패턴 (buildKeyfileInitContainerSecurityContext).
//
// 회귀 주의: 과거 RunAsUser:0 + CHOWN cap 구현 (2026-05-16 chown race 오진) 은
// PSA restricted enforce namespace 에서 pod 생성 거부 (CHOWN cap / runAsUser=0 /
// seccomp 누락) → StatefulSet rollout 영구 차단 (2026-05-30 KeiaiLab data ns 사고).
// fsGroup 기반이 정합 (라이브 검증: data ns restricted enforce + cfg/shard/mongos 3/3).
//
// Exported — caller (cfg/shard/mongos reconciler) 가 STS build 후 conditional append.
func BuildPEMMergeInitContainer() corev1.Container {
	return corev1.Container{
		Name:  "tls-pem-merge",
		Image: keyfileInitImage, // busybox:1.37 const 단일화 정합
		Command: []string{
			"sh", "-c",
			// 멱등(idempotent) — pod/init 재시작 시 emptyDir 의 기존 server.pem(0400)을
			// `> server.pem` 으로 덮어쓰면 owner write 권한 부재로 "Permission denied" 크래시.
			// (실 사고 2026-06-22: node/kubelet 재기동 후 init 재실행 → tls-pem-merge
			// CrashLoopBackOff → pod not-ready → 클러스터 회복 정체.) temp 파일에 쓴 뒤
			// atomic `mv -f` 로 교체 — rename 은 dir write(fsGroup=999)만 필요, 대상 파일 0400
			// 무관. .tmp 는 chmod 하지 않아 재실행 시 항상 overwrite 가능.
			"cat /tls-input/tls.crt /tls-input/tls.key > /tls-pem/server.pem.tmp && " +
				"mv -f /tls-pem/server.pem.tmp /tls-pem/server.pem && " +
				"chmod 0400 /tls-pem/server.pem",
		},
		SecurityContext: buildKeyfileInitContainerSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "tls-server", MountPath: "/tls-input", ReadOnly: true},
			{Name: "tls-server-pem", MountPath: "/tls-pem"},
		},
	}
}

// buildTLSPEMVolume 는 init container 와 mongod 가 공유하는 emptyDir.
func buildTLSPEMVolume() corev1.Volume {
	return corev1.Volume{
		Name:         "tls-server-pem",
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
}

// buildTLSPEMMount 는 mongod container 의 mount.
func buildTLSPEMMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      "tls-server-pem",
		MountPath: MongoTLSPEMPath,
		ReadOnly:  true,
	}
}

// tlsArgs 는 mongod TLS 활성 args. preferTLS mode = plaintext + TLS 양쪽 listen
// → operator client (keyfile + plaintext) 와 외부 TLS client 양쪽 호환. cycle 19
// Phase 3b 의 *진정 무중단* 정공.
//
// tlsAllowConnectionsWithoutCertificates: client cert 없는 plaintext connection 허용
// (mTLS 비강제). cycle 19 valkey-operator 의 tls-auth-clients=yes 강제 패턴 회피.
//
// tlsAllowInvalidHostnames (v1.4.17 fix, cycle 19 last): mongos 가 cfg replica set 에
// outbound TLS connect 시 hostname verification 을 우회. mongos --configdb 의 connection
// string 은 short hostname (keiailab-mongo-cfg-0:27017) 사용 — cert SAN 의 wildcard FQDN
// (*.keiailab-mongo-cfg-headless.<ns>.svc.cluster.local) 와 직접 매치 안 됨 → TLS verify
// fail → sharding pool init 실패 → 27017 미 listen → kubelet liveness kill cascade.
// cluster-internal CA chain + preferTLS 환경에서 hostname 검증은 의미 적음 (CA 로 ID
// 검증 충분), short/long hostname mix 흡수 의무.
//
//lint:ignore U1000 StatefulSet PVC template 통합 예정 helper 보존
func buildDataVolumeClaimTemplate(storage mongodbv1alpha1.StorageSpec) corev1.PersistentVolumeClaim {
	accessModes := storage.AccessModes
	if len(accessModes) == 0 {
		accessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}
	storageClassName := ptr.To(storage.StorageClassName)
	if storage.StorageClassName == "" {
		storageClassName = nil
	}
	pvc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			StorageClassName: storageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storage.Size,
				},
			},
		},
	}
	if storage.Selector != nil {
		pvc.Spec.Selector = storage.Selector
	}
	return pvc
}

func tlsArgs(tls *mongodbv1alpha1.TLSSpec) []string {
	mode := "preferTLS"
	if tls != nil && tls.Mode != "" {
		mode = tls.Mode
	}
	args := []string{
		"--tlsMode", mode,
		"--tlsCertificateKeyFile", MongoTLSPEMPath + "/server.pem",
		"--tlsCAFile", MongoTLSMountPath + "/ca.crt",
		"--tlsAllowConnectionsWithoutCertificates",
	}
	if tls == nil || tls.AllowInvalidHostnames == nil || *tls.AllowInvalidHostnames {
		args = append(args, "--tlsAllowInvalidHostnames")
	}
	return args
}

// buildTLSServerVolume 은 cert-manager 가 발급한 server cert Secret (<name>-tls)
// 을 mount 하는 Volume 을 반환한다. tlsEnabled=false 시 nil.
//
// defaultMode 0o400: mongo user read-only. mongod 의 키 파일 권한 검사 통과.
// Phase 3a 는 volume mount 만 추가 — mongod args 의 --tlsCertificateKeyFile 통합은
// Phase 3b 에서 init container PEM merge (cat tls.crt tls.key > server.pem) 후.
func buildTLSServerVolume(secretName string) corev1.Volume {
	return corev1.Volume{
		Name: "tls-server",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  secretName,
				DefaultMode: ptr.To[int32](0o400),
			},
		},
	}
}

// buildTLSServerMount 는 tls-server Volume 의 mountPath 를 반환한다.
func buildTLSServerMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      "tls-server",
		MountPath: MongoTLSMountPath,
		ReadOnly:  true,
	}
}

// buildLabels — keiailab-commons/pkg/labels 위임 (iteration 27).
// 기존 4-key map (no version, no part-of) 동작 보존 — labels.Set.All() 의 optional
// 필드 omit 동작 활용.
func buildLabels(name, component string) map[string]string {
	return commonslabels.Set{
		Name:      "mongodb",
		Instance:  name,
		Component: component,
		ManagedBy: "mongodb-operator",
		// Version + PartOf 미지정 — All() 에서 자동 omit (기존 동작 보존).
	}.All()
}

func buildResourceRequirements(spec mongodbv1alpha1.ResourcesSpec) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: spec.Requests,
		Limits:   spec.Limits,
	}
}

// pod-level 기본 SecurityContext — commons pkg/security 위임 (v0.11.0).
// 기존 수기 작성 (FSGroup/RunAsUser/RunAsGroup=999 + RunAsNonRoot) 대비
// pod-level seccompProfile=RuntimeDefault 가 추가된다 (PSA restricted 정합 —
// container-level 은 RestrictedContainer 로 이미 적용 중이라 의미 변화 없음.
// 단 pod template hash 변경으로 operator 업그레이드 시 1회 rolling restart).
func buildDefaultSecurityContext() *corev1.PodSecurityContext {
	return secv2.DefaultPodSecurityContext()
}

func buildDefaultContainerSecurityContext() *corev1.SecurityContext {
	return secv2.DefaultContainerSecurityContext()
}

// PodSecurity "restricted" 정책을 만족하는 init container용 SecurityContext.
// keyfile 복사 init container 4곳 (replicaset / config server / shard / mongos) 에서
// 동일 정의가 인라인 중복되어 있던 것을 commons 단일 진실원으로 위임.
// 클러스터 사고 (2026-05-07): copy-keyfile container 가 capabilities.drop 과
// seccompProfile 누락으로 PodSecurity restricted 위반 → StatefulSet pod 생성 거부.
// iteration 8: keiailab-commons/pkg/security 로 통합 — 3 operator 가 동일 패턴 채택.
func buildKeyfileInitContainerSecurityContext() *corev1.SecurityContext {
	return secv2.KeyfileInitSecurityContext()
}

// BuildKeyfileSecret creates a keyfile secret for MongoDB internal auth
func BuildKeyfileSecret(mdb *mongodbv1alpha1.MongoDB) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdb.Name + "-keyfile",
			Namespace: mdb.Namespace,
			Labels:    buildLabels(mdb.Name, "keyfile"),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"keyfile": []byte(generateRandomKey(756)),
		},
	}
}

// BuildShardedKeyfileSecret creates a keyfile secret for MongoDBSharded
func BuildShardedKeyfileSecret(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-keyfile",
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, "keyfile"),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"keyfile": []byte(generateRandomKey(756)),
		},
	}
}

// buildReadinessScript는 mongod ping을 수행하는 readiness probe 스크립트를 만든다.
// port가 다르면(cfg=27019, shard=27018, replicaset/mongos=27017) --port 인자가 필요.
func buildReadinessScript(port int) string {
	// assets/scripts/readiness.sh.tpl로 외부화. embed error는 발생할 수 없는
	// 상황(컴파일 타임 검증)이라 panic이 안전.
	out, err := assets.RenderReadiness(port)
	if err != nil {
		panic(fmt.Sprintf("render readiness script: %v", err))
	}
	return out
}

// buildAdminBootstrapScript는 lifecycle.postStart에서 localhost-exception을 사용해
// RS init과 첫 admin user를 한 번에 부트스트랩하는 스크립트를 반환한다.
//
// 본 스크립트가 두 단계를 모두 책임지는 이유 — mongod이 `--auth+--keyFile`로 시작
// 하면 *모든 admin 명령*이 인증을 요구한다. localhost-exception은 *createUser*에만
// 적용되고 *replSetInitiate*는 적용되지 않는다. 그러나 두 명령 모두 *pod 내부의
// localhost connect*에서는 mongod이 첫 user 생성 직전까지 익명 호출을 허용한다.
// 따라서 외부 connect로는 풀 수 없는 부트스트랩 deadlock이 pod 내부 스크립트로는
// 한 번에 풀린다(2026-04-29 검증).
//
// 동작 분기:
//   - 모든 멤버: mongod ping 대기.
//   - ordinal-0(HOSTNAME suffix `-0`): rs.initiate(idempotent) → PRIMARY 대기 →
//     createUser(idempotent). RS init은 단 한 번만, 다른 멤버는 oplog로 자동 합류.
//   - 기타 ordinal: skip(secondary는 cfg propagate로 자동 RS 합류).
//
// 환경변수(STS template에서 주입):
//
//	MONGO_PORT      — 27017(RS) / 27019(cfg) / 27018(shard)
//	MONGO_REPLSET   — RS 이름
//	MONGO_MEMBERS   — 콤마 list of <pod-FQDN>:<port>
//	MONGO_CONFIGSVR — "true"이면 rs.initiate에 configsvr:true 추가
//
// 보안:
//   - password는 Secret Volume(/etc/mongodb-admin/password)에서 fs.readFileSync로
//     읽음. ps/audit 로그 노출 없음, JS literal 인젝션 차단.
//   - 이미 RS init/user가 있으면 idempotent no-op.
func buildAdminBootstrapScript(port int) string {
	// assets/scripts/bootstrap-admin.sh.tpl로 외부화.
	out, err := assets.RenderBootstrap(port)
	if err != nil {
		panic(fmt.Sprintf("render bootstrap script: %v", err))
	}
	return out
}

// buildAdminCredentialsVolume은 admin password Secret을 0400으로 mount하는 Volume을
// 만든다. 호출자는 secretName이 비어있지 않은지 미리 검증해야 한다.
func buildAdminCredentialsVolume(secretName string) corev1.Volume {
	return corev1.Volume{
		Name: "admin-credentials",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  secretName,
				DefaultMode: ptr.To[int32](0400),
			},
		},
	}
}

// buildAdminCredentialsMount는 /etc/mongodb-admin 경로에 admin Secret을
// read-only로 마운트하는 VolumeMount를 반환한다. bootstrap 스크립트는
// /etc/mongodb-admin/password를 읽는다.
func buildAdminCredentialsMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      "admin-credentials",
		MountPath: "/etc/mongodb-admin",
		ReadOnly:  true,
	}
}

// buildScriptsVolume은 ConfigMap을 0755 실행 권한으로 mount하는 Volume을 만든다.
func buildScriptsVolume(configMapName string) corev1.Volume {
	return corev1.Volume{
		Name: "scripts",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMapName,
				},
				DefaultMode: ptr.To[int32](0755),
			},
		},
	}
}

// buildScriptsMount는 /scripts 경로에 ConfigMap을 read-only로 마운트한다.
func buildScriptsMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: "scripts", MountPath: "/scripts", ReadOnly: true}
}

// buildBootstrapEnv는 RS init과 admin user 생성을 자동화하는 postStart 스크립트가
// 사용할 환경변수를 만든다(buildAdminBootstrapScript 참조).
//
// stsName: StatefulSet 이름 = pod 호스트네임 prefix.
// headlessSvc: pod의 headless Service 이름.
// replSetName: RS 식별자.
// replicas: 멤버 수 (RS init members[].host 갯수).
// port: 27017(RS) / 27019(cfg) / 27018(shard).
// configsvr: rs.initiate(cfg.configsvr=true) 추가 여부.
func buildBootstrapEnv(stsName, headlessSvc, namespace, replSetName string, replicas int32, port int, configsvr bool) []corev1.EnvVar {
	fqdns := make([]string, replicas)
	for i := int32(0); i < replicas; i++ {
		fqdns[i] = fmt.Sprintf("%s-%d.%s.%s.svc.cluster.local:%d", stsName, i, headlessSvc, namespace, port)
	}
	cfgFlag := ""
	if configsvr {
		cfgFlag = "true"
	}
	return []corev1.EnvVar{
		{Name: "MONGO_PORT", Value: fmt.Sprintf("%d", port)},
		{Name: "MONGO_REPLSET", Value: replSetName},
		{Name: "MONGO_MEMBERS", Value: strings.Join(fqdns, ",")},
		{Name: "MONGO_CONFIGSVR", Value: cfgFlag},
	}
}

// buildAdminBootstrapLifecycle은 /scripts/bootstrap-admin.sh를 postStart로 실행하는
// Lifecycle hook을 반환한다. ReplicaSet/ConfigServer/Shard StatefulSet 공용.
func buildAdminBootstrapLifecycle() *corev1.Lifecycle {
	return &corev1.Lifecycle{
		PostStart: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"/scripts/bootstrap-admin.sh"},
			},
		},
	}
}

// BuildMongoDBConfigMap creates a ConfigMap for MongoDB configuration.
//
// 포함 스크립트:
//   - readiness-probe.sh: mongod이 ping에 응답하는지 확인.
//   - bootstrap-admin.sh: pod 자체가 자기 mongod에 localhost connection으로
//     첫 admin user를 생성. operator는 더 이상 pods/exec을 수행하지 않는다.
func BuildMongoDBConfigMap(mdb *mongodbv1alpha1.MongoDB) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdb.Name + "-scripts",
			Namespace: mdb.Namespace,
			Labels:    buildLabels(mdb.Name, "scripts"),
		},
		Data: map[string]string{
			"readiness-probe.sh": buildReadinessScript(mongoDBPort),
			"bootstrap-admin.sh": buildAdminBootstrapScript(mongoDBPort),
		},
	}
}

// BuildCustomConfigMap generates a ConfigMap from spec.pod.customConfig.configInline.
// Returns nil if customConfig is nil or configInline is empty.
func BuildCustomConfigMap(name, namespace string, pod *mongodbv1alpha1.PodSpec) *corev1.ConfigMap {
	if pod == nil || pod.CustomConfig == nil || pod.CustomConfig.ConfigInline == "" {
		return nil
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-custom-config",
			Namespace: namespace,
			Labels:    buildLabels(name, "custom-config"),
		},
		Data: map[string]string{
			"mongod.conf": pod.CustomConfig.ConfigInline,
		},
	}
}

// BuildConfigServerScriptsConfigMap는 Config Server StatefulSet에 마운트되는
// scripts ConfigMap을 만든다. port=27019.
func BuildConfigServerScriptsConfigMap(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-cfg-scripts",
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, "configsvr"),
		},
		Data: map[string]string{
			"readiness-probe.sh": buildReadinessScript(27019),
			"bootstrap-admin.sh": buildAdminBootstrapScript(27019),
		},
	}
}

// BuildShardScriptsConfigMap는 Shard StatefulSet에 마운트되는 scripts ConfigMap을
// 만든다. port=27018, name={instance}-shard-{i}-scripts.
func BuildShardScriptsConfigMap(mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-shard-%d-scripts", mdbsh.Name, shardIndex),
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", shardIndex)),
		},
		Data: map[string]string{
			"readiness-probe.sh": buildReadinessScript(27018),
			"bootstrap-admin.sh": buildAdminBootstrapScript(27018),
		},
	}
}

// BuildHeadlessService creates a headless service for StatefulSet
func BuildHeadlessService(mdb *mongodbv1alpha1.MongoDB) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdb.Name + "-headless",
			Namespace: mdb.Namespace,
			Labels:    buildLabels(mdb.Name, "headless"),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  buildLabels(mdb.Name, "replicaset"),
			Ports: []corev1.ServicePort{
				{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
			},
			PublishNotReadyAddresses: true,
		},
	}
}

// BuildClientService creates a client service for MongoDB access
func BuildClientService(mdb *mongodbv1alpha1.MongoDB) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdb.Name,
			Namespace: mdb.Namespace,
			Labels:    buildLabels(mdb.Name, "client"),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: buildLabels(mdb.Name, "replicaset"),
			Ports: []corev1.ServicePort{
				{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
				{Name: "metrics", Port: metricsPort, TargetPort: intstr.FromInt(metricsPort)},
			},
		},
	}
}

// BuildReplicaSetStatefulSet creates a StatefulSet for MongoDB ReplicaSet.
// cycle 15: integrated auth/encryption/audit/PodSpec/sidecars in one builder;
// refactor to per-concern helpers is cycle 16 follow-up.
//
//nolint:gocyclo
func BuildReplicaSetStatefulSet(mdb *mongodbv1alpha1.MongoDB) *appsv1.StatefulSet {
	labels := buildLabels(mdb.Name, "replicaset")

	// Build mongod args
	args := []string{
		"--replSet", mdb.Spec.ReplicaSetName,
		"--bind_ip_all",
		"--auth",
		"--keyFile", "/etc/mongodb-keyfile/keyfile",
	}

	// Volumes
	volumes := []corev1.Volume{
		{
			Name: "keyfile-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  mdb.Name + "-keyfile",
					DefaultMode: ptr.To[int32](0400),
				},
			},
		},
		{
			Name: "keyfile",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		buildScriptsVolume(mdb.Name + "-scripts"),
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "data", MountPath: mdb.Spec.Storage.DataDirPath},
		{Name: "keyfile", MountPath: "/etc/mongodb-keyfile", ReadOnly: true},
		buildScriptsMount(),
	}

	// admin credentials Secret을 file로 마운트한다. lifecycle.postStart의 bootstrap
	// 스크립트가 /etc/mongodb-admin/password를 읽어 첫 admin user를 생성한다.
	// envvar로 노출하지 않으므로 ps/audit에 password가 보이지 않는다.
	if mdb.Spec.Auth.AdminCredentialsSecretRef.Name != "" {
		volumes = append(volumes, buildAdminCredentialsVolume(mdb.Spec.Auth.AdminCredentialsSecretRef.Name))
		volumeMounts = append(volumeMounts, buildAdminCredentialsMount())
	}

	// Pillar P7 Phase 3a+3b — TLS server cert mount + PEM merge + mongod args.
	// preferTLS mode = plaintext + TLS 양쪽 listen → operator client (keyfile +
	// plaintext) 와 외부 TLS client 양쪽 호환 (rolling 무중단).
	if mdb.Spec.TLS != nil && mdb.Spec.TLS.Enabled {
		volumes = append(volumes,
			buildTLSServerVolume(mdb.Name+"-tls"),
			buildTLSPEMVolume(),
		)
		volumeMounts = append(volumeMounts,
			buildTLSServerMount(),
			buildTLSPEMMount(),
		)
		args = append(args, tlsArgs(mdb.Spec.TLS)...)
	}

	// cycle 13 (실 통합): auth (LDAP/OIDC) + encryption (KMS) + audit args 를
	// mongod CLI 에 주입. nil spec 은 빈 slice 반환 → noop.
	if mdb.Spec.Auth.LDAP != nil {
		args = append(args, authpkg.LDAPMongodArgs(mdb.Spec.Auth.LDAP)...)
	}
	if mdb.Spec.Auth.OIDC != nil {
		if oidcParam, err := authpkg.OIDCMongodSetParameter(mdb.Spec.Auth.OIDC); err == nil && oidcParam != "" {
			args = append(args, "--setParameter", "oidcIdentityProviders="+oidcParam)
		}
	}
	if mdb.Spec.Storage.Encryption != nil {
		args = append(args, encryptionpkg.MongodArgs(mdb.Spec.Storage.Encryption)...)
	}
	if mdb.Spec.AuditLog != nil {
		args = append(args, auditpkg.MongodArgs(mdb.Spec.AuditLog)...)
	}

	// mongot(MongoDB Search) sidecar 연동: image annotation 있을 때만 sidecar 컨테이너 +
	// init(password 0400) + volumes + setParameter(mongotHost=localhost:27028) 주입.
	// annotation 부재 시 무변경 = search opt-in 무롤링(mongot_test 검증). Community mongot 은
	// localhost mongod 로 topology 연결 → mongod pod sidecar 필수(별도 STS 비호환, kind e2e 실증).
	var mongotSidecar, mongotInit *corev1.Container
	if img := mdb.Annotations[MongotSidecarImageAnnotation]; img != "" {
		mc, ic, mvols := MongotSidecar(mdb.Name, img, mdb.Annotations[MongotSyncSecretAnnotation])
		mongotSidecar, mongotInit = &mc, &ic
		volumes = append(volumes, mvols...)
		args = append(args, MongotSetParameterArgs(fmt.Sprintf("localhost:%d", mongotGRPCPort), mdb.Annotations[MongotTLSModeAnnotation])...)
	}

	// Init container to copy keyfile with correct permissions
	// Runs as mongodb user (999) and uses FSGroup for proper file ownership
	initContainers := []corev1.Container{
		{
			Name:  "copy-keyfile",
			Image: keyfileInitImage,
			Command: []string{
				"sh", "-c",
				"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
				{Name: "keyfile", MountPath: "/keyfile"},
			},
			SecurityContext: buildKeyfileInitContainerSecurityContext(),
		},
	}
	// Pillar P7 Phase 3b — PEM merge init container (replicaset).
	if mdb.Spec.TLS != nil && mdb.Spec.TLS.Enabled {
		initContainers = append(initContainers, BuildPEMMergeInitContainer())
	}

	// cycle 13: VolumePermissions init container (PSA restricted PVC ownership).
	if mdb.Spec.Pod != nil && mdb.Spec.Pod.VolumePermissions != nil && mdb.Spec.Pod.VolumePermissions.Enabled {
		initContainers = append(initContainers, buildVolumePermissionsInit(mdb.Spec.Pod.VolumePermissions))
	}

	// cycle 13: PodSpec.InitContainers (user-provided) chain — operator init 다음.
	if mdb.Spec.Pod != nil && len(mdb.Spec.Pod.InitContainers) > 0 {
		initContainers = append(initContainers, mdb.Spec.Pod.InitContainers...)
	}
	// mongot sidecar init(password 0400 복사) — 위 mongot 블록에서 수집(annotation 시).
	if mongotInit != nil {
		initContainers = append(initContainers, *mongotInit)
	}

	// MongoDB container
	containers := []corev1.Container{
		{
			Name:  "mongodb",
			Image: getMongoDBImage(mdb.Spec.Version),
			Ports: []corev1.ContainerPort{
				{Name: "mongodb", ContainerPort: mongoDBPort, Protocol: corev1.ProtocolTCP},
			},
			Args:            args,
			VolumeMounts:    volumeMounts,
			Resources:       buildResourceRequirements(mdb.Spec.Resources),
			SecurityContext: buildDefaultContainerSecurityContext(),
			LivenessProbe: probes.New().
				Exec("mongosh", "--quiet", "--eval", "db.adminCommand('ping')").
				InitialDelay(30 * time.Second).
				Period(10 * time.Second).
				Timeout(5 * time.Second).
				FailureThreshold(6).
				Build(),
			ReadinessProbe: probes.New().
				Exec("/scripts/readiness-probe.sh").
				InitialDelay(5 * time.Second).
				Period(10 * time.Second).
				Timeout(5 * time.Second).
				Build(),
			// pod 자체가 자기 mongod에 localhost 연결로 첫 admin user를 생성한다.
			// operator는 pods/exec을 호출하지 않는다. 스크립트가 실패해도 mongod
			// 시작 자체는 멈추지 않으며, 운영자는 readiness 미달 → reconcile
			// requeue로 인지한다.
			Lifecycle: buildAdminBootstrapLifecycle(),
			Env: append([]corev1.EnvVar{
				{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
				}},
				{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
				}},
			}, buildBootstrapEnv(mdb.Name, mdb.Name+"-headless", mdb.Namespace, mdb.Spec.ReplicaSetName, mdb.Spec.Members, mongoDBPort, false)...),
		},
	}

	// 4.8 Diagnostic mode — mongod 컨테이너를 sleep infinity 로 교체. Probe /
	// Lifecycle 비활성화로 pod 가 기동 실패 없이 Running 유지 → exec 진단 가능.
	if mdb.Spec.Pod != nil && mdb.Spec.Pod.DiagnosticMode != nil && mdb.Spec.Pod.DiagnosticMode.Enabled {
		applyDiagnosticMode(&containers[0])
	}

	// cycle 13: PodSpec extension 의 mongod 컨테이너 merge.
	if mdb.Spec.Pod != nil {
		applyPodSpecExtensions(&containers[0], mdb.Spec.Pod)
	}
	// cycle 13: ResourcesPreset (Resources 가 비어있을 때만 적용).
	if mdb.Spec.Pod != nil && mdb.Spec.Pod.ResourcesPreset != "" && isResourcesEmpty(mdb.Spec.Resources) {
		containers[0].Resources = ResourcePreset(mdb.Spec.Pod.ResourcesPreset)
	}

	// Add exporter sidecar if monitoring enabled
	if mdb.Spec.Monitoring != nil && mdb.Spec.Monitoring.Enabled {
		exporterImg := exporterImage
		//lint:ignore SA1019 ADR-0018 Phase 1: deprecated Exporter 필드는 Phase 2 결정 전까지 읽기 호환성 보존.
		if mdb.Spec.Monitoring.Exporter != nil && mdb.Spec.Monitoring.Exporter.Image != "" { //nolint:staticcheck
			//lint:ignore SA1019 ADR-0018 Phase 1: deprecated Exporter.Image 읽기 호환성 보존.
			exporterImg = mdb.Spec.Monitoring.Exporter.Image //nolint:staticcheck
		}

		containers = append(containers, corev1.Container{
			Name:  "exporter",
			Image: exporterImg,
			Ports: []corev1.ContainerPort{
				{Name: "metrics", ContainerPort: metricsPort, Protocol: corev1.ProtocolTCP},
			},
			Args: []string{
				"--collect-all",
				"--compatible-mode",
			},
			Env: []corev1.EnvVar{
				{
					Name: "MONGODB_URI",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: mdb.Name + "-exporter-uri",
							},
							Key: "uri",
						},
					},
				},
			},
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
		})
	}

	// cycle 15: PITR oplog tailer sidecar inject (when backup.pitrEnabled=true).
	if IsOplogTailerEnabled(mdb.Spec.Backup) {
		hasAdmin := mdb.Spec.Auth.AdminCredentialsSecretRef.Name != ""
		containers = append(containers, BuildOplogTailerSidecar(mdb.Spec.Version, mongoDBPort, hasAdmin))
		volumes = append(volumes, BuildOplogStagingVolume())
		// Also mount staging on mongod for restore drill.
		containers[0].VolumeMounts = append(containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      oplogStagingVolume,
			MountPath: oplogStagingMount,
		})
	}

	// cycle 15: Audit forwarder fluent-bit sidecar (when CentralForwarder set).
	if mdb.Spec.AuditLog != nil && mdb.Spec.AuditLog.Enabled && mdb.Spec.AuditLog.CentralForwarder != nil {
		containers = append(containers, buildAuditForwarderSidecar(mdb.Spec.AuditLog.CentralForwarder))
		volumes = append(volumes, corev1.Volume{
			Name:         "audit-log",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		auditMount := corev1.VolumeMount{Name: "audit-log", MountPath: "/var/log/mongodb-audit"}
		containers[0].VolumeMounts = append(containers[0].VolumeMounts, auditMount)
	}

	// Security context
	securityContext := buildDefaultSecurityContext()
	if mdb.Spec.Pod != nil && mdb.Spec.Pod.SecurityContext != nil {
		securityContext = mdb.Spec.Pod.SecurityContext
	}

	// Storage class - use nil for cluster default if not specified
	var storageClassName *string
	if mdb.Spec.Storage.StorageClassName != "" {
		storageClassName = &mdb.Spec.Storage.StorageClassName
	}

	// Storage size
	storageSize := mdb.Spec.Storage.Size
	if storageSize.IsZero() {
		storageSize = resource.MustParse("10Gi")
	}

	// mongot sidecar 컨테이너 추가(annotation 시) — 위 mongot 블록에서 수집(Phase 1.1).
	if mongotSidecar != nil {
		containers = append(containers, *mongotSidecar)
	}
	// cycle 13: PodSpec Sidecars / ExtraVolumes / InitScripts volume append.
	containers, volumes = appendPodSpecPodLevel(containers, volumes, mdb.Spec.Pod)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdb.Name,
			Namespace: mdb.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: mdb.Name + "-headless",
			Replicas:    &mdb.Spec.Members,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   fmt.Sprintf("%d", metricsPort),
					},
				},
				Spec: corev1.PodSpec{
					SecurityContext:           securityContext,
					InitContainers:            initContainers,
					Containers:                containers,
					Volumes:                   volumes,
					Affinity:                  buildDefaultAffinity(mdb.Name),
					TopologySpreadConstraints: commonstopology.Defaulted(nil, mdb.Spec.Members, labels),
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						StorageClassName: storageClassName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: storageSize,
							},
						},
					},
				},
			},
			PersistentVolumeClaimRetentionPolicy: mdb.Spec.Storage.PersistentVolumeClaimRetentionPolicy,
		},
	}
}

// applyPodSpecExtensions 는 PodSpec 의 ExtraVolumeMounts, ExtraEnvVars,
// LifecycleHooks 를 mongod 컨테이너에 merge. Sidecars / ExtraVolumes 는
// 호출자가 pod-level 에 append (별 함수). cycle 13 — F68/F79 builder 통합.
func applyPodSpecExtensions(c *corev1.Container, pod *mongodbv1alpha1.PodSpec) {
	if pod == nil {
		return
	}
	if len(pod.ExtraVolumeMounts) > 0 {
		c.VolumeMounts = append(c.VolumeMounts, pod.ExtraVolumeMounts...)
	}
	if len(pod.ExtraEnvVars) > 0 {
		c.Env = append(c.Env, pod.ExtraEnvVars...)
	}
	// LifecycleHooks merge — operator 의 admin bootstrap postStart 가 항상 우선.
	// user 의 postStart 가 있어도 operator postStart 가 덮음. user preStop 만 cherry-pick.
	if pod.LifecycleHooks != nil && pod.LifecycleHooks.PreStop != nil {
		if c.Lifecycle == nil {
			c.Lifecycle = &corev1.Lifecycle{}
		}
		c.Lifecycle.PreStop = pod.LifecycleHooks.PreStop
	}
	// InitScripts mount (/docker-entrypoint-initdb.d) — cycle 13 F71.
	if pod.InitScripts != nil {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      "init-scripts",
			MountPath: "/docker-entrypoint-initdb.d",
			ReadOnly:  true,
		})
	}
}

// appendPodSpecPodLevel 는 pod-level Sidecars / ExtraVolumes / InitScripts
// volume 을 pod.Spec 에 append. caller 가 PodTemplateSpec 구성 후 호출.
func appendPodSpecPodLevel(containers []corev1.Container, volumes []corev1.Volume, pod *mongodbv1alpha1.PodSpec) ([]corev1.Container, []corev1.Volume) {
	if pod == nil {
		return containers, volumes
	}
	if len(pod.Sidecars) > 0 {
		containers = append(containers, pod.Sidecars...)
	}
	if len(pod.ExtraVolumes) > 0 {
		volumes = append(volumes, pod.ExtraVolumes...)
	}
	// InitScripts → ConfigMap/Secret projected volume.
	if pod.InitScripts != nil {
		if pod.InitScripts.ConfigMapRef != nil {
			volumes = append(volumes, corev1.Volume{
				Name: "init-scripts",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: *pod.InitScripts.ConfigMapRef,
						DefaultMode:          ptr.To[int32](0o555),
					},
				},
			})
		} else if pod.InitScripts.SecretRef != nil {
			volumes = append(volumes, corev1.Volume{
				Name: "init-scripts",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  pod.InitScripts.SecretRef.Name,
						DefaultMode: ptr.To[int32](0o555),
					},
				},
			})
		}
	}
	return containers, volumes
}

// isResourcesEmpty 는 ResourcesSpec 이 사용자 설정 0 인지 (preset 우선 가능 판단).
func isResourcesEmpty(r mongodbv1alpha1.ResourcesSpec) bool {
	return len(r.Requests) == 0 && len(r.Limits) == 0
}

// buildAuditForwarderSidecar — cycle 15. fluent-bit sidecar 로 audit log 를
// Loki/Elasticsearch/OpenSearch 로 forward. /var/log/mongodb-audit 를 read.
func buildAuditForwarderSidecar(fwd *mongodbv1alpha1.AuditForwarderSpec) corev1.Container {
	return corev1.Container{
		Name:  "audit-forwarder",
		Image: "fluent/fluent-bit:3.2",
		Command: []string{
			"/fluent-bit/bin/fluent-bit",
			"-c", "/fluent-bit/etc/fluent-bit.conf",
		},
		Env: []corev1.EnvVar{
			{Name: "FORWARDER_TYPE", Value: fwd.Type},
			{Name: "FORWARDER_URL", Value: fwd.URL},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "audit-log", MountPath: "/var/log/mongodb-audit", ReadOnly: true},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		SecurityContext: buildDefaultContainerSecurityContext(),
	}
}

// buildVolumePermissionsInit 는 PSA restricted 환경에서 PVC 의 ownership 을
// mongodb user (uid 999, gid 999) 로 정합시키는 init container 를 생성한다.
// cycle 13 — F70 builder 통합.
func buildVolumePermissionsInit(spec *mongodbv1alpha1.VolumePermissionsSpec) corev1.Container {
	img := spec.Image
	if img == "" {
		img = keyfileInitImage
	}
	return corev1.Container{
		Name:  "volume-permissions",
		Image: img,
		// 결함 #3: root chown (RunAsUser=0 + CHOWN cap) 은 PSA restricted 위반.
		// pod-level fsGroup=999 (buildDefaultSecurityContext) 가 PVC ownership 을
		// 이미 999 로 정합시키므로, 비특권 uid 999 로 자기 소유 파일에 chown 하면
		// CAP_CHOWN 없이 성공한다. 이미 정합된 경우 no-op success — chmod 만 실효.
		Command: []string{
			"sh", "-c",
			"chown -R 999:999 /data/db 2>/dev/null || true; chmod -R 0755 /data/db",
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "data", MountPath: "/data/db"},
		},
		Resources:       buildResourceRequirements(spec.Resources),
		SecurityContext: buildKeyfileInitContainerSecurityContext(),
	}
}

// applyDiagnosticMode 는 mongod 컨테이너를 진단용 sleep infinity 로 교체한다.
// command/args 가 sleep 으로 바뀌면 mongod 가 기동하지 않으므로 probe / lifecycle
// 도 동시 비활성화해야 한다 (그대로 두면 probe 실패로 컨테이너 무한 재시작).
func applyDiagnosticMode(c *corev1.Container) {
	c.Command = []string{"sleep", "infinity"}
	c.Args = nil
	c.LivenessProbe = nil
	c.ReadinessProbe = nil
	c.StartupProbe = nil
	c.Lifecycle = nil
}

func buildDefaultAffinity(instanceName string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/instance": instanceName,
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		},
	}
}

// BuildConfigServerService creates a headless service for Config Server
func BuildConfigServerService(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.Service {
	labels := buildLabels(mdbsh.Name, "configsvr")
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-cfg-headless",
			Namespace: mdbsh.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  labels,
			Ports: []corev1.ServicePort{
				{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
			},
			PublishNotReadyAddresses: true,
		},
	}
}

// BuildConfigServerStatefulSet creates a StatefulSet for Config Server
func BuildConfigServerStatefulSet(mdbsh *mongodbv1alpha1.MongoDBSharded) *appsv1.StatefulSet {
	labels := buildLabels(mdbsh.Name, "configsvr")

	args := []string{
		"--configsvr",
		"--replSet", mdbsh.Name + "-cfg",
		"--bind_ip_all",
		"--auth",
		"--keyFile", "/etc/mongodb-keyfile/keyfile",
	}

	// Storage class - use nil for cluster default if not specified
	var storageClassName *string
	if mdbsh.Spec.ConfigServer.Storage.StorageClassName != "" {
		storageClassName = &mdbsh.Spec.ConfigServer.Storage.StorageClassName
	}

	storageSize := mdbsh.Spec.ConfigServer.Storage.Size
	if storageSize.IsZero() {
		storageSize = resource.MustParse("10Gi")
	}

	// Volumes/Mounts 구성. admin-credentials와 scripts는 AdminCredentialsSecretRef
	// 가 설정된 경우에만 추가하여 옵션 사용을 깨지 않는다.
	volumes := []corev1.Volume{
		{
			Name: "keyfile-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  mdbsh.Name + "-keyfile",
					DefaultMode: ptr.To[int32](0400),
				},
			},
		},
		{
			Name:         "keyfile",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}
	mongodVolumeMounts := []corev1.VolumeMount{
		{Name: "data", MountPath: "/data/configdb"},
		{Name: "keyfile", MountPath: "/etc/mongodb-keyfile", ReadOnly: true},
	}
	var mongodLifecycle *corev1.Lifecycle
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name != "" {
		volumes = append(volumes,
			buildScriptsVolume(mdbsh.Name+"-cfg-scripts"),
			buildAdminCredentialsVolume(mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name),
		)
		mongodVolumeMounts = append(mongodVolumeMounts,
			buildScriptsMount(),
			buildAdminCredentialsMount(),
		)
		mongodLifecycle = buildAdminBootstrapLifecycle()
	}

	// Pillar P7 Phase 3a+3b — TLS volume mount + PEM merge + mongod args (cfg).
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		volumes = append(volumes,
			buildTLSServerVolume(mdbsh.Name+"-tls"),
			buildTLSPEMVolume(),
		)
		mongodVolumeMounts = append(mongodVolumeMounts,
			buildTLSServerMount(),
			buildTLSPEMMount(),
		)
		args = append(args, tlsArgs(mdbsh.Spec.TLS)...)
	}

	// cycle 14 sharded ConfigServer integration: auth / encryption / audit args.
	if mdbsh.Spec.Auth.LDAP != nil {
		args = append(args, authpkg.LDAPMongodArgs(mdbsh.Spec.Auth.LDAP)...)
	}
	if mdbsh.Spec.Auth.OIDC != nil {
		if oidcParam, err := authpkg.OIDCMongodSetParameter(mdbsh.Spec.Auth.OIDC); err == nil && oidcParam != "" {
			args = append(args, "--setParameter", "oidcIdentityProviders="+oidcParam)
		}
	}
	if mdbsh.Spec.ConfigServer.Storage.Encryption != nil {
		args = append(args, encryptionpkg.MongodArgs(mdbsh.Spec.ConfigServer.Storage.Encryption)...)
	}
	if mdbsh.Spec.AuditLog != nil {
		args = append(args, auditpkg.MongodArgs(mdbsh.Spec.AuditLog)...)
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-cfg",
			Namespace: mdbsh.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: mdbsh.Name + "-cfg-headless",
			Replicas:    &mdbsh.Spec.ConfigServer.Members,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					SecurityContext: buildDefaultSecurityContext(),
					InitContainers: []corev1.Container{
						{
							Name:  "copy-keyfile",
							Image: keyfileInitImage,
							Command: []string{
								"sh", "-c",
								"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
								{Name: "keyfile", MountPath: "/keyfile"},
							},
							SecurityContext: buildKeyfileInitContainerSecurityContext(),
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "mongodb",
							Image: getMongoDBImage(mdbsh.Spec.Version),
							Ports: []corev1.ContainerPort{
								{Name: "mongodb", ContainerPort: mongoDBPort},
							},
							Args:            args,
							Resources:       buildResourceRequirements(mdbsh.Spec.ConfigServer.Resources),
							SecurityContext: buildDefaultContainerSecurityContext(),
							VolumeMounts:    mongodVolumeMounts,
							Lifecycle:       mongodLifecycle,
							// Layer 5 modern HA: hang detection. mongosh ping 단순 path
							// (script 의존 0). ConfigServer port 27019.
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mongosh", "--quiet", "--port", "27019", "--eval", "db.adminCommand('ping')"},
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								FailureThreshold:    6,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mongosh", "--quiet", "--port", "27019", "--eval", "db.adminCommand('ping')"},
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
							},
							Env: buildBootstrapEnv(mdbsh.Name+"-cfg", mdbsh.Name+"-cfg-headless", mdbsh.Namespace, mdbsh.Name+"-cfg", mdbsh.Spec.ConfigServer.Members, 27019, true),
						},
					},
					Volumes:  volumes,
					Affinity: buildDefaultAffinity(mdbsh.Name),
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						StorageClassName: storageClassName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceStorage: storageSize},
						},
					},
				},
			},
			PersistentVolumeClaimRetentionPolicy: mdbsh.Spec.ConfigServer.Storage.PersistentVolumeClaimRetentionPolicy,
		},
	}

	// cycle 16: sharded ConfigServer 에도 oplog tailer / audit forwarder sidecar.
	if IsOplogTailerEnabled(mdbsh.Spec.Backup) {
		hasAdmin := mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name != ""
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers,
			BuildOplogTailerSidecar(mdbsh.Spec.Version, 27019, hasAdmin))
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, BuildOplogStagingVolume())
		sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			sts.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: oplogStagingVolume, MountPath: oplogStagingMount},
		)
	}
	if mdbsh.Spec.AuditLog != nil && mdbsh.Spec.AuditLog.Enabled && mdbsh.Spec.AuditLog.CentralForwarder != nil {
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers,
			buildAuditForwarderSidecar(mdbsh.Spec.AuditLog.CentralForwarder))
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "audit-log", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			sts.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "audit-log", MountPath: "/var/log/mongodb-audit"},
		)
	}

	// F-IMP-04 (cycle 0): DiagnosticMode 를 sharded ConfigServer 까지 확장.
	// MongoDB (RS) 와 동일 패턴 — mongod 컨테이너를 sleep infinity 로 교체하여
	// 기동 실패 없이 exec 진단 가능 (probe / lifecycle 도 동시 nil).
	if mdbsh.Spec.ConfigServer.Pod != nil && mdbsh.Spec.ConfigServer.Pod.DiagnosticMode != nil && mdbsh.Spec.ConfigServer.Pod.DiagnosticMode.Enabled {
		applyDiagnosticMode(&sts.Spec.Template.Spec.Containers[0])
	}
	// cycle 14: PodSpec extension merge (ExtraVolumeMounts/ExtraEnvVars/LifecycleHooks).
	if mdbsh.Spec.ConfigServer.Pod != nil {
		applyPodSpecExtensions(&sts.Spec.Template.Spec.Containers[0], mdbsh.Spec.ConfigServer.Pod)
	}
	// cycle 14: ResourcesPreset (Resources 비어있을 때만).
	if mdbsh.Spec.ConfigServer.Pod != nil && mdbsh.Spec.ConfigServer.Pod.ResourcesPreset != "" && isResourcesEmpty(mdbsh.Spec.ConfigServer.Resources) {
		sts.Spec.Template.Spec.Containers[0].Resources = ResourcePreset(mdbsh.Spec.ConfigServer.Pod.ResourcesPreset)
	}
	// cycle 14: pod-level Sidecars + ExtraVolumes + InitScripts volume.
	sts.Spec.Template.Spec.Containers, sts.Spec.Template.Spec.Volumes =
		appendPodSpecPodLevel(sts.Spec.Template.Spec.Containers, sts.Spec.Template.Spec.Volumes, mdbsh.Spec.ConfigServer.Pod)
	// cycle 14: VolumePermissions init container (PSA restricted).
	if mdbsh.Spec.ConfigServer.Pod != nil && mdbsh.Spec.ConfigServer.Pod.VolumePermissions != nil && mdbsh.Spec.ConfigServer.Pod.VolumePermissions.Enabled {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, buildVolumePermissionsInit(mdbsh.Spec.ConfigServer.Pod.VolumePermissions))
	}
	// cycle 14: user-provided InitContainers chain.
	if mdbsh.Spec.ConfigServer.Pod != nil && len(mdbsh.Spec.ConfigServer.Pod.InitContainers) > 0 {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, mdbsh.Spec.ConfigServer.Pod.InitContainers...)
	}
	return sts
}

// BuildShardService creates a headless service for a Shard
func BuildShardService(mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) *corev1.Service {
	name := fmt.Sprintf("%s-shard-%d", mdbsh.Name, shardIndex)
	labels := buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", shardIndex))

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-headless",
			Namespace: mdbsh.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  labels,
			Ports: []corev1.ServicePort{
				{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
			},
			PublishNotReadyAddresses: true,
		},
	}
}

// BuildShardStatefulSet creates a StatefulSet for a Shard
func BuildShardStatefulSet(mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) *appsv1.StatefulSet {
	name := fmt.Sprintf("%s-shard-%d", mdbsh.Name, shardIndex)
	labels := buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", shardIndex))

	args := []string{
		"--shardsvr",
		"--replSet", name,
		"--bind_ip_all",
		"--auth",
		"--keyFile", "/etc/mongodb-keyfile/keyfile",
	}

	// Storage class - use nil for cluster default if not specified
	var storageClassName *string
	if mdbsh.Spec.Shards.Storage.StorageClassName != "" {
		storageClassName = &mdbsh.Spec.Shards.Storage.StorageClassName
	}

	storageSize := mdbsh.Spec.Shards.Storage.Size
	if storageSize.IsZero() {
		storageSize = resource.MustParse("50Gi")
	}

	// ConfigServer와 동일 패턴: admin bootstrap 옵션 사용시 추가 마운트.
	volumes := []corev1.Volume{
		{
			Name: "keyfile-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  mdbsh.Name + "-keyfile",
					DefaultMode: ptr.To[int32](0400),
				},
			},
		},
		{
			Name:         "keyfile",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}
	mongodVolumeMounts := []corev1.VolumeMount{
		{Name: "data", MountPath: "/data/db"},
		{Name: "keyfile", MountPath: "/etc/mongodb-keyfile", ReadOnly: true},
	}
	var mongodLifecycle *corev1.Lifecycle
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name != "" {
		volumes = append(volumes,
			buildScriptsVolume(fmt.Sprintf("%s-shard-%d-scripts", mdbsh.Name, shardIndex)),
			buildAdminCredentialsVolume(mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name),
		)
		mongodVolumeMounts = append(mongodVolumeMounts,
			buildScriptsMount(),
			buildAdminCredentialsMount(),
		)
		mongodLifecycle = buildAdminBootstrapLifecycle()
	}

	// Pillar P7 Phase 3a+3b — TLS volume mount + PEM merge + mongod args (shard).
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		volumes = append(volumes,
			buildTLSServerVolume(mdbsh.Name+"-tls"),
			buildTLSPEMVolume(),
		)
		mongodVolumeMounts = append(mongodVolumeMounts,
			buildTLSServerMount(),
			buildTLSPEMMount(),
		)
		args = append(args, tlsArgs(mdbsh.Spec.TLS)...)
	}

	// cycle 14 sharded Shard integration.
	if mdbsh.Spec.Auth.LDAP != nil {
		args = append(args, authpkg.LDAPMongodArgs(mdbsh.Spec.Auth.LDAP)...)
	}
	if mdbsh.Spec.Auth.OIDC != nil {
		if oidcParam, err := authpkg.OIDCMongodSetParameter(mdbsh.Spec.Auth.OIDC); err == nil && oidcParam != "" {
			args = append(args, "--setParameter", "oidcIdentityProviders="+oidcParam)
		}
	}
	if mdbsh.Spec.Shards.Storage.Encryption != nil {
		args = append(args, encryptionpkg.MongodArgs(mdbsh.Spec.Shards.Storage.Encryption)...)
	}
	if mdbsh.Spec.AuditLog != nil {
		args = append(args, auditpkg.MongodArgs(mdbsh.Spec.AuditLog)...)
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mdbsh.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name + "-headless",
			Replicas:    &mdbsh.Spec.Shards.MembersPerShard,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					SecurityContext: buildDefaultSecurityContext(),
					InitContainers: []corev1.Container{
						{
							Name:  "copy-keyfile",
							Image: keyfileInitImage,
							Command: []string{
								"sh", "-c",
								"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
								{Name: "keyfile", MountPath: "/keyfile"},
							},
							SecurityContext: buildKeyfileInitContainerSecurityContext(),
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "mongodb",
							Image: getMongoDBImage(mdbsh.Spec.Version),
							Ports: []corev1.ContainerPort{
								{Name: "mongodb", ContainerPort: mongoDBPort},
							},
							Args:            args,
							Resources:       buildResourceRequirements(mdbsh.Spec.Shards.Resources),
							SecurityContext: buildDefaultContainerSecurityContext(),
							VolumeMounts:    mongodVolumeMounts,
							Lifecycle:       mongodLifecycle,
							// Layer 5 modern HA: hang detection. mongosh ping 단순 path
							// (script 의존 0). keiailab cycle 21 stop hook 21차 동기.
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mongosh", "--quiet", "--port", "27018", "--eval", "db.adminCommand('ping')"},
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								FailureThreshold:    6,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mongosh", "--quiet", "--port", "27018", "--eval", "db.adminCommand('ping')"},
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
							},
							Env: buildBootstrapEnv(name, name+"-headless", mdbsh.Namespace, name, mdbsh.Spec.Shards.MembersPerShard, 27018, false),
						},
					},
					Volumes:  volumes,
					Affinity: buildDefaultAffinity(mdbsh.Name),
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						StorageClassName: storageClassName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceStorage: storageSize},
						},
					},
				},
			},
			PersistentVolumeClaimRetentionPolicy: mdbsh.Spec.Shards.Storage.PersistentVolumeClaimRetentionPolicy,
		},
	}

	// cycle 16: sharded Shard 에도 oplog tailer / audit forwarder sidecar.
	if IsOplogTailerEnabled(mdbsh.Spec.Backup) {
		hasAdmin := mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name != ""
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers,
			BuildOplogTailerSidecar(mdbsh.Spec.Version, 27018, hasAdmin))
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, BuildOplogStagingVolume())
		sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			sts.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: oplogStagingVolume, MountPath: oplogStagingMount},
		)
	}
	if mdbsh.Spec.AuditLog != nil && mdbsh.Spec.AuditLog.Enabled && mdbsh.Spec.AuditLog.CentralForwarder != nil {
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers,
			buildAuditForwarderSidecar(mdbsh.Spec.AuditLog.CentralForwarder))
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "audit-log", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			sts.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "audit-log", MountPath: "/var/log/mongodb-audit"},
		)
	}

	// F-IMP-04 (cycle 0): DiagnosticMode 를 sharded Shard 까지 확장.
	// 각 shard 의 mongod 컨테이너 (shard 별 STS 인스턴스) 가 진단 모드 진입.
	if mdbsh.Spec.Shards.Pod != nil && mdbsh.Spec.Shards.Pod.DiagnosticMode != nil && mdbsh.Spec.Shards.Pod.DiagnosticMode.Enabled {
		applyDiagnosticMode(&sts.Spec.Template.Spec.Containers[0])
	}
	// cycle 14: Shard PodSpec extension + preset + pod-level append.
	if mdbsh.Spec.Shards.Pod != nil {
		applyPodSpecExtensions(&sts.Spec.Template.Spec.Containers[0], mdbsh.Spec.Shards.Pod)
	}
	if mdbsh.Spec.Shards.Pod != nil && mdbsh.Spec.Shards.Pod.ResourcesPreset != "" && isResourcesEmpty(mdbsh.Spec.Shards.Resources) {
		sts.Spec.Template.Spec.Containers[0].Resources = ResourcePreset(mdbsh.Spec.Shards.Pod.ResourcesPreset)
	}
	sts.Spec.Template.Spec.Containers, sts.Spec.Template.Spec.Volumes =
		appendPodSpecPodLevel(sts.Spec.Template.Spec.Containers, sts.Spec.Template.Spec.Volumes, mdbsh.Spec.Shards.Pod)
	if mdbsh.Spec.Shards.Pod != nil && mdbsh.Spec.Shards.Pod.VolumePermissions != nil && mdbsh.Spec.Shards.Pod.VolumePermissions.Enabled {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, buildVolumePermissionsInit(mdbsh.Spec.Shards.Pod.VolumePermissions))
	}
	if mdbsh.Spec.Shards.Pod != nil && len(mdbsh.Spec.Shards.Pod.InitContainers) > 0 {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, mdbsh.Spec.Shards.Pod.InitContainers...)
	}
	return sts
}

// BuildMongosConfigMap creates a ConfigMap for Mongos configuration.
//
// 포함:
//   - configdb: mongos --configdb 인자로 사용되는 connection string 캐시 (read-only mount).
//   - bootstrap-admin.sh: mongos pod 자체가 자기 mongos에 localhost 연결로 첫
//     admin user를 만든다. operator는 pods/exec을 호출하지 않는다. ReplicaSet의
//     동일 패턴(BuildMongoDBConfigMap의 bootstrap-admin.sh)과 동일.
//
// buildMongosConfigDB 는 mongos --configdb 인자로 쓰이는 config server
// connection string 을 만든다. 결함 #5: BuildMongosConfigMap 과
// BuildMongosDeployment 양쪽에 동일 로직이 인라인 중복이던 것을 단일 진실원으로
// 추출. External 지정 시 외부 RS, 미지정 시 in-cluster cfg STS host 목록.
func buildMongosConfigDB(mdbsh *mongodbv1alpha1.MongoDBSharded) string {
	if mdbsh.Spec.ConfigServer.External != nil {
		return fmt.Sprintf("%s/%s",
			mdbsh.Spec.ConfigServer.External.ReplicaSetName,
			strings.Join(mdbsh.Spec.ConfigServer.External.Hosts, ","))
	}
	var configHosts string
	for i := int32(0); i < mdbsh.Spec.ConfigServer.Members; i++ {
		if i > 0 {
			configHosts += ","
		}
		configHosts += fmt.Sprintf("%s-cfg-%d.%s-cfg-headless.%s.svc.cluster.local:27019",
			mdbsh.Name, i, mdbsh.Name, mdbsh.Namespace)
	}
	return fmt.Sprintf("%s-cfg/%s", mdbsh.Name, configHosts)
}

func BuildMongosConfigMap(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.ConfigMap {
	configdbValue := buildMongosConfigDB(mdbsh)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-mongos-config",
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, "mongos"),
		},
		Data: map[string]string{
			"configdb":           configdbValue,
			"bootstrap-admin.sh": buildAdminBootstrapScript(mongoDBPort),
		},
	}
}

// BuildMongosService creates a service for Mongos
func BuildMongosService(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.Service {
	labels := buildLabels(mdbsh.Name, "mongos")

	svcType := corev1.ServiceTypeClusterIP
	if mdbsh.Spec.Mongos.Service != nil && mdbsh.Spec.Mongos.Service.Type != "" {
		svcType = corev1.ServiceType(mdbsh.Spec.Mongos.Service.Type)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-mongos",
			Namespace: mdbsh.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
				{Name: "metrics", Port: metricsPort, TargetPort: intstr.FromInt(metricsPort)},
			},
		},
	}

	if mdbsh.Spec.Mongos.Service != nil {
		if mdbsh.Spec.Mongos.Service.Annotations != nil {
			svc.Annotations = mdbsh.Spec.Mongos.Service.Annotations
		}
		if mdbsh.Spec.Mongos.Service.LoadBalancerIP != "" {
			svc.Spec.LoadBalancerIP = mdbsh.Spec.Mongos.Service.LoadBalancerIP
		}
	}

	return svc
}

// BuildMongosPerReplicaServices creates one Service per mongos pod for direct external routing.
func BuildMongosPerReplicaServices(mdbsh *mongodbv1alpha1.MongoDBSharded) []*corev1.Service {
	if mdbsh.Spec.Mongos.Service == nil || mdbsh.Spec.Mongos.Service.ServicePerReplica == nil ||
		!mdbsh.Spec.Mongos.Service.ServicePerReplica.Enabled {
		return nil
	}

	replicas := int32(2)
	if mdbsh.Spec.Mongos.Replicas > 0 {
		replicas = mdbsh.Spec.Mongos.Replicas
	}

	svcType := corev1.ServiceTypeClusterIP
	if mdbsh.Spec.Mongos.Service.ServicePerReplica.Type != "" {
		svcType = corev1.ServiceType(mdbsh.Spec.Mongos.Service.ServicePerReplica.Type)
	}

	var services []*corev1.Service
	for i := int32(0); i < replicas; i++ {
		podName := fmt.Sprintf("%s-mongos-%d", mdbsh.Name, i)
		labels := buildLabels(mdbsh.Name, "mongos")

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: mdbsh.Namespace,
				Labels:    labels,
			},
			Spec: corev1.ServiceSpec{
				Type: svcType,
				Selector: map[string]string{
					"statefulset.kubernetes.io/pod-name": podName,
				},
				Ports: []corev1.ServicePort{
					{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
				},
			},
		}

		if mdbsh.Spec.Mongos.Service.ServicePerReplica.Annotations != nil {
			svc.Annotations = mdbsh.Spec.Mongos.Service.ServicePerReplica.Annotations
		}

		services = append(services, svc)
	}
	return services
}

// BuildMongosDeployment creates a Deployment for Mongos
// BuildMongosStatefulSet creates a StatefulSet for Mongos when
// Mongos.UseStatefulSet=true (cycle 18, G-12 upstream chart parity).
// mongos 가 stable network identity 필요한 시나리오 (외부 client 직접 routing).
func BuildMongosStatefulSet(mdbsh *mongodbv1alpha1.MongoDBSharded) *appsv1.StatefulSet {
	dep := BuildMongosDeployment(mdbsh)
	// Convert Deployment spec → StatefulSet spec (선택 fields 만 copy).
	return &appsv1.StatefulSet{
		ObjectMeta: dep.ObjectMeta,
		Spec: appsv1.StatefulSetSpec{
			ServiceName: mdbsh.Name + "-mongos-headless",
			Replicas:    dep.Spec.Replicas,
			Selector:    dep.Spec.Selector,
			Template:    dep.Spec.Template,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
			PodManagementPolicy: appsv1.ParallelPodManagement,
		},
	}
}

func BuildMongosDeployment(mdbsh *mongodbv1alpha1.MongoDBSharded) *appsv1.Deployment {
	labels := buildLabels(mdbsh.Name, "mongos")

	// Build config server connection string (결함 #5: 공통 헬퍼 위임).
	configdbArg := buildMongosConfigDB(mdbsh)

	args := []string{
		"--configdb", configdbArg,
		"--bind_ip_all",
		"--keyFile", "/etc/mongodb-keyfile/keyfile",
	}
	// Pillar P7 Phase 3b — TLS args (mongos, preferTLS plaintext fallback).
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		args = append(args, tlsArgs(mdbsh.Spec.TLS)...)
	}

	// cycle 14 mongos integration: auth + audit args. mongos 는 데이터 저장 안 하므로
	// encryption-at-rest 불필요. LDAP/OIDC bind 는 mongos 에도 의미 있음.
	if mdbsh.Spec.Auth.LDAP != nil {
		args = append(args, authpkg.LDAPMongodArgs(mdbsh.Spec.Auth.LDAP)...)
	}
	if mdbsh.Spec.Auth.OIDC != nil {
		if oidcParam, err := authpkg.OIDCMongodSetParameter(mdbsh.Spec.Auth.OIDC); err == nil && oidcParam != "" {
			args = append(args, "--setParameter", "oidcIdentityProviders="+oidcParam)
		}
	}
	if mdbsh.Spec.AuditLog != nil {
		args = append(args, auditpkg.MongodArgs(mdbsh.Spec.AuditLog)...)
	}

	// mongos container의 volume mounts. admin-credentials와 scripts는
	// AdminCredentialsSecretRef가 설정된 경우에만 추가.
	mongosVolumeMounts := []corev1.VolumeMount{
		{Name: "keyfile", MountPath: "/etc/mongodb-keyfile", ReadOnly: true},
	}
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		mongosVolumeMounts = append(mongosVolumeMounts,
			buildTLSServerMount(),
			buildTLSPEMMount(),
		)
	}
	var mongosLifecycle *corev1.Lifecycle
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name != "" {
		mongosVolumeMounts = append(mongosVolumeMounts,
			corev1.VolumeMount{Name: "scripts", MountPath: "/scripts", ReadOnly: true},
			corev1.VolumeMount{Name: "admin-credentials", MountPath: "/etc/mongodb-admin", ReadOnly: true},
		)
		// mongos pod가 자기 mongos에 localhost 연결로 첫 admin user를 만든다.
		// operator는 pods/exec을 호출하지 않는다.
		mongosLifecycle = &corev1.Lifecycle{
			PostStart: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/scripts/bootstrap-admin.sh"},
				},
			},
		}
	}

	containers := []corev1.Container{
		{
			Name:    "mongos",
			Image:   getMongoDBImage(mdbsh.Spec.Version),
			Command: []string{"mongos"},
			Args:    args,
			Ports: []corev1.ContainerPort{
				{Name: "mongodb", ContainerPort: mongoDBPort},
			},
			Resources:       buildResourceRequirements(mdbsh.Spec.Mongos.Resources),
			SecurityContext: buildDefaultContainerSecurityContext(),
			VolumeMounts:    mongosVolumeMounts,
			Lifecycle:       mongosLifecycle,
			// StartupProbe (v1.4.17 fix, cycle 19 last): mongos 가 cfg replica set 에
			// outbound connect + sharding pool init 시간 (TLS handshake 포함, ~10-60s)
			// 이 default liveness initialDelay 30s 보다 길어 race condition. Startup
			// probe 가 success 까지 liveness/readiness 차단 → kubelet kill 회피.
			// failureThreshold=30 × periodSeconds=10 = 최대 5분 startup window.
			StartupProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{
						Port: intstr.FromInt(mongoDBPort),
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
				FailureThreshold:    60,
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{
						Port: intstr.FromInt(mongoDBPort),
					},
				},
				InitialDelaySeconds: 30,
				PeriodSeconds:       10,
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{
						Command: []string{"mongosh", "--norc", "--quiet", "--eval", "db.adminCommand('ping')"},
					},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       10,
				TimeoutSeconds:      10,
				FailureThreshold:    6,
			},
		},
	}

	// Add exporter sidecar if monitoring enabled
	if mdbsh.Spec.Monitoring != nil && mdbsh.Spec.Monitoring.Enabled {
		containers = append(containers, corev1.Container{
			Name:  "exporter",
			Image: exporterImage,
			Ports: []corev1.ContainerPort{
				{Name: "metrics", ContainerPort: metricsPort},
			},
			Args: []string{"--collect-all", "--compatible-mode"},
			Env: []corev1.EnvVar{
				{
					Name: "MONGODB_URI",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: mdbsh.Name + "-exporter-uri",
							},
							Key: "uri",
						},
					},
				},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
			SecurityContext: buildDefaultContainerSecurityContext(),
		})
	}

	// F-IMP-04 (cycle 0): DiagnosticMode 를 sharded Mongos 까지 확장.
	// mongos 컨테이너 (containers[0]) 만 적용 — exporter sidecar 는 그대로.
	if mdbsh.Spec.Mongos.Pod != nil && mdbsh.Spec.Mongos.Pod.DiagnosticMode != nil && mdbsh.Spec.Mongos.Pod.DiagnosticMode.Enabled {
		applyDiagnosticMode(&containers[0])
	}
	// cycle 14: Mongos PodSpec extension + preset + sidecars/extraVolumes.
	if mdbsh.Spec.Mongos.Pod != nil {
		applyPodSpecExtensions(&containers[0], mdbsh.Spec.Mongos.Pod)
	}
	if mdbsh.Spec.Mongos.Pod != nil && mdbsh.Spec.Mongos.Pod.ResourcesPreset != "" && isResourcesEmpty(mdbsh.Spec.Mongos.Resources) {
		containers[0].Resources = ResourcePreset(mdbsh.Spec.Mongos.Pod.ResourcesPreset)
	}
	mongosVolumes := buildMongosVolumes(mdbsh)
	containers, mongosVolumes = appendPodSpecPodLevel(containers, mongosVolumes, mdbsh.Spec.Mongos.Pod)
	// cycle 16: audit forwarder sidecar (mongos audit log forward).
	if mdbsh.Spec.AuditLog != nil && mdbsh.Spec.AuditLog.Enabled && mdbsh.Spec.AuditLog.CentralForwarder != nil {
		containers = append(containers, buildAuditForwarderSidecar(mdbsh.Spec.AuditLog.CentralForwarder))
		mongosVolumes = append(mongosVolumes, corev1.Volume{
			Name: "audit-log", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		containers[0].VolumeMounts = append(containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "audit-log", MountPath: "/var/log/mongodb-audit"})
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-mongos",
			Namespace: mdbsh.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &mdbsh.Spec.Mongos.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
					MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					SecurityContext: buildDefaultSecurityContext(),
					InitContainers: []corev1.Container{
						{
							Name:  "copy-keyfile",
							Image: keyfileInitImage,
							Command: []string{
								"sh", "-c",
								"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
								{Name: "keyfile", MountPath: "/keyfile"},
							},
							SecurityContext: buildKeyfileInitContainerSecurityContext(),
						},
					},
					Containers: containers,
					Volumes:    mongosVolumes,
					Affinity:   buildDefaultAffinity(mdbsh.Name),
				},
			},
		},
	}
}

// buildMongosVolumes는 mongos pod의 volumes를 만든다. AdminCredentialsSecretRef가
// 설정되면 admin password와 mongos-config(bootstrap-admin.sh 포함)를 추가 마운트한다.
func buildMongosVolumes(mdbsh *mongodbv1alpha1.MongoDBSharded) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: "keyfile-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  mdbsh.Name + "-keyfile",
					DefaultMode: ptr.To[int32](0400),
				},
			},
		},
		{
			Name: "keyfile",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name != "" {
		volumes = append(volumes,
			corev1.Volume{
				Name: "scripts",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: mdbsh.Name + "-mongos-config",
						},
						DefaultMode: ptr.To[int32](0755),
					},
				},
			},
			corev1.Volume{
				Name: "admin-credentials",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name,
						DefaultMode: ptr.To[int32](0400),
					},
				},
			},
		)
	}

	// Pillar P7 Phase 3a+3b — TLS volume + PEM emptyDir (mongos).
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		volumes = append(volumes, buildTLSServerVolume(mdbsh.Name+"-tls"), buildTLSPEMVolume())
	}

	return volumes
}

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

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backup.Name,
			Namespace: backup.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
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
				},
			},
		},
	}
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
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backup.Name + "-restore",
			Namespace: backup.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
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
					SecurityContext: buildDefaultSecurityContext(),
				},
			},
		},
	}, nil
}

// BuildMongoDBPDB는 MongoDB ReplicaSet workload를 위한 PodDisruptionBudget을 생성한다.
// spec.podDisruptionBudget가 nil이거나 enabled=false면 nil을 반환해 controller가
// 생성/업데이트를 skip하게 한다.
//
// 기본값: minAvailable = max(replicas-1, 0). 3 멤버 RS면 minAvailable=2가
// 적용되어 한 번에 한 멤버만 maintenance 가능하도록 보장.
func BuildMongoDBPDB(mdb *mongodbv1alpha1.MongoDB) *policyv1.PodDisruptionBudget {
	pdbSpec := mdb.Spec.PodDisruptionBudget
	if pdbSpec == nil || !pdbSpec.Enabled {
		return nil
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdb.Name + "-pdb",
			Namespace: mdb.Namespace,
			Labels:    buildLabels(mdb.Name, "replicaset"),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: buildLabels(mdb.Name, "replicaset"),
			},
		},
	}
	switch {
	case pdbSpec.MinAvailable != nil:
		pdb.Spec.MinAvailable = pdbSpec.MinAvailable
	case pdbSpec.MaxUnavailable != nil:
		pdb.Spec.MaxUnavailable = pdbSpec.MaxUnavailable
	default:
		minAvail := mdb.Spec.Members - 1
		if minAvail < 0 {
			minAvail = 0
		}
		v := intstr.FromInt(int(minAvail))
		pdb.Spec.MinAvailable = &v
	}
	return pdb
}

// BuildMongoDBNetworkPolicy는 MongoDB ReplicaSet pods에 대한 deny-by-default
// NetworkPolicy를 생성한다. spec.networkPolicy.enabled=false면 nil 반환.
//
// 기본 정책: 같은 RS의 pods간 27017 ingress만 허용. AdditionalIngressFrom으로
// 운영 namespace, 모니터링 stack(exporter scrape) 등을 추가 ingress로 명시 가능.
//
// iteration 26 (2026-05-07): keiailab-commons/pkg/networkpolicy v0.3.0 위임.
// 인라인 builder → commons.New + WithSelfIngress + WithIngressFromPeers.
// valkey iteration 25 (97162b5) 패턴 차용. semantic equivalence 보존.
func BuildMongoDBNetworkPolicy(mdb *mongodbv1alpha1.MongoDB) *networkingv1.NetworkPolicy {
	npSpec := mdb.Spec.NetworkPolicy
	if npSpec == nil || !npSpec.Enabled {
		return nil
	}
	selector := buildLabels(mdb.Name, "replicaset")

	opts := []commonsnp.Option{
		commonsnp.WithLabels(selector),
		commonsnp.WithSelfIngress([]int32{int32(mongoDBPort)}),
	}
	if extra := convertAdditionalPeers(npSpec.AdditionalIngressFrom); len(extra) > 0 {
		opts = append(opts, commonsnp.WithIngressFromPeers(extra, []int32{int32(mongoDBPort)}))
	}
	return commonsnp.New(mdb.Name+"-netpol", mdb.Namespace, selector, opts...)
}

// convertAdditionalPeers — mongodbv1alpha1.NetworkPolicyPeer → commons Peer.
// PodSelector / NamespaceSelector 둘 다 nil 인 entry 는 skip (기존 동작 보존).
func convertAdditionalPeers(in []mongodbv1alpha1.NetworkPolicyPeer) []commonsnp.Peer {
	out := make([]commonsnp.Peer, 0, len(in))
	for _, peer := range in {
		if peer.PodSelector == nil && peer.NamespaceSelector == nil {
			continue
		}
		p := commonsnp.Peer{}
		if peer.PodSelector != nil {
			p.PodSelector = *peer.PodSelector
		}
		if peer.NamespaceSelector != nil {
			p.NamespaceSelector = *peer.NamespaceSelector
		}
		out = append(out, p)
	}
	return out
}

// pdbBaseSpec은 PodDisruptionBudgetSpec를 받아 기본 minAvailable=replicas-1을
// 적용한 K8s PDB spec을 만든다(MinAvailable/MaxUnavailable 미지정 시).
func pdbBaseSpec(spec *mongodbv1alpha1.PodDisruptionBudgetSpec, replicas int32, selector map[string]string) policyv1.PodDisruptionBudgetSpec {
	out := policyv1.PodDisruptionBudgetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: selector},
	}
	switch {
	case spec.MinAvailable != nil:
		out.MinAvailable = spec.MinAvailable
	case spec.MaxUnavailable != nil:
		out.MaxUnavailable = spec.MaxUnavailable
	default:
		minAvail := replicas - 1
		if minAvail < 0 {
			minAvail = 0
		}
		v := intstr.FromInt(int(minAvail))
		out.MinAvailable = &v
	}
	return out
}

// BuildShardedPDBs는 sharded cluster의 cfg/shards/mongos 각 컴포넌트에 대한
// PodDisruptionBudget 슬라이스를 반환한다. Spec.PodDisruptionBudget이 nil이거나
// disabled면 nil 슬라이스 반환.
func BuildShardedPDBs(mdbsh *mongodbv1alpha1.MongoDBSharded) []*policyv1.PodDisruptionBudget {
	pdbSpec := mdbsh.Spec.PodDisruptionBudget
	if pdbSpec == nil || !pdbSpec.Enabled {
		return nil
	}
	var out []*policyv1.PodDisruptionBudget

	// Config server
	cfgSelector := buildLabels(mdbsh.Name, "configsvr")
	out = append(out, &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-cfg-pdb",
			Namespace: mdbsh.Namespace,
			Labels:    cfgSelector,
		},
		Spec: pdbBaseSpec(pdbSpec, mdbsh.Spec.ConfigServer.Members, cfgSelector),
	})

	// Shards (각 shard별)
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shSelector := buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", i))
		out = append(out, &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-shard-%d-pdb", mdbsh.Name, i),
				Namespace: mdbsh.Namespace,
				Labels:    shSelector,
			},
			Spec: pdbBaseSpec(pdbSpec, mdbsh.Spec.Shards.MembersPerShard, shSelector),
		})
	}

	// Mongos
	mongosSelector := buildLabels(mdbsh.Name, "mongos")
	out = append(out, &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-mongos-pdb",
			Namespace: mdbsh.Namespace,
			Labels:    mongosSelector,
		},
		Spec: pdbBaseSpec(pdbSpec, mdbsh.Spec.Mongos.Replicas, mongosSelector),
	})

	return out
}

// buildShardedComponentNetworkPolicy은 cfg/shard/mongos 컴포넌트별 NetworkPolicy
// 빌더의 공용 helper. selector(컴포넌트 라벨)와 port(cfg=27019/shard=27018/
// mongos=27017)를 매개변수화한다.
//
// iteration 26: commons.New 위임. self-peer 가 *cluster-wide* (instance + managed-by
// 만 매칭, component 무관) — cfg ↔ shard ↔ mongos 통신 허용 패턴 — WithIngressFromPeers
// 의 cluster-wide Peer 로 표현.
func buildShardedComponentNetworkPolicy(mdbsh *mongodbv1alpha1.MongoDBSharded, name string, port int, selector map[string]string) *networkingv1.NetworkPolicy {
	npSpec := mdbsh.Spec.NetworkPolicy
	if npSpec == nil || !npSpec.Enabled {
		return nil
	}
	ports := []int32{int32(port)}
	clusterPeer := commonsnp.Peer{
		PodSelector: map[string]string{
			"app.kubernetes.io/instance":   mdbsh.Name,
			"app.kubernetes.io/managed-by": "mongodb-operator",
		},
	}
	opts := []commonsnp.Option{
		commonsnp.WithLabels(selector),
		commonsnp.WithIngressFromPeers([]commonsnp.Peer{clusterPeer}, ports),
	}
	if extra := convertAdditionalPeers(npSpec.AdditionalIngressFrom); len(extra) > 0 {
		opts = append(opts, commonsnp.WithIngressFromPeers(extra, ports))
	}
	return commonsnp.New(name, mdbsh.Namespace, selector, opts...)
}

// BuildShardedNetworkPolicies는 cfg/shards/mongos 각 컴포넌트에 대한 NetworkPolicy
// 슬라이스를 반환한다. spec.networkPolicy.enabled=false면 nil 반환.
func BuildShardedNetworkPolicies(mdbsh *mongodbv1alpha1.MongoDBSharded) []*networkingv1.NetworkPolicy {
	npSpec := mdbsh.Spec.NetworkPolicy
	if npSpec == nil || !npSpec.Enabled {
		return nil
	}
	var out []*networkingv1.NetworkPolicy
	out = append(out, buildShardedComponentNetworkPolicy(mdbsh, mdbsh.Name+"-cfg-netpol", 27019,
		buildLabels(mdbsh.Name, "configsvr")))
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		out = append(out, buildShardedComponentNetworkPolicy(mdbsh,
			fmt.Sprintf("%s-shard-%d-netpol", mdbsh.Name, i), 27018,
			buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", i))))
	}
	out = append(out, buildShardedComponentNetworkPolicy(mdbsh, mdbsh.Name+"-mongos-netpol", 27017,
		buildLabels(mdbsh.Name, "mongos")))
	return out
}

// BuildMongosHPA는 mongos Deployment에 대한 HorizontalPodAutoscaler를 만든다.
// `Spec.Mongos.AutoScaling.Enabled=false`이면 nil(호출자가 기존 HPA 삭제 처리).
// mongos는 stateless router라 deliberate 가드가 불필요(ADR-0007).
func BuildMongosHPA(mdbsh *mongodbv1alpha1.MongoDBSharded) *autoscalingv2.HorizontalPodAutoscaler {
	if !IsMongosHPAActive(mdbsh) {
		return nil
	}
	return buildHPAForTarget(
		mdbsh.Name+"-mongos-hpa", mdbsh.Namespace, buildLabels(mdbsh.Name, "mongos"),
		"Deployment", mdbsh.Name+"-mongos",
		mdbsh.Spec.Mongos.AutoScaling,
	)
}

// BuildReplicaSetHPA는 ReplicaSet StatefulSet에 대한 HPA를 만든다.
// `Spec.AutoScaling.Enabled=true` + `Spec.ScalePolicy.Deliberate=true` *둘 다*
// 일 때만 nil 아닌 HPA 반환(이중 가드, ADR-0008).
//
// 이중 가드 근거 — RS 멤버 수 변경은 RS reconfig + initial sync 부작용 동반.
// HPA controller가 metric 변동에 따라 Replicas를 자동 patch하면 운영자 모르는
// 사이 RS reconfig가 발동될 수 있어, "운영자 의도된 자동화"임을 명시(deliberate)
// 해야만 활성화한다.
func BuildReplicaSetHPA(mdb *mongodbv1alpha1.MongoDB) *autoscalingv2.HorizontalPodAutoscaler {
	if !IsRSHPAActive(mdb) {
		return nil
	}
	return buildHPAForTarget(
		mdb.Name+"-hpa", mdb.Namespace, buildLabels(mdb.Name, "replicaset"),
		"StatefulSet", mdb.Name,
		mdb.Spec.AutoScaling,
	)
}

// BuildConfigServerHPA는 cfg StatefulSet에 대한 HPA를 만든다. cfg는 보통 작은
// 멤버 수(3-7)이고 변동이 거의 없어 *deliberate 이중 가드*를 동일하게 적용한다
// (ADR-0009).
func BuildConfigServerHPA(mdbsh *mongodbv1alpha1.MongoDBSharded) *autoscalingv2.HorizontalPodAutoscaler {
	if !IsConfigServerHPAActive(mdbsh) {
		return nil
	}
	return buildHPAForTarget(
		mdbsh.Name+"-cfg-hpa", mdbsh.Namespace, buildLabels(mdbsh.Name, "configsvr"),
		"StatefulSet", mdbsh.Name+"-cfg",
		mdbsh.Spec.ConfigServer.AutoScaling,
	)
}

// buildHPAForTarget는 HPA 객체 생성 공통 로직(가드 검사는 호출자 책임).
func buildHPAForTarget(name, namespace string, labels map[string]string, kind, refName string,
	as *mongodbv1alpha1.AutoScalingSpec,
) *autoscalingv2.HorizontalPodAutoscaler {
	min := as.MinReplicas
	if min < 1 {
		min = 1
	}
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       kind,
				Name:       refName,
			},
			MinReplicas: &min,
			MaxReplicas: as.MaxReplicas,
			Metrics:     buildHPAMetrics(as.Metrics),
		},
	}
}

// IsRSHPAActive는 RS HPA가 reconcile에서 활성 상태인지 검사한다(이중 가드).
// applyStatefulSet의 HPA-aware preserve 분기에서 사용.
func IsRSHPAActive(mdb *mongodbv1alpha1.MongoDB) bool {
	return mdb.Spec.AutoScaling != nil && mdb.Spec.AutoScaling.Enabled &&
		mdb.Spec.ScalePolicy != nil && mdb.Spec.ScalePolicy.Deliberate
}

// IsConfigServerHPAActive는 cfg HPA의 이중 가드 통과 여부를 검사한다.
func IsConfigServerHPAActive(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.ConfigServer.AutoScaling != nil && mdbsh.Spec.ConfigServer.AutoScaling.Enabled &&
		mdbsh.Spec.ConfigServer.ScalePolicy != nil && mdbsh.Spec.ConfigServer.ScalePolicy.Deliberate
}

// IsMongosHPAActive는 mongos HPA의 활성 상태(단일 가드: enabled).
func IsMongosHPAActive(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.Mongos.AutoScaling != nil && mdbsh.Spec.Mongos.AutoScaling.Enabled
}

// IsRSScaleDeliberate는 ReplicaSet 멤버 수 변경이 의도된 자동화임을 검사한다.
// Spec.ScalePolicy=nil이면 default false(즉시 변경 금지). true이면 spec.Members
// 변경이 즉시 STS replicas로 반영된다(ADR-0008).
func IsRSScaleDeliberate(mdb *mongodbv1alpha1.MongoDB) bool {
	return mdb.Spec.ScalePolicy != nil && mdb.Spec.ScalePolicy.Deliberate
}

// IsConfigServerScaleDeliberate는 cfg 멤버 수 변경 가드(ADR-0008/0009).
func IsConfigServerScaleDeliberate(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.ConfigServer.ScalePolicy != nil && mdbsh.Spec.ConfigServer.ScalePolicy.Deliberate
}

// IsShardScaleDeliberate는 shard MembersPerShard 변경 가드(ADR-0008/0009).
func IsShardScaleDeliberate(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.Shards.ScalePolicy != nil && mdbsh.Spec.Shards.ScalePolicy.Deliberate
}

// buildHPAMetrics는 spec의 metric 목록을 autoscaling/v2 metric으로 변환한다.
// 알 수 없는 type은 silently skip하지 않고(운영자 진단성을 위해) caller가 spec
// validation에서 차단해야 한다 — 본 함수는 변환 책임만.
func buildHPAMetrics(metrics []mongodbv1alpha1.AutoScalingMetric) []autoscalingv2.MetricSpec {
	if len(metrics) == 0 {
		// 아무 metric도 없으면 cpu 80% 기본값 — upstream chart 등 표준 기본.
		v := int32(80)
		return []autoscalingv2.MetricSpec{{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &v,
				},
			},
		}}
	}
	out := make([]autoscalingv2.MetricSpec, 0, len(metrics))
	for _, m := range metrics {
		target := m.Target
		switch m.Type {
		case "cpu":
			out = append(out, autoscalingv2.MetricSpec{
				Type: autoscalingv2.ResourceMetricSourceType,
				Resource: &autoscalingv2.ResourceMetricSource{
					Name: corev1.ResourceCPU,
					Target: autoscalingv2.MetricTarget{
						Type:               autoscalingv2.UtilizationMetricType,
						AverageUtilization: &target,
					},
				},
			})
		case "memory":
			out = append(out, autoscalingv2.MetricSpec{
				Type: autoscalingv2.ResourceMetricSourceType,
				Resource: &autoscalingv2.ResourceMetricSource{
					Name: corev1.ResourceMemory,
					Target: autoscalingv2.MetricTarget{
						Type:               autoscalingv2.UtilizationMetricType,
						AverageUtilization: &target,
					},
				},
			})
		case "custom":
			if m.CustomMetric == nil || m.CustomMetric.Name == "" {
				continue
			}
			q := resource.NewQuantity(int64(target), resource.DecimalSI)
			out = append(out, autoscalingv2.MetricSpec{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					Metric: autoscalingv2.MetricIdentifier{Name: m.CustomMetric.Name},
					Target: autoscalingv2.MetricTarget{
						Type:         autoscalingv2.AverageValueMetricType,
						AverageValue: q,
					},
				},
			})
		}
	}
	return out
}

// BuildQueryableStatefulSet creates a single-member read-only StatefulSet
// for queryable backup access.
func BuildQueryableStatefulSet(backup *mongodbv1alpha1.MongoDBBackup, spec *mongodbv1alpha1.QueryableBackupSpec) *appsv1.StatefulSet {
	if spec == nil || !spec.Enabled {
		return nil
	}
	name := backup.Name + "-queryable"
	labels := buildLabels(name, "queryable-backup")
	replicas := int32(1)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: backup.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "mongod",
						// 이미지 SSOT: 하드코딩 "mongo:8.0" → defaultImage 일원화.
						Image:        defaultImage,
						Args:         []string{"--bind_ip_all", "--noauth", "--dbpath", "/data/db"},
						Ports:        []corev1.ContainerPort{{Name: "mongodb", ContainerPort: 27017}},
						VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data/db"}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("250m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
						// 결함 #4: SecurityContext 완전 누락이던 read-only mongod 에
						// PSA restricted 충족 컨텍스트 추가. --noauth read-only 라도
						// 비특권 999 로 기동 가능 (다른 STS 빌더와 동일).
						SecurityContext: buildDefaultContainerSecurityContext(),
					}},
					SecurityContext: buildDefaultSecurityContext(),
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "data"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
					},
				},
			}},
		},
	}
}
