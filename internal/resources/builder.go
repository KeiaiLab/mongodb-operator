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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	commonslabels "github.com/keiailab/keiailab-commons/pkg/labels"
	"github.com/keiailab/keiailab-commons/pkg/probes"
	commonsservice "github.com/keiailab/keiailab-commons/pkg/service"
	commonstopology "github.com/keiailab/keiailab-commons/pkg/topology"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/assets"
	auditpkg "github.com/keiailab/mongodb-operator/internal/controller/audit"
	authpkg "github.com/keiailab/mongodb-operator/internal/controller/auth"
	encryptionpkg "github.com/keiailab/mongodb-operator/internal/controller/encryption"
	secv2 "github.com/keiailab/mongodb-operator/internal/security"
)

const (
	mongoDBPort = 27017
	metricsPort = 9216
	// metricsPortName — Service/Container 의 metrics 포트 이름 (goconst SSOT).
	metricsPortName = "metrics"
	defaultImage    = "mongo:8.3.1"
	// exporterImage — chart values / examples (0.51.0) 와 코드 const (0.40) 의
	// 3-way drift 를 최신 쪽 0.51.0 단일 진실원으로 통일 (kubebuilder default 동기).
	exporterImage = "percona/mongodb_exporter:0.51.0"
	// keyfileInitImage 는 copy-keyfile init container (4곳: replicaset / cfg / shard / mongos)
	// 의 단일 진실원. busybox 만 사용 (chmod + cp), CVE 패치 시 본 const 만 갱신.
	keyfileInitImage = "busybox:1.37"

	// scripts ConfigMap key 이름 (RS/cfg/shard 3 ConfigMap 공용 — goconst).
	scriptReadiness = "readiness-probe.sh"
	scriptBootstrap = "bootstrap-admin.sh"
	scriptStepDown  = "prestop-stepdown.sh"

	// envMongoDBURI — backup/restore Job 의 MongoDB 연결 URI env 이름 (RS/sharded
	// StatefulSet + backup/restore Job 공용 — goconst SSOT).
	envMongoDBURI = "MONGODB_URI"
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

// effectiveMongoDBVersion은 STS가 실제 배포할 버전을 결정한다(무중단 업그레이드/롤백 SSOT).
// status.EffectiveVersion이 비어있지 않으면 그것을 우선(롤백 중 PreviousVersion), 비어있으면
// spec.Version으로 fallback(기존 클러스터 무롤링 호환 — byte-identical). spec.Version.Image
// override는 보존한다(version string만 effective로 치환).
func effectiveMongoDBVersion(spec mongodbv1alpha1.MongoDBVersion, statusEffective string) mongodbv1alpha1.MongoDBVersion {
	if statusEffective == "" || spec.Image != "" {
		return spec
	}
	return mongodbv1alpha1.MongoDBVersion{Version: statusEffective}
}

// MongoTLSMountPath 는 cert-manager 발급 Secret 의 raw mount 경로.
const MongoTLSMountPath = "/etc/ssl/mongo"

// MongoTLSPEMPath 는 init container 가 만든 PEM merge file 의 경로.
// mongod --tlsCertificateKeyFile 가 단일 PEM (cert + key) 를 요구.
const MongoTLSPEMPath = "/etc/ssl/mongo-pem"

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

// buildStepDownScript은 preStop에서 PRIMARY 이양(rs.stepDown)을 수행하는 스크립트를
// 반환한다(무중단 업그레이드). assets/scripts/prestop-stepdown.sh.tpl로 외부화.
func buildStepDownScript(port int) string {
	out, err := assets.RenderStepDown(port)
	if err != nil {
		panic(fmt.Sprintf("render stepdown script: %v", err))
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

// buildAdminBootstrapLifecycle은 /scripts/bootstrap-admin.sh를 postStart로 실행하고
// /scripts/prestop-stepdown.sh를 preStop으로 실행하는 Lifecycle hook을 반환한다.
// ReplicaSet/ConfigServer/Shard StatefulSet 공용.
//
// preStop stepDown(무중단 업그레이드): pod 종료(롤링 업데이트 등) 직전 자기 mongod가
// primary면 rs.stepDown()으로 secondary에 primary 이양 → election 끊김(~10s) 회피.
// primary가 아니면 no-op(스크립트가 에러 무시). RollingUpdate 가 primary pod 를
// 재시작할 때 쓰기 단절을 최소화한다.
func buildAdminBootstrapLifecycle() *corev1.Lifecycle {
	return &corev1.Lifecycle{
		PostStart: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"/scripts/bootstrap-admin.sh"},
			},
		},
		PreStop: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"/scripts/prestop-stepdown.sh"},
			},
		},
	}
}

