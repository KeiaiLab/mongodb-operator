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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// shardedWithAuth는 admin bootstrap 활성화에 필요한 AdminCredentialsSecretRef와
// 기본 ConfigServer/Shards 스펙을 갖춘 MongoDBSharded fixture를 만든다.
func shardedWithAuth() *mongodbv1alpha1.MongoDBSharded {
	return &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sharded",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{Version: "7.0"},
			Auth: mongodbv1alpha1.AuthSpec{
				AdminCredentialsSecretRef: corev1.LocalObjectReference{
					Name: "test-sharded-admin",
				},
			},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{
				Members: 3,
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")},
			},
			Shards: mongodbv1alpha1.ShardSpec{
				Count:           2,
				MembersPerShard: 3,
				Storage:         mongodbv1alpha1.StorageSpec{Size: resource.MustParse("50Gi")},
			},
		},
	}
}

// hasVolume는 Pod spec의 Volumes 중 name과 일치하는 Volume을 반환한다.
func findVolume(t *testing.T, volumes []corev1.Volume, name string) *corev1.Volume {
	t.Helper()
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i]
		}
	}
	return nil
}

func findVolumeMount(t *testing.T, mounts []corev1.VolumeMount, name string) *corev1.VolumeMount {
	t.Helper()
	for i := range mounts {
		if mounts[i].Name == name {
			return &mounts[i]
		}
	}
	return nil
}

func TestBuildKeyfileSecret(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
	}

	secret := BuildKeyfileSecret(mdb)

	assert.Equal(t, "test-mongodb-keyfile", secret.Name)
	assert.Equal(t, "default", secret.Namespace)
	assert.Equal(t, corev1.SecretTypeOpaque, secret.Type)
	assert.Contains(t, secret.Data, "keyfile")
	assert.NotEmpty(t, secret.Data["keyfile"])
}

func TestBuildHeadlessService(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
	}

	svc := BuildHeadlessService(mdb)

	assert.Equal(t, "test-mongodb-headless", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.True(t, svc.Spec.PublishNotReadyAddresses)
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(27017), svc.Spec.Ports[0].Port)
}

func TestBuildClientService(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
	}

	svc := BuildClientService(mdb)

	assert.Equal(t, "test-mongodb", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)
	assert.Len(t, svc.Spec.Ports, 2)
}

func TestBuildReplicaSetStatefulSet(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			Storage: mongodbv1alpha1.StorageSpec{
				Size:        resource.MustParse("10Gi"),
				DataDirPath: "/data/db",
			},
		},
	}

	sts := BuildReplicaSetStatefulSet(mdb)

	assert.Equal(t, "test-mongodb", sts.Name)
	assert.Equal(t, "default", sts.Namespace)
	assert.Equal(t, int32(3), *sts.Spec.Replicas)
	assert.Equal(t, "test-mongodb-headless", sts.Spec.ServiceName)
	assert.Len(t, sts.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "mongodb", sts.Spec.Template.Spec.Containers[0].Name)
}

func TestBuildReplicaSetStatefulSetWithStorageClass(t *testing.T) {
	storageClass := "fast-storage"
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			Storage: mongodbv1alpha1.StorageSpec{
				StorageClassName: storageClass,
				Size:             resource.MustParse("20Gi"),
				DataDirPath:      "/data/db",
			},
		},
	}

	sts := BuildReplicaSetStatefulSet(mdb)

	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	require.NotNil(t, sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName)
	assert.Equal(t, storageClass, *sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName)
}

func TestBuildReplicaSetStatefulSetWithoutStorageClass(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			Storage: mongodbv1alpha1.StorageSpec{
				Size:        resource.MustParse("10Gi"),
				DataDirPath: "/data/db",
			},
		},
	}

	sts := BuildReplicaSetStatefulSet(mdb)

	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	assert.Nil(t, sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName)
}

