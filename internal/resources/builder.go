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

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	mongoDBPort   = 27017
	metricsPort   = 9216
	defaultImage  = "mongo:8.2"
	exporterImage = "percona/mongodb_exporter:0.40"
)

// Helper functions
func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }
func boolPtr(b bool) *bool    { return &b }

func generateRandomKey(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return base64.StdEncoding.EncodeToString(bytes)
}

func getMongoDBImage(version mongodbv1alpha1.MongoDBVersion) string {
	if version.Image != "" {
		return version.Image
	}
	return fmt.Sprintf("mongo:%s", version.Version)
}

func buildLabels(name, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "mongodb",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "mongodb-operator",
	}
}

func buildResourceRequirements(spec mongodbv1alpha1.ResourcesSpec) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: spec.Requests,
		Limits:   spec.Limits,
	}
}

func buildDefaultSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		FSGroup:      int64Ptr(999),
		RunAsUser:    int64Ptr(999),
		RunAsGroup:   int64Ptr(999),
		RunAsNonRoot: boolPtr(true),
	}
}

func buildDefaultContainerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsNonRoot:             boolPtr(true),
		RunAsUser:                int64Ptr(999),
		AllowPrivilegeEscalation: boolPtr(false),
		ReadOnlyRootFilesystem:   boolPtr(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
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
	return fmt.Sprintf(`#!/bin/bash
set -e
mongosh --quiet --port %d --eval "db.adminCommand('ping')" > /dev/null 2>&1
`, port)
}

// buildAdminBootstrapScript는 lifecycle.postStart에서 localhost exception을 사용해
// 첫 admin user를 만드는 스크립트를 반환한다.
//
// 보안 메모:
//   - password는 process.env가 아닌 Secret Volume(/etc/mongodb-admin/password)에서
//     fs.readFileSync로 읽음 → ps/audit 로그에 password 노출 0건.
//   - JS literal에 password 문자열이 들어가지 않아 인젝션 차단.
//   - 이미 user가 존재하면 idempotent no-op.
//
// port 매개변수: ReplicaSet=27017, Config Server=27019, Shard=27018.
func buildAdminBootstrapScript(port int) string {
	return fmt.Sprintf(`#!/bin/bash
set -eu

# mongod이 응답할 때까지 최대 120초 대기 (60회 × 2초).
for i in $(seq 1 60); do
  if mongosh --quiet --port %d --eval "db.adminCommand('ping').ok" > /dev/null 2>&1; then
    break
  fi
  sleep 2
done

# 이미 user가 있으면 idempotent no-op.
EXISTING=$(mongosh --quiet --port %d admin --eval "db.system.users.countDocuments({user:'admin'})" 2>/dev/null || echo 0)
if [ "$EXISTING" != "0" ]; then
  echo "admin user already exists, skipping bootstrap"
  exit 0
fi

# password는 mongosh stdin으로 fs.readFileSync로 읽어 직접 createUser에 전달.
# JS literal에 password 문자열이 들어가지 않으므로 인젝션 위험도 없다.
mongosh --quiet --port %d admin <<'EOF'
const fs = require('fs');
const pw = fs.readFileSync('/etc/mongodb-admin/password', 'utf8').trim();
db.createUser({
  user: 'admin',
  pwd: pw,
  roles: [{ role: 'root', db: 'admin' }],
});
EOF
echo "admin user bootstrap complete"
`, port, port, port)
}

// buildAdminCredentialsVolume은 admin password Secret을 0400으로 mount하는 Volume을
// 만든다. 호출자는 secretName이 비어있지 않은지 미리 검증해야 한다.
func buildAdminCredentialsVolume(secretName string) corev1.Volume {
	return corev1.Volume{
		Name: "admin-credentials",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  secretName,
				DefaultMode: int32Ptr(0400),
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
				DefaultMode: int32Ptr(0755),
			},
		},
	}
}