// BuildHeadlessService creates a headless service for StatefulSet.
// 조립은 keiailab-commons/pkg/service.Build 에 위임 (Headless → ClusterIP None +
// PublishNotReadyAddresses). 포트/라벨은 mongo 도메인 잔류.
func BuildHeadlessService(mdb *mongodbv1alpha1.MongoDB) *corev1.Service {
	return commonsservice.Build(commonsservice.Params{
		Name:      mdb.Name + "-headless",
		Namespace: mdb.Namespace,
		Labels:    buildLabels(mdb.Name, "headless"),
		Selector:  buildLabels(mdb.Name, "replicaset"),
		Ports: []corev1.ServicePort{
			{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
		},
		Headless: true,
	})
}

// BuildClientService creates a client service for MongoDB access.
func BuildClientService(mdb *mongodbv1alpha1.MongoDB) *corev1.Service {
	return commonsservice.Build(commonsservice.Params{
		Name:      mdb.Name,
		Namespace: mdb.Namespace,
		Labels:    buildLabels(mdb.Name, "client"),
		Selector:  buildLabels(mdb.Name, "replicaset"),
		Type:      corev1.ServiceTypeClusterIP,
		Ports: []corev1.ServicePort{
			{Name: "mongodb", Port: mongoDBPort, TargetPort: intstr.FromInt(mongoDBPort)},
			{Name: metricsPortName, Port: metricsPort, TargetPort: intstr.FromInt(metricsPort)},
		},
	})
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
			Image: getMongoDBImage(effectiveMongoDBVersion(mdb.Spec.Version, mdb.Status.EffectiveVersion)),
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
				{Name: metricsPortName, ContainerPort: metricsPort, Protocol: corev1.ProtocolTCP},
			},
			Args: []string{
				"--collect-all",
				"--compatible-mode",
			},
			Env: []corev1.EnvVar{
				{
					Name: envMongoDBURI,
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
	//
	// 아키텍처 A(S3 직접 스트리밍) 이후 staging volume 은 tailer 전용 scratch
	// (aws CLI 의 쓰기 가능 HOME) 이므로 mongod 컨테이너에는 마운트하지 않는다 —
	// 구 구현이 EmptyDir 에 쌓던 oplog batch 파일 자체가 더 이상 없다.
	if IsOplogTailerEnabled(mdb.Spec.Backup) {
		hasAdmin := mdb.Spec.Auth.AdminCredentialsSecretRef.Name != ""
		if image, reason := resolveOplogTailerImage(); reason == "" {
			containers = append(containers, BuildOplogTailerSidecar(
				image, mongoDBPort, hasAdmin, mdb.Name, mdb.Spec.Backup))
			volumes = append(volumes, BuildOplogStagingVolume())
		} else {
			// fail-open: OPLOG_TAILER_IMAGE 미설정 시 사이드카를 주입하지 않아
			// mongod pod readiness 를 지키고, reason 은 컨트롤러가 status 로 표면화.
			_ = reason
		}
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
			// RollingUpdate 명시 — 무중단 버전 업그레이드(이미지 변경 시 k8s 자동 순차 롤링).
			// 미지정 시 OnDelete 기본값이라 STS image 변경해도 pod 재생성 안 됨(업그레이드 불가).
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
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
					Containers: []corev1.Container{
						{
							Name:  "mongodb",
							Image: getMongoDBImage(effectiveMongoDBVersion(mdbsh.Spec.Version, mdbsh.Status.EffectiveVersion)),
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

	// cycle 16: sharded ConfigServer 의 audit forwarder sidecar.
	//
	// PITR oplog tailer 는 sharded 에 주입하지 않는다 (RS-only 우선 지원).
	// S3 키 스킴이 `<cluster>/oplog/<startTs>_<endTs>` 라 shard 차원이 없어,
	// config-server 와 각 shard 의 tailer 가 *동일 prefix* 를 공유하게 된다 —
	// 서로 독립적인 oplog 타임라인의 세그먼트가 한 체인에 뒤섞여 restore 가
	// 잘못된 데이터를 replay 한다 (best-effort 가 아니라 손상).
	// sharded PITR 은 shard 별 독립 체인 + 단일 PIT 정합 설계가 선행되어야 한다.
	// MongoDBBackup webhook 이 sharded 대상 복원에 Warning 을 발행한다.
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
			// RollingUpdate 명시 — 무중단 버전 업그레이드(이미지 변경 시 k8s 자동 순차 롤링).
			// 미지정 시 OnDelete 기본값이라 STS image 변경해도 pod 재생성 안 됨(업그레이드 불가).
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
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
					Containers: []corev1.Container{
						{
							Name:  "mongodb",
							Image: getMongoDBImage(effectiveMongoDBVersion(mdbsh.Spec.Version, mdbsh.Status.EffectiveVersion)),
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

	// mongot(MongoDB Search) sidecar — shard 별 주입(annotation 시). 각 shard replicaSet 의 mongot 은
	// 자기 shard 데이터만 인덱싱하며 localhost mongod(27018) 로 topology 연결(별도 STS 비호환).
	// ⚠ 구 주석의 "mongos setParameter 불요" 는 오류였다(ADR-0039 #5 / 라이브 실측 2026-07-14 —
	// mongos 에 mongot 엔드포인트가 없으면 $search·인덱스 관리 명령이 SearchNotEnabled 로 거부).
	// mongos 배선은 BuildMongosDeployment + BuildMongotService(ADR-0040) 참조. config server 는
	// mongot 미배포(메타데이터만). annotation 부재 시 무변경 = 무롤링(mongot_test no-roll 가드).
	// RS 와 동일하게 data PVC subPath(search-index) 공유 — VCT 불변 보존(신규 VCT 없음).
	if img := mdbsh.Annotations[MongotSidecarImageAnnotation]; img != "" {
		mc, ic, mvols := MongotSidecar(name, img, mdbsh.Annotations[MongotSyncSecretAnnotation])
		// mongod(Containers[0]) args 에 setParameter(mongotHost=localhost:27028) 추가.
		sts.Spec.Template.Spec.Containers[0].Args = append(sts.Spec.Template.Spec.Containers[0].Args,
			MongotSetParameterArgs(fmt.Sprintf("localhost:%d", mongotGRPCPort), mdbsh.Annotations[MongotTLSModeAnnotation])...)
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, ic)
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers, mc)
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, mvols...)
		// mongot Service(BuildMongotService) selector 표식 — pod template 에만 부착한다.
		// Selector.MatchLabels 와 Template.Labels 가 같은 map 인스턴스라 in-place 수정 시 STS
		// Selector(immutable)까지 오염 → apply 실패. 반드시 복사본에 추가한다.
		tmplLabels := make(map[string]string, len(sts.Spec.Template.Labels)+1)
		for k, v := range sts.Spec.Template.Labels {
			tmplLabels[k] = v
		}
		tmplLabels[MongotPodLabel] = MongotPodLabelValue
		sts.Spec.Template.Labels = tmplLabels
	}

	// cycle 16: sharded Shard 의 audit forwarder sidecar.
	//
	// PITR oplog tailer 미주입 — 사유는 BuildConfigServerStatefulSet 의 동일
	// 위치 주석 참조 (S3 키에 shard 차원 부재 → 체인 혼입 = 손상, RS-only).
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
				{Name: metricsPortName, Port: metricsPort, TargetPort: intstr.FromInt(metricsPort)},
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

	// mongot(MongoDB Search) 컨트롤면 배선 — mongos 는 mongot 사이드카가 *없다*. 그러나
	// MongoDBSearchIndex 컨트롤러는 인덱스 관리 명령($listSearchIndexes / createSearchIndex)을 mongos
	// 경유로 보내므로, mongos 에 mongot 엔드포인트가 비어 있으면 SearchNotEnabled 로 거부되어 인덱스
	// CR 이 Pending 고착한다(라이브 실측 2026-07-14 — 구 주석의 "mongos setParameter 불요" 가정 오류).
	// 따라서 pod 밖 mongot Service(BuildMongotService)를 가리킨다. 그 Service 는 **단일 shard pin**
	// (spec.router.mongotShard, 기본 shard-0) — mongos 는 이 엔드포인트에 *직접 연결*하며 broadcast
	// 하지 않으므로(ADR-0039 #7) LB 엔드포인트를 주면 비결정적 빈 결과가 난다. multi-shard 분산
	// 컬렉션 검색은 upstream 한계로 여전히 미해결(ADR-0039 Decision #2) — 본 배선의 범위 밖.
	// annotation 부재(= search 비활성) 시 args 무변경 = mongos 무롤링(mongot_sharded_test no-roll 가드).
	if mdbsh.Annotations[MongotSidecarImageAnnotation] != "" {
		args = append(args, MongotSetParameterArgs(
			MongotServiceEndpoint(mdbsh.Name, mdbsh.Namespace),
			mdbsh.Annotations[MongotTLSModeAnnotation])...)
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
			Image:   getMongoDBImage(effectiveMongoDBVersion(mdbsh.Spec.Version, mdbsh.Status.EffectiveVersion)),
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
				{Name: metricsPortName, ContainerPort: metricsPort},
			},
			Args: []string{"--collect-all", "--compatible-mode"},
			Env: []corev1.EnvVar{
				{
					Name: envMongoDBURI,
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