func TestBuildReplicaSetStatefulSetWithMonitoring(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			Storage: mongodbv1alpha1.StorageSpec{
				Size:        resource.MustParse("10Gi"),
				DataDirPath: "/data/db",
			},
			Monitoring: &mongodbv1alpha1.MonitoringSpec{
				Enabled: true,
			},
		},
	}

	sts := BuildReplicaSetStatefulSet(mdb)

	// Should have 2 containers: mongodb and exporter
	assert.Len(t, sts.Spec.Template.Spec.Containers, 2)
	assert.Equal(t, "mongodb", sts.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "exporter", sts.Spec.Template.Spec.Containers[1].Name)
}

func TestBuildLabels(t *testing.T) {
	labels := buildLabels("my-instance", "replicaset")

	assert.Equal(t, "mongodb", labels["app.kubernetes.io/name"])
	assert.Equal(t, "my-instance", labels["app.kubernetes.io/instance"])
	assert.Equal(t, "replicaset", labels["app.kubernetes.io/component"])
	assert.Equal(t, "mongodb-operator", labels["app.kubernetes.io/managed-by"])
}

func TestGetMongoDBImage(t *testing.T) {
	tests := []struct {
		name     string
		version  mongodbv1alpha1.MongoDBVersion
		expected string
	}{
		{
			name: "version only",
			version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			expected: "mongo:7.0",
		},
		{
			name: "custom image",
			version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
				Image:   "myregistry/mongo:7.0-custom",
			},
			expected: "myregistry/mongo:7.0-custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMongoDBImage(tt.version)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildMongoDBConfigMap(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mongodb",
			Namespace: "default",
		},
	}

	cm := BuildMongoDBConfigMap(mdb)

	assert.Equal(t, "test-mongodb-scripts", cm.Name)
	assert.Equal(t, "default", cm.Namespace)
	assert.Contains(t, cm.Data, "readiness-probe.sh")
}

func TestBuildConfigServerStatefulSet(t *testing.T) {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sharded",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{
				Members: 3,
				Storage: mongodbv1alpha1.StorageSpec{
					Size: resource.MustParse("10Gi"),
				},
			},
		},
	}

	sts := BuildConfigServerStatefulSet(mdbsh)

	assert.Equal(t, "test-sharded-cfg", sts.Name)
	assert.Equal(t, int32(3), *sts.Spec.Replicas)
	assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args, "--configsvr")
}

func TestBuildShardStatefulSet(t *testing.T) {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sharded",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			Shards: mongodbv1alpha1.ShardSpec{
				Count:           2,
				MembersPerShard: 3,
				Storage: mongodbv1alpha1.StorageSpec{
					Size: resource.MustParse("50Gi"),
				},
			},
		},
	}

	sts := BuildShardStatefulSet(mdbsh, 0)

	assert.Equal(t, "test-sharded-shard-0", sts.Name)
	assert.Equal(t, int32(3), *sts.Spec.Replicas)
	assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args, "--shardsvr")
}

func TestBuildShardStatefulSet_HasAdminBootstrap(t *testing.T) {
	mdbsh := shardedWithAuth()

	sts := BuildShardStatefulSet(mdbsh, 0)

	podSpec := sts.Spec.Template.Spec
	require.NotNil(t, findVolume(t, podSpec.Volumes, "admin-credentials"),
		"shard StatefulSet은 admin-credentials Volume을 가져야 한다")
	require.NotNil(t, findVolume(t, podSpec.Volumes, "scripts"),
		"shard StatefulSet은 scripts Volume을 가져야 한다")

	// 컨테이너 마운트와 lifecycle 검증.
	require.Len(t, podSpec.Containers, 1)
	mongod := podSpec.Containers[0]
	require.NotNil(t, findVolumeMount(t, mongod.VolumeMounts, "admin-credentials"),
		"mongod 컨테이너에 admin-credentials VolumeMount가 있어야 한다")
	require.NotNil(t, findVolumeMount(t, mongod.VolumeMounts, "scripts"),
		"mongod 컨테이너에 scripts VolumeMount가 있어야 한다")
	require.NotNil(t, mongod.Lifecycle, "Lifecycle이 설정되어야 한다")
	require.NotNil(t, mongod.Lifecycle.PostStart)
	require.NotNil(t, mongod.Lifecycle.PostStart.Exec)
	assert.Equal(t, []string{"/scripts/bootstrap-admin.sh"},
		mongod.Lifecycle.PostStart.Exec.Command)

	// scripts Volume이 shard별 ConfigMap을 가리켜야 함.
	scriptsVol := findVolume(t, podSpec.Volumes, "scripts")
	require.NotNil(t, scriptsVol.ConfigMap)
	assert.Equal(t, "test-sharded-shard-0-scripts", scriptsVol.ConfigMap.Name)
}