// buildScriptsMount는 /scripts 경로에 ConfigMap을 read-only로 마운트한다.
func buildScriptsMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: "scripts", MountPath: "/scripts", ReadOnly: true}
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
					DefaultMode: int32Ptr(0400),
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

	// Init container to copy keyfile with correct permissions
	// Runs as mongodb user (999) and uses FSGroup for proper file ownership
	initContainers := []corev1.Container{
		{
			Name:  "copy-keyfile",
			Image: "busybox:1.36",
			Command: []string{
				"sh", "-c",
				"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
				{Name: "keyfile", MountPath: "/keyfile"},
			},
			SecurityContext: &corev1.SecurityContext{
				RunAsUser:                int64Ptr(999),
				RunAsGroup:               int64Ptr(999),
				RunAsNonRoot:             boolPtr(true),
				AllowPrivilegeEscalation: boolPtr(false),
			},
		},
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
			Env: []corev1.EnvVar{
				{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
				}},
				{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
				}},
			},
		},
	}

	// Add exporter sidecar if monitoring enabled
	if mdb.Spec.Monitoring != nil && mdb.Spec.Monitoring.Enabled {
		exporterImg := exporterImage
		if mdb.Spec.Monitoring.Exporter != nil && mdb.Spec.Monitoring.Exporter.Image != "" {
			exporterImg = mdb.Spec.Monitoring.Exporter.Image
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
					SecurityContext: securityContext,
					InitContainers:  initContainers,
					Containers:      containers,
					Volumes:         volumes,
					Affinity:        buildDefaultAffinity(mdb.Name),
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
		},
	}
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
					DefaultMode: int32Ptr(0400),
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
							Image: "busybox:1.36",
							Command: []string{
								"sh", "-c",
								"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
								{Name: "keyfile", MountPath: "/keyfile"},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:                int64Ptr(999),
								RunAsGroup:               int64Ptr(999),
								RunAsNonRoot:             boolPtr(true),
								AllowPrivilegeEscalation: boolPtr(false),
							},
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
					DefaultMode: int32Ptr(0400),
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
							Image: "busybox:1.36",
							Command: []string{
								"sh", "-c",
								"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
								{Name: "keyfile", MountPath: "/keyfile"},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:                int64Ptr(999),
								RunAsGroup:               int64Ptr(999),
								RunAsNonRoot:             boolPtr(true),
								AllowPrivilegeEscalation: boolPtr(false),
							},
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

	// mongos container의 volume mounts. admin-credentials와 scripts는
	// AdminCredentialsSecretRef가 설정된 경우에만 추가.
	mongosVolumeMounts := []corev1.VolumeMount{
		{Name: "keyfile", MountPath: "/etc/mongodb-keyfile", ReadOnly: true},
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
							Image: "busybox:1.36",
							Command: []string{
								"sh", "-c",
								"cp /keyfile-secret/keyfile /keyfile/keyfile && chmod 400 /keyfile/keyfile",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "keyfile-secret", MountPath: "/keyfile-secret", ReadOnly: true},
								{Name: "keyfile", MountPath: "/keyfile"},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:                int64Ptr(999),
								RunAsGroup:               int64Ptr(999),
								RunAsNonRoot:             boolPtr(true),
								AllowPrivilegeEscalation: boolPtr(false),
							},
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
					DefaultMode: int32Ptr(0400),
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
						DefaultMode: int32Ptr(0755),
					},
				},
			},
			corev1.Volume{
				Name: "admin-credentials",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name,
						DefaultMode: int32Ptr(0400),
					},
				},
			},
		)
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

	if backup.Spec.Storage.Type == "s3" {
		return fmt.Sprintf(`
set -e
BACKUP_NAME="%s-$(date +%%Y%%m%%d-%%H%%M%%S)"
echo "Starting backup: ${BACKUP_NAME}"

# Install aws-cli
apt-get update && apt-get install -y awscli

# Create backup and upload to S3
mongodump --uri="${MONGODB_URI}" %s --archive | \
    aws s3 cp - "s3://${S3_BUCKET}/${S3_PREFIX}${BACKUP_NAME}.archive.gz" \
    --endpoint-url="${S3_ENDPOINT}"

echo "Backup completed: ${BACKUP_NAME}"
`, backup.Spec.ClusterRef.Name, compressionFlag)
	}

	return fmt.Sprintf(`
set -e
BACKUP_NAME="%s-$(date +%%Y%%m%%d-%%H%%M%%S)"
echo "Starting backup: ${BACKUP_NAME}"
mongodump --uri="${MONGODB_URI}" --out="/backup/${BACKUP_NAME}" %s
echo "Backup completed: ${BACKUP_NAME}"
`, backup.Spec.ClusterRef.Name, compressionFlag)
}
