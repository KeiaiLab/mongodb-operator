/*
Copyright 2024 Keiailab.

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

package resources

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

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

	commonslabels "github.com/keiailab/operator-commons/pkg/labels"
	commonsnp "github.com/keiailab/operator-commons/pkg/networkpolicy"
	"github.com/keiailab/operator-commons/pkg/security"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/assets"
)

const (
	mongoDBPort   = 27017
	metricsPort   = 9216
	defaultImage  = "mongo:8.3.1"
	exporterImage = "percona/mongodb_exporter:0.40"
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
// 호환). PSA restricted 정합 (busybox 사용, drop ALL caps).
// Exported — caller (cfg/shard/mongos reconciler) 가 STS build 후 conditional append.
func BuildPEMMergeInitContainer() corev1.Container {
	return corev1.Container{
		Name:            "tls-pem-merge",
		Image:           keyfileInitImage, // busybox:1.37 const 단일화 정합
		Command:         []string{"sh", "-c", "cat /tls-input/tls.crt /tls-input/tls.key > /tls-pem/server.pem && chmod 0400 /tls-pem/server.pem"},
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
// string 은 short hostname (argos-mongo-cfg-0:27017) 사용 — cert SAN 의 wildcard FQDN
// (*.argos-mongo-cfg-headless.<ns>.svc.cluster.local) 와 직접 매치 안 됨 → TLS verify
// fail → sharding pool init 실패 → 27017 미 listen → kubelet liveness kill cascade.
// cluster-internal CA chain + preferTLS 환경에서 hostname 검증은 의미 적음 (CA 로 ID
// 검증 충분), short/long hostname mix 흡수 의무.
func tlsArgs() []string {
	return []string{
		"--tlsMode", "preferTLS",
		"--tlsCertificateKeyFile", MongoTLSPEMPath + "/server.pem",
		"--tlsCAFile", MongoTLSMountPath + "/ca.crt",
		"--tlsAllowConnectionsWithoutCertificates",
		"--tlsAllowInvalidHostnames",
	}
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

// buildLabels — operator-commons/pkg/labels 위임 (iteration 27).
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

func buildDefaultSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		FSGroup:      ptr.To[int64](999),
		RunAsUser:    ptr.To[int64](999),
		RunAsGroup:   ptr.To[int64](999),
		RunAsNonRoot: ptr.To(true),
	}
}

func buildDefaultContainerSecurityContext() *corev1.SecurityContext {
	return security.RestrictedContainer(
		security.WithRunAsUser(999),
		security.WithReadOnlyRootFilesystem(false),
	)
}

// PodSecurity "restricted" 정책을 만족하는 init container용 SecurityContext.
// keyfile 복사 init container 4곳 (replicaset / config server / shard / mongos) 에서
// 동일 정의가 인라인 중복되어 있던 것을 commons 단일 진실원으로 위임.
// 클러스터 사고 (2026-05-07): copy-keyfile container 가 capabilities.drop 과
// seccompProfile 누락으로 PodSecurity restricted 위반 → StatefulSet pod 생성 거부.
// iteration 8: operator-commons/pkg/security 로 통합 — 3 operator 가 동일 패턴 채택.
func buildKeyfileInitContainerSecurityContext() *corev1.SecurityContext {
	return security.RestrictedContainer(
		security.WithRunAsUser(999),
		security.WithRunAsGroup(999),
	)
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

// BuildReplicaSetStatefulSet creates a StatefulSet for MongoDB ReplicaSet
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
		args = append(args, tlsArgs()...)
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
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{
						Command: []string{"mongosh", "--quiet", "--eval", "db.adminCommand('ping')"},
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
						Command: []string{"/scripts/readiness-probe.sh"},
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
				TimeoutSeconds:      5,
			},
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
					Name:  "MONGODB_URI",
					Value: "mongodb://localhost:27017",
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
					TopologySpreadConstraints: defaultedTopologySpread(nil, mdb.Spec.Members, labels),
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
		args = append(args, tlsArgs()...)
	}

	return &appsv1.StatefulSet{
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
		args = append(args, tlsArgs()...)
	}

	return &appsv1.StatefulSet{
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
							// (script 의존 0). argos cycle 21 stop hook 21차 동기.
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
}

// BuildMongosConfigMap creates a ConfigMap for Mongos configuration.
//
// 포함:
//   - configdb: mongos --configdb 인자로 사용되는 connection string 캐시 (read-only mount).
//   - bootstrap-admin.sh: mongos pod 자체가 자기 mongos에 localhost 연결로 첫
//     admin user를 만든다. operator는 pods/exec을 호출하지 않는다. ReplicaSet의
//     동일 패턴(BuildMongoDBConfigMap의 bootstrap-admin.sh)과 동일.
func BuildMongosConfigMap(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.ConfigMap {
	// Build config server connection string
	// Config servers use port 27019
	var configHosts string
	for i := int32(0); i < mdbsh.Spec.ConfigServer.Members; i++ {
		if i > 0 {
			configHosts += ","
		}
		configHosts += fmt.Sprintf("%s-cfg-%d.%s-cfg-headless.%s.svc.cluster.local:27019",
			mdbsh.Name, i, mdbsh.Name, mdbsh.Namespace)
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-mongos-config",
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, "mongos"),
		},
		Data: map[string]string{
			"configdb":           fmt.Sprintf("%s-cfg/%s", mdbsh.Name, configHosts),
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

// BuildMongosDeployment creates a Deployment for Mongos
func BuildMongosDeployment(mdbsh *mongodbv1alpha1.MongoDBSharded) *appsv1.Deployment {
	labels := buildLabels(mdbsh.Name, "mongos")

	// Build config server connection string
	// Config servers use port 27019
	var configHosts string
	for i := int32(0); i < mdbsh.Spec.ConfigServer.Members; i++ {
		if i > 0 {
			configHosts += ","
		}
		configHosts += fmt.Sprintf("%s-cfg-%d.%s-cfg-headless.%s.svc.cluster.local:27019",
			mdbsh.Name, i, mdbsh.Name, mdbsh.Namespace)
	}

	args := []string{
		"--configdb", fmt.Sprintf("%s-cfg/%s", mdbsh.Name, configHosts),
		"--bind_ip_all",
		"--keyFile", "/etc/mongodb-keyfile/keyfile",
	}
	// Pillar P7 Phase 3b — TLS args (mongos, preferTLS plaintext fallback).
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.Enabled {
		args = append(args, tlsArgs()...)
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
				FailureThreshold:    30,
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
						Command: []string{"mongosh", "--quiet", "--eval", "db.adminCommand('ping')"},
					},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       10,
				TimeoutSeconds:      5,
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
				{Name: "MONGODB_URI", Value: "mongodb://localhost:27017"},
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
					Volumes:    buildMongosVolumes(mdbsh),
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
func BuildBackupJob(backup *mongodbv1alpha1.MongoDBBackup, connectionString string) *batchv1.Job {
	labels := buildLabels(backup.Name, "backup")

	backoff := int32(3)
	ttl := int32(86400) // 24 hours

	var envVars []corev1.EnvVar
	envVars = append(envVars, corev1.EnvVar{
		Name:  "MONGODB_URI",
		Value: connectionString,
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
// iteration 26 (2026-05-07): operator-commons/pkg/networkpolicy v0.3.0 위임.
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
		// 아무 metric도 없으면 cpu 80% 기본값 — Bitnami chart 등 표준 기본.
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