func TestBuildShardStatefulSet_NoAuthSkipsBootstrap(t *testing.T) {
	// AdminCredentialsSecretRef가 비어있으면 admin volume/lifecycle이 추가되지 않아야 한다.
	mdbsh := shardedWithAuth()
	mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name = ""

	sts := BuildShardStatefulSet(mdbsh, 0)
	podSpec := sts.Spec.Template.Spec
	assert.Nil(t, findVolume(t, podSpec.Volumes, "admin-credentials"))
	assert.Nil(t, findVolume(t, podSpec.Volumes, "scripts"))
	assert.Nil(t, podSpec.Containers[0].Lifecycle)
}

func TestBuildConfigServerStatefulSet_HasAdminBootstrap(t *testing.T) {
	mdbsh := shardedWithAuth()

	sts := BuildConfigServerStatefulSet(mdbsh)

	podSpec := sts.Spec.Template.Spec
	require.NotNil(t, findVolume(t, podSpec.Volumes, "admin-credentials"))
	require.NotNil(t, findVolume(t, podSpec.Volumes, "scripts"))

	require.Len(t, podSpec.Containers, 1)
	mongod := podSpec.Containers[0]
	require.NotNil(t, findVolumeMount(t, mongod.VolumeMounts, "admin-credentials"))
	require.NotNil(t, findVolumeMount(t, mongod.VolumeMounts, "scripts"))
	require.NotNil(t, mongod.Lifecycle)
	require.NotNil(t, mongod.Lifecycle.PostStart)
	assert.Equal(t, []string{"/scripts/bootstrap-admin.sh"},
		mongod.Lifecycle.PostStart.Exec.Command)

	scriptsVol := findVolume(t, podSpec.Volumes, "scripts")
	require.NotNil(t, scriptsVol.ConfigMap)
	assert.Equal(t, "test-sharded-cfg-scripts", scriptsVol.ConfigMap.Name)
}

func TestBuildConfigServerScriptsConfigMap_PortMatches(t *testing.T) {
	mdbsh := shardedWithAuth()

	cm := BuildConfigServerScriptsConfigMap(mdbsh)

	assert.Equal(t, "test-sharded-cfg-scripts", cm.Name)
	assert.Equal(t, "default", cm.Namespace)
	require.Contains(t, cm.Data, "bootstrap-admin.sh")
	require.Contains(t, cm.Data, "readiness-probe.sh")
	assert.Contains(t, cm.Data["bootstrap-admin.sh"], `MONGO_PORT:-27019`,
		"cfg server bootstrap script는 27019 포트를 디폴트로 써야 한다")
	assert.Contains(t, cm.Data["readiness-probe.sh"], "--port 27019")
}

func TestBuildShardScriptsConfigMap_PortMatches(t *testing.T) {
	mdbsh := shardedWithAuth()

	cm := BuildShardScriptsConfigMap(mdbsh, 1)

	assert.Equal(t, "test-sharded-shard-1-scripts", cm.Name)
	assert.Equal(t, "default", cm.Namespace)
	require.Contains(t, cm.Data, "bootstrap-admin.sh")
	require.Contains(t, cm.Data, "readiness-probe.sh")
	assert.Contains(t, cm.Data["bootstrap-admin.sh"], `MONGO_PORT:-27018`,
		"shard bootstrap script는 27018 포트를 디폴트로 써야 한다")
	assert.Contains(t, cm.Data["readiness-probe.sh"], "--port 27018")
}

