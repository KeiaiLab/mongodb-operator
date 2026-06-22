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
	"k8s.io/apimachinery/pkg/api/resource"

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
