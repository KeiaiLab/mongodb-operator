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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

func TestMongotImage_Default(t *testing.T) {
	assert.Equal(t, "mongodb/mongodb-community-search:latest", MongotImage(mongodbv1beta1.MongotVersion{}),
		"기본 이미지 = 검증된 Community mongot")
	assert.Equal(t, "mongodb/mongodb-community-search:1.0.0", MongotImage(mongodbv1beta1.MongotVersion{Version: "1.0.0"}))
	assert.Equal(t, "custom/mongot:x", MongotImage(mongodbv1beta1.MongotVersion{Image: "custom/mongot:x", Version: "1.0.0"}),
		"Image override 가 Version 보다 우선")
}

func TestBuildMongotConfigMap(t *testing.T) {
	cm := BuildMongotConfigMap("ks", "data", "ks-search", "search-sync", true, MongodReplicaSetPort)
	assert.Equal(t, "ks-mongot-config", cm.Name)
	cfg := cm.Data["config.yml"]
	require.NotEmpty(t, cfg)
	assert.Contains(t, cfg, `hostAndPort: "localhost:27017"`, "RS sidecar — localhost mongod 27017 (단수 hostAndPort)")
	assert.Contains(t, cfg, "search-sync", "searchCoordinator user")
	// gRPC(mongod↔mongot)는 in-pod localhost 평문 → server.grpc.tls.mode 는 항상 유효 enum
	// "DISABLED" 여야 한다. mongod searchTLSMode 값 "requireTLS" 가 grpc.tls.mode 로 새면
	// mongot 이 "must be one of [DISABLED, MTLS, TLS]" 로 config-parse crash 한다
	// (prod sharded(preferTLS) search 활성화 2026-06-24 회귀 가드). mongot↔mongod 양방향
	// localhost 채널은 모두 평문 — grpc DISABLED + syncSource tls:false (mongod preferTLS 평문 수락).
	assert.Contains(t, cfg, `mode: "DISABLED"`, "grpc.tls.mode = DISABLED (localhost 평문)")
	assert.NotContains(t, cfg, "requireTLS", "grpc.tls.mode 에 mongod enum requireTLS 누출 금지")
	assert.Contains(t, cfg, "tls: false", "syncSource(mongot→mongod) localhost 평문 — CA 부재 + mongod preferTLS 수락")
	assert.Contains(t, cfg, `dataPath: "/var/lib/mongot"`, "data dir = base (serverId 영속)")
	assert.Contains(t, cfg, "/etc/mongot/secrets/passwordFile", "owner-only password 경로")
}

// TestBuildMongotConfigMap_ShardPort — Sharded shard mongot 은 27018 로 sync(27017 하드코딩 버그 가드).
func TestBuildMongotConfigMap_ShardPort(t *testing.T) {
	cm := BuildMongotConfigMap("ks-shard-0", "data", "ks-search", "search-sync", false, MongodShardPort)
	cfg := cm.Data["config.yml"]
	assert.Contains(t, cfg, `hostAndPort: "localhost:27018"`, "shard sidecar — localhost mongod 27018(RS 27017 와 다름)")
	assert.NotContains(t, cfg, `localhost:27017`, "shard config 에 27017 누출 금지")
}

func TestMongotSidecar(t *testing.T) {
	mongotC, initC, vols := MongotSidecar("ks", "mongodb/mongodb-community-search:latest", "ks-sync")

	// mongot 컨테이너: 이미지 + 27028 포트 + securityContext.
	assert.Equal(t, "mongot", mongotC.Name)
	assert.Equal(t, "mongodb/mongodb-community-search:latest", mongotC.Image)
	ports := map[int32]bool{}
	for _, p := range mongotC.Ports {
		ports[p.ContainerPort] = true
	}
	assert.True(t, ports[mongotGRPCPort], "27028 gRPC")
	assert.NotNil(t, mongotC.SecurityContext)

	// init 컨테이너: password 0400 복사.
	assert.Equal(t, "copy-mongot-password", initC.Name)
	assert.Contains(t, strings.Join(initC.Command, " "), "chmod 0400", "owner-only password")

	// volumes: config + sync-raw + secrets(emptyDir). data 는 mongod data PVC(STS VCT) 공유 →
	// 여기서 추가 X(노드 디스크 종속 제거 — kind e2e 근본 원인).
	volNames := map[string]bool{}
	for _, v := range vols {
		volNames[v.Name] = true
	}
	assert.True(t, volNames["mongot-config"] && volNames["mongot-sync-raw"] && volNames["mongot-secrets"])
	assert.False(t, volNames["mongot-data"], "mongot-data emptyDir 제거 — mongod data PVC subPath 공유로 대체")

	// mongot 인덱스 스토어 = mongod data PVC("data") 의 subPath(search-index) — 노드 디스크 독립 + 영속.
	var dataMount *corev1.VolumeMount
	for i := range mongotC.VolumeMounts {
		if mongotC.VolumeMounts[i].Name == mongodDataVolume {
			dataMount = &mongotC.VolumeMounts[i]
		}
	}
	require.NotNil(t, dataMount, "mongot 이 mongod data PVC 를 mount")
	assert.Equal(t, mongotBasePath, dataMount.MountPath)
	assert.Equal(t, mongotDataSubPath, dataMount.SubPath, "subPath 로 mongod dbpath 와 분리")
}

func TestMongotSetParameterArgs(t *testing.T) {
	// endpoint 비어있으면 nil → mongod template 무변경(무롤링).
	assert.Nil(t, MongotSetParameterArgs("", ""))

	args := MongotSetParameterArgs("localhost:27028", "disabled")
	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "mongotHost=localhost:27028")
	assert.Contains(t, joined, "searchIndexManagementHostAndPort=localhost:27028")
	assert.Contains(t, joined, "searchTLSMode=disabled")
	assert.Contains(t, joined, "useGrpcForSearch=true")
}

// TestMongod_SearchSidecar_NoRoll — 무롤링 계약: sidecar annotation 부재 시 mongod STS 에
// mongot 컨테이너 + mongotHost 없음(기존 프로덕션 mongod 무변경) + annotation 존재 시 주입.
func TestMongod_SearchSidecar_NoRoll(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "ks", Namespace: "data"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version:        mongodbv1alpha1.MongoDBVersion{Version: "8.3"},
			Storage:        mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")},
		},
	}
	// annotation 부재 → mongot sidecar/mongotHost 없음(무롤링 baseline).
	base := BuildReplicaSetStatefulSet(mdb)
	assert.False(t, stsHasMongotHost(base), "MongoDBSearch 부재 시 mongotHost 없어야(무롤링)")
	assert.False(t, stsHasContainer(base, "mongot"), "MongoDBSearch 부재 시 mongot sidecar 없어야(무롤링)")

	// sidecar annotation 존재 → mongot sidecar + mongotHost 주입.
	mdb.Annotations = map[string]string{
		MongotSidecarImageAnnotation: "mongodb/mongodb-community-search:latest",
		MongotSyncSecretAnnotation:   "ks-sync",
		MongotTLSModeAnnotation:      "disabled",
	}
	with := BuildReplicaSetStatefulSet(mdb)
	assert.True(t, stsHasMongotHost(with), "annotation 시 mongotHost(localhost) 주입")
	assert.True(t, stsHasContainer(with, "mongot"), "annotation 시 mongot sidecar 주입")
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

func stsHasContainer(sts *appsv1.StatefulSet, name string) bool {
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	return false
}