func TestBuildAdminBootstrapScript_NoSecretLeak(t *testing.T) {
	// 모든 RS port에 대해 환경변수로 password가 노출되지 않는지 검증.
	for _, port := range []int{27017, 27018, 27019} {
		script := buildAdminBootstrapScript(port)

		// envvar 패턴으로 password가 export/노출되면 안 된다.
		// fs.readFileSync('/etc/mongodb-admin/password') 패턴만 사용해야 함.
		lower := strings.ToLower(script)
		assert.NotContains(t, lower, "export password",
			"port %d: password를 export하면 안 됨", port)
		assert.NotContains(t, lower, "$password",
			"port %d: shell variable로 password를 다루면 안 됨", port)
		assert.NotContains(t, script, "MONGODB_PASSWORD=",
			"port %d: env로 password를 노출하면 안 됨", port)
		assert.NotContains(t, script, "ADMIN_PASSWORD=",
			"port %d: env로 admin password를 노출하면 안 됨", port)

		// 올바른 패턴: file로부터 읽기.
		assert.Contains(t, script, "fs.readFileSync('/etc/mongodb-admin/password'",
			"port %d: password는 mounted Secret file에서 읽어야 함", port)
	}
}

func TestBuildMongosDeployment(t *testing.T) {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sharded",
			Namespace: "default",
		},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{
				Version: "7.0",
			},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{
				Members: 3,
			},
			Mongos: mongodbv1alpha1.MongosSpec{
				Replicas: 2,
			},
		},
	}

	deploy := BuildMongosDeployment(mdbsh)

	assert.Equal(t, "test-sharded-mongos", deploy.Name)
	assert.Equal(t, int32(2), *deploy.Spec.Replicas)
	assert.Equal(t, "mongos", deploy.Spec.Template.Spec.Containers[0].Command[0])
}

// 본 사이클 Track C1/C2/C4 신규 helper 회귀 가드 (심층 검수에서 0% 커버리지로 식별).

func TestBuildMongoDBPDB_NilWhenDisabledOrMissing(t *testing.T) {
	cases := []struct {
		name string
		spec *mongodbv1alpha1.PodDisruptionBudgetSpec
	}{
		{"nil_spec", nil},
		{"explicit_disabled", &mongodbv1alpha1.PodDisruptionBudgetSpec{Enabled: false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mdb := &mongodbv1alpha1.MongoDB{
				ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
				Spec:       mongodbv1alpha1.MongoDBSpec{Members: 3, PodDisruptionBudget: c.spec},
			}
			if pdb := BuildMongoDBPDB(mdb); pdb != nil {
				t.Fatalf("기대 nil(미생성), got %+v", pdb)
			}
		})
	}
}

func TestBuildMongoDBPDB_DefaultMinAvailableIsReplicasMinusOne(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:             3,
			PodDisruptionBudget: &mongodbv1alpha1.PodDisruptionBudgetSpec{Enabled: true},
		},
	}
	pdb := BuildMongoDBPDB(mdb)
	if pdb == nil {
		t.Fatal("기대 PDB non-nil")
	}
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != 2 {
		t.Fatalf("기대 minAvailable=2 (replicas-1), got %+v", pdb.Spec.MinAvailable)
	}
	if pdb.Spec.MaxUnavailable != nil {
		t.Fatalf("기대 maxUnavailable nil, got %+v", pdb.Spec.MaxUnavailable)
	}
	if pdb.Spec.Selector == nil || pdb.Spec.Selector.MatchLabels["app.kubernetes.io/component"] != "replicaset" {
		t.Fatalf("selector component label 잘못: %+v", pdb.Spec.Selector)
	}
}

func TestBuildShardedPDBs_NilWhenDisabled(t *testing.T) {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		Spec: mongodbv1alpha1.MongoDBShardedSpec{},
	}
	if out := BuildShardedPDBs(mdbsh); out != nil {
		t.Fatalf("기대 nil, got %d items", len(out))
	}
}

