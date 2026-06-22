/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

func newTestSearch() *mongodbv1beta1.MongoDBSearch {
	return &mongodbv1beta1.MongoDBSearch{}
}

func TestMongotImage_Default(t *testing.T) {
	assert.Equal(t, "mongodb/mongodb-community-search:latest", mongotImage(mongodbv1beta1.MongotVersion{}),
		"기본 이미지 = 검증된 Community mongot")
	assert.Equal(t, "mongodb/mongodb-community-search:1.0.0", mongotImage(mongodbv1beta1.MongotVersion{Version: "1.0.0"}))
	assert.Equal(t, "custom/mongot:x", mongotImage(mongodbv1beta1.MongotVersion{Image: "custom/mongot:x", Version: "1.0.0"}),
		"Image override 가 Version 보다 우선")
}

func TestBuildMongotStatefulSet(t *testing.T) {
	s := newTestSearch()
	s.Name = "ks"
	s.Namespace = "data"
	s.Spec.Replicas = 1
	s.Spec.Storage.Size = resource.MustParse("20Gi")

	sts := BuildMongotStatefulSet(s, "ks-mongot-sync")
	require.NotNil(t, sts)
	assert.Equal(t, "ks-mongot", sts.Name)
	assert.Equal(t, "ks-mongot", sts.Spec.ServiceName)
	require.Len(t, sts.Spec.Template.Spec.Containers, 1)

	c := sts.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "mongodb/mongodb-community-search:latest", c.Image)
	// 포트 27028(gRPC) + 27029(health) — 공식 mongot 사양.
	ports := map[int32]bool{}
	for _, p := range c.Ports {
		ports[p.ContainerPort] = true
	}
	assert.True(t, ports[mongotGRPCPort], "27028 gRPC 포트")
	assert.True(t, ports[mongotHealthPort], "27029 health 포트")

	// data PVC(인덱스 스토어) + config + sync-secret 볼륨.
	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	assert.Equal(t, "data", sts.Spec.VolumeClaimTemplates[0].Name)
	volNames := map[string]bool{}
	for _, v := range sts.Spec.Template.Spec.Volumes {
		volNames[v.Name] = true
	}
	assert.True(t, volNames["config"], "config 볼륨")
	assert.True(t, volNames["sync-secret"], "sync-secret 볼륨(syncSecretName 제공 시)")

	// 보안: non-root + automountSAToken=false (cluster hardening 정합).
	require.NotNil(t, sts.Spec.Template.Spec.AutomountServiceAccountToken)
	assert.False(t, *sts.Spec.Template.Spec.AutomountServiceAccountToken)
	assert.NotNil(t, sts.Spec.Template.Spec.SecurityContext)
}

func TestBuildMongotService(t *testing.T) {
	s := newTestSearch()
	s.Name = "ks"
	s.Namespace = "data"
	svc := BuildMongotService(s)
	assert.Equal(t, "ks-mongot", svc.Name)
	assert.Equal(t, "None", svc.Spec.ClusterIP, "headless")
	ports := map[int32]bool{}
	for _, p := range svc.Spec.Ports {
		ports[p.Port] = true
	}
	assert.True(t, ports[mongotGRPCPort] && ports[mongotHealthPort])
}

func TestBuildMongotConfigMap(t *testing.T) {
	s := newTestSearch()
	s.Name = "ks"
	s.Namespace = "data"
	cm := BuildMongotConfigMap(s, []string{"ks-0.ks-headless.data.svc:27017"}, "search-sync", true)
	assert.Equal(t, "ks-mongot-config", cm.Name)
	cfg := cm.Data["config.yml"]
	require.NotEmpty(t, cfg)
	assert.Contains(t, cfg, "ks-0.ks-headless.data.svc:27017", "sync source host")
	assert.Contains(t, cfg, "search-sync", "searchCoordinator user")
	assert.Contains(t, cfg, "requireTLS", "tlsEnabled=true → requireTLS")
	assert.Contains(t, cfg, "27028", "gRPC 포트")
}

func TestBuildMongotNetworkPolicy(t *testing.T) {
	s := newTestSearch()
	s.Name = "ks"
	s.Namespace = "data"
	np := BuildMongotNetworkPolicy(s)
	assert.Equal(t, "ks-mongot", np.Name)
	require.Len(t, np.Spec.Ingress, 1)
	require.Len(t, np.Spec.Egress, 2, "egress: mongod(27017) + DNS(53)")
	// ingress 27028/27029
	ingPorts := map[int32]bool{}
	for _, p := range np.Spec.Ingress[0].Ports {
		ingPorts[p.Port.IntVal] = true
	}
	assert.True(t, ingPorts[mongotGRPCPort] && ingPorts[mongotHealthPort])
}

func TestMongotSetParameterArgs(t *testing.T) {
	// endpoint 비어있으면 nil → mongod template 무변경(무롤링 — 컷오버 교훈).
	assert.Nil(t, MongotSetParameterArgs("", ""))

	args := MongotSetParameterArgs("ks-mongot.data.svc.cluster.local:27028", "disabled")
	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "mongotHost=ks-mongot.data.svc.cluster.local:27028")
	assert.Contains(t, joined, "searchIndexManagementHostAndPort=ks-mongot.data.svc.cluster.local:27028")
	assert.Contains(t, joined, "searchTLSMode=disabled")
	assert.Contains(t, joined, "useGrpcForSearch=true")
}

func TestMongotEndpoint(t *testing.T) {
	assert.Equal(t, "ks-mongot.data.svc.cluster.local:27028", MongotEndpoint("ks", "data"))
}

// TestMongod_SearchAnnotation_NoRoll — 무롤링 계약(cross-review 역방향): annotation 부재
// 시 mongod STS args 에 mongotHost 없음(기존 프로덕션 mongod 무변경) + annotation 존재 시 주입.
func TestMongod_SearchAnnotation_NoRoll(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "ks", Namespace: "data"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version:        mongodbv1alpha1.MongoDBVersion{Version: "8.3"},
			Storage:        mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")},
		},
	}
	// annotation 부재 → mongotHost 없음(무롤링 baseline).
	assert.False(t, stsHasMongotHost(BuildReplicaSetStatefulSet(mdb)),
		"MongoDBSearch 부재 시 mongod STS 에 mongotHost 없어야 함(무롤링)")

	// annotation 존재 → mongotHost 주입.
	mdb.Annotations = map[string]string{
		MongotSearchEndpointAnnotation: "ks-mongot.data.svc.cluster.local:27028",
		MongotTLSModeAnnotation:        "disabled",
	}
	assert.True(t, stsHasMongotHost(BuildReplicaSetStatefulSet(mdb)),
		"annotation 존재 시 mongotHost setParameter 주입")
}

func stsHasMongotHost(sts *appsv1.StatefulSet) bool {
	for _, c := range sts.Spec.Template.Spec.Containers {
		for _, a := range c.Args {
			if strings.Contains(a, "mongotHost=") {
				return true
			}
		}
	}
	return false
}