func TestBuildShardedPDBs_GeneratesAllComponents(t *testing.T) {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			ConfigServer:        mongodbv1alpha1.ConfigServerSpec{Members: 3},
			Shards:              mongodbv1alpha1.ShardSpec{Count: 2, MembersPerShard: 3},
			Mongos:              mongodbv1alpha1.MongosSpec{Replicas: 2},
			PodDisruptionBudget: &mongodbv1alpha1.PodDisruptionBudgetSpec{Enabled: true},
		},
	}
	out := BuildShardedPDBs(mdbsh)
	// 1 cfg + 2 shards + 1 mongos = 4
	if len(out) != 4 {
		t.Fatalf("기대 4 PDBs (cfg + 2 shards + mongos), got %d", len(out))
	}
	names := map[string]bool{}
	for _, pdb := range out {
		names[pdb.Name] = true
	}
	expected := []string{"sh-cfg-pdb", "sh-shard-0-pdb", "sh-shard-1-pdb", "sh-mongos-pdb"}
	for _, n := range expected {
		if !names[n] {
			t.Errorf("PDB name %q 누락: actual=%v", n, names)
		}
	}
}

func TestBuildMongoDBNetworkPolicy_NilWhenDisabled(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		Spec: mongodbv1alpha1.MongoDBSpec{},
	}
	if np := BuildMongoDBNetworkPolicy(mdb); np != nil {
		t.Fatalf("기대 nil, got %+v", np)
	}
}

func TestBuildMongoDBNetworkPolicy_DenyByDefaultPlusIntraCluster(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			NetworkPolicy: &mongodbv1alpha1.NetworkPolicySpec{Enabled: true},
		},
	}
	np := BuildMongoDBNetworkPolicy(mdb)
	if np == nil {
		t.Fatal("기대 non-nil")
	}
	if np.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"] != "replicaset" {
		t.Fatalf("podSelector component 잘못: %+v", np.Spec.PodSelector)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("기대 1 ingress(intra-cluster), got %d", len(np.Spec.Ingress))
	}
	port := np.Spec.Ingress[0].Ports[0].Port.IntValue()
	if port != 27017 {
		t.Fatalf("기대 port=27017, got %d", port)
	}
}

func TestBuildMongoDBNetworkPolicy_AdditionalIngressFromAppendsRules(t *testing.T) {
	monitoringPodLabel := map[string]string{"app": "prometheus"}
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			NetworkPolicy: &mongodbv1alpha1.NetworkPolicySpec{
				Enabled: true,
				AdditionalIngressFrom: []mongodbv1alpha1.NetworkPolicyPeer{
					{PodSelector: &monitoringPodLabel},
					{PodSelector: nil, NamespaceSelector: nil}, // 무효 — skip 되어야 함
				},
			},
		},
	}
	np := BuildMongoDBNetworkPolicy(mdb)
	// intra-cluster 1 + additional 1 (무효 1건은 skip) = 2
	if len(np.Spec.Ingress) != 2 {
		t.Fatalf("기대 2 ingress(intra + 1 valid additional), got %d (전체: %+v)",
			len(np.Spec.Ingress), np.Spec.Ingress)
	}
}

func TestBuildShardedNetworkPolicies_PerComponentPort(t *testing.T) {
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Shards:        mongodbv1alpha1.ShardSpec{Count: 2},
			NetworkPolicy: &mongodbv1alpha1.NetworkPolicySpec{Enabled: true},
		},
	}
	out := BuildShardedNetworkPolicies(mdbsh)
	if len(out) != 4 {
		t.Fatalf("기대 4 NetworkPolicies, got %d", len(out))
	}
	// cfg=27019, shard=27018(×2), mongos=27017
	expected := map[string]int{
		"sh-cfg-netpol":     27019,
		"sh-shard-0-netpol": 27018,
		"sh-shard-1-netpol": 27018,
		"sh-mongos-netpol":  27017,
	}
	for _, np := range out {
		want, ok := expected[np.Name]
		if !ok {
			t.Errorf("예상 외 NetworkPolicy 이름: %s", np.Name)
			continue
		}
		got := np.Spec.Ingress[0].Ports[0].Port.IntValue()
		if got != want {
			t.Errorf("%s: 기대 port=%d, got %d", np.Name, want, got)
		}
	}
}
