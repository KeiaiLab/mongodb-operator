/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongot_sharded_test.go — PR4: Sharded search(shard 별 mongot sidecar) builder 회귀 가드.
// annotation 부재 시 무롤링(byte-identical), 존재 시 모든 shard STS 에 sidecar 주입 + config server
// 제외 + shard mongod 27018 sync 를 결정론 검증.
package resources

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const shardedMongotImage = "mongodb/mongodb-community-search:latest"

// mongotAnnotations — search 활성 annotation 3종(search controller 가 설정하는 값과 동일).
func mongotAnnotations() map[string]string {
	return map[string]string{
		MongotSidecarImageAnnotation: shardedMongotImage,
		MongotSyncSecretAnnotation:   "test-sh-search-sync",
		MongotTLSModeAnnotation:      "disabled",
	}
}

// mongosArgs — mongos Deployment 의 mongos 컨테이너 args.
func mongosArgs(t *testing.T, sh *mongodbv1alpha1.MongoDBSharded) []string {
	t.Helper()
	dep := BuildMongosDeployment(sh)
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == "mongos" {
			return c.Args
		}
	}
	t.Fatal("mongos 컨테이너 부재")
	return nil
}

// TestShardedMongot_NoRoll — annotation 부재 → shard/configsvr STS 에 mongot 없음(무롤링 baseline).
func TestShardedMongot_NoRoll(t *testing.T) {
	sh := newTestSharded()
	shard := BuildShardStatefulSet(sh, 0)
	assert.False(t, stsHasContainer(shard, "mongot"), "annotation 부재 시 shard 에 mongot sidecar 없어야(무롤링)")
	assert.False(t, stsHasMongotHost(shard), "annotation 부재 시 shard 에 mongotHost 없어야(무롤링)")

	cfg := BuildConfigServerStatefulSet(sh)
	assert.False(t, stsHasContainer(cfg, "mongot"), "config server 는 mongot 미배포")
}

// TestShardedMongot_InjectsPerShard — annotation 존재 → 모든 shard STS 에 mongot sidecar +
// mongotHost(localhost) 주입. config server 는 여전히 제외.
func TestShardedMongot_InjectsPerShard(t *testing.T) {
	sh := newTestSharded()
	sh.Annotations = map[string]string{
		MongotSidecarImageAnnotation: shardedMongotImage,
		MongotSyncSecretAnnotation:   "test-sh-search-sync",
		MongotTLSModeAnnotation:      "disabled",
	}

	// 모든 shard(Count=2)에 주입.
	for i := int32(0); i < sh.Spec.Shards.Count; i++ {
		shard := BuildShardStatefulSet(sh, i)
		assert.True(t, stsHasContainer(shard, "mongot"), "shard-%d 에 mongot sidecar 주입돼야", i)
		assert.True(t, stsHasMongotHost(shard), "shard-%d 에 mongotHost setParameter 주입돼야", i)
	}

	// config server 는 mongot 미배포(메타데이터만).
	cfg := BuildConfigServerStatefulSet(sh)
	assert.False(t, stsHasContainer(cfg, "mongot"), "config server 는 mongot 미배포 유지")
}

// TestShardedMongot_MongosNoRoll — annotation 부재 → mongos args 에 --setParameter 0개(무롤링 가드).
// mongos 는 라이브 운영 컨트롤면이라 args 1 byte 변화도 롤링을 유발한다. search 비활성 클러스터에서
// 본 변경이 mongos template 을 건드리지 않음을 결정론 고정.
func TestShardedMongot_MongosNoRoll(t *testing.T) {
	sh := newTestSharded()

	args := mongosArgs(t, sh)
	for _, a := range args {
		assert.NotEqual(t, setParameterFlag, a, "annotation 부재 시 mongos 에 --setParameter 없어야(무롤링)")
		assert.NotContains(t, a, "mongotHost=", "annotation 부재 시 mongos 에 mongotHost 없어야(무롤링)")
	}

	// baseline args 가 annotation 도입 전과 byte-identical 인지(요소 단위 동등) 재확인.
	assert.Equal(t, args, mongosArgs(t, newTestSharded()), "annotation 부재 mongos args 는 결정론 동일해야")

	// Service 도 생성되지 않는다(opt-in).
	assert.Nil(t, BuildMongotService(sh), "annotation 부재 시 mongot Service 미생성")
	// shard pod template 에 mongot 표식 라벨도 없어야(무롤링).
	shard := BuildShardStatefulSet(sh, 0)
	assert.NotContains(t, shard.Spec.Template.Labels, MongotPodLabel, "annotation 부재 시 mongot 표식 라벨 없어야")
}

// TestShardedMongot_MongosSetParameterInjected — annotation 존재 → mongos args 에 mongot Service 를
// 가리키는 mongotHost + searchIndexManagementHostAndPort 주입(SearchNotEnabled 근본 원인 해소).
func TestShardedMongot_MongosSetParameterInjected(t *testing.T) {
	sh := newTestSharded()
	sh.Annotations = mongotAnnotations()

	args := mongosArgs(t, sh)
	joined := strings.Join(args, " ")
	endpoint := fmt.Sprintf("test-sh-mongot.%s.svc.cluster.local:27028", sh.Namespace)

	assert.Contains(t, joined, "mongotHost="+endpoint, "mongos 가 mongot Service 를 mongotHost 로 가리켜야")
	assert.Contains(t, joined, "searchIndexManagementHostAndPort="+endpoint,
		"mongos 가 mongot Service 를 인덱스 관리 엔드포인트로 가리켜야")
	assert.Contains(t, joined, "searchTLSMode=disabled")
	assert.Contains(t, joined, "useGrpcForSearch=true")
	// localhost 오배선 금지 — mongos pod 에는 mongot 사이드카가 없다.
	assert.NotContains(t, joined, "localhost:27028", "mongos 는 localhost mongot 을 가리키면 안 됨(사이드카 부재)")
}

// TestShardedMongot_ShardInjectionUnchanged — mongos 배선 추가 후에도 shard 주입은 localhost:27028 불변.
func TestShardedMongot_ShardInjectionUnchanged(t *testing.T) {
	sh := newTestSharded()
	sh.Annotations = mongotAnnotations()

	for i := int32(0); i < sh.Spec.Shards.Count; i++ {
		shard := BuildShardStatefulSet(sh, i)
		joined := strings.Join(shard.Spec.Template.Spec.Containers[0].Args, " ")
		assert.Contains(t, joined, "mongotHost=localhost:27028", "shard-%d 는 localhost mongot 유지", i)
		assert.Contains(t, joined, "searchIndexManagementHostAndPort=localhost:27028", "shard-%d", i)
		assert.NotContains(t, joined, "svc.cluster.local", "shard 는 Service 경유 아님(사이드카 직결)")
	}
}

// TestShardedMongot_Service — mongot Service 가 전 shard 의 mongot pod 를 selector 로 묶고 27028(gRPC,
// named targetPort)을 노출하는지. selector 는 shard STS pod template 라벨과 실제로 매치해야 한다
// (component=shard-N 은 shard 마다 달라 공통 표식 라벨 MongotPodLabel 로 묶는다).
func TestShardedMongot_Service(t *testing.T) {
	sh := newTestSharded()
	sh.Annotations = mongotAnnotations()

	svc := BuildMongotService(sh)
	require.NotNil(t, svc, "annotation 존재 시 mongot Service 생성돼야")
	assert.Equal(t, "test-sh-mongot", svc.Name)
	assert.Equal(t, sh.Namespace, svc.Namespace)
	require.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(27028), svc.Spec.Ports[0].Port)
	assert.Equal(t, "mongot-grpc", svc.Spec.Ports[0].TargetPort.StrVal)

	// selector 가 *모든* shard pod template 라벨의 부분집합인지(= 전 shard mongot 을 엔드포인트로 묶음).
	for i := int32(0); i < sh.Spec.Shards.Count; i++ {
		podLabels := BuildShardStatefulSet(sh, i).Spec.Template.Labels
		for k, v := range svc.Spec.Selector {
			assert.Equal(t, v, podLabels[k], "shard-%d pod 가 mongot Service selector[%s] 를 만족해야", i, k)
		}
	}

	// mongos / config server pod 는 selector 에 걸리지 않아야(mongot 미보유 — 엔드포인트 오염 금지).
	mongosLabels := BuildMongosDeployment(sh).Spec.Template.Labels
	assert.NotEqual(t, "true", mongosLabels[MongotPodLabel], "mongos pod 는 mongot 표식 라벨 없어야")
	cfgLabels := BuildConfigServerStatefulSet(sh).Spec.Template.Labels
	assert.NotEqual(t, "true", cfgLabels[MongotPodLabel], "config server pod 는 mongot 표식 라벨 없어야")

	// STS Selector(immutable)는 오염되지 않아야 — 표식 라벨은 pod template 에만.
	shard := BuildShardStatefulSet(sh, 0)
	assert.NotContains(t, shard.Spec.Selector.MatchLabels, MongotPodLabel,
		"STS Selector 는 immutable — mongot 표식 라벨이 들어가면 기존 STS apply 실패")
}

// TestShardedMongot_SidecarHasInitAndVolume — 주입된 sidecar 가 init(password 0400) + data PVC subPath
// 마운트를 갖는지(RS 와 동일 구조).
func TestShardedMongot_SidecarHasInitAndVolume(t *testing.T) {
	sh := newTestSharded()
	sh.Annotations = map[string]string{
		MongotSidecarImageAnnotation: shardedMongotImage,
		MongotSyncSecretAnnotation:   "test-sh-search-sync",
		MongotTLSModeAnnotation:      "disabled",
	}
	shard := BuildShardStatefulSet(sh, 0)

	// init container(password 복사).
	hasInit := false
	for _, ic := range shard.Spec.Template.Spec.InitContainers {
		if ic.Name == "copy-mongot-password" {
			hasInit = true
		}
	}
	assert.True(t, hasInit, "mongot password init container 주입돼야")

	// mongot 컨테이너가 data PVC 를 search-index subPath 로 마운트(노드 디스크 독립).
	hasSubPath := false
	for _, c := range shard.Spec.Template.Spec.Containers {
		if c.Name != "mongot" {
			continue
		}
		for _, vm := range c.VolumeMounts {
			if vm.Name == mongodDataVolume && vm.SubPath == mongotDataSubPath {
				hasSubPath = true
			}
		}
	}
	assert.True(t, hasSubPath, "mongot 이 data PVC 의 search-index subPath 마운트해야(VCT 불변 보존)")
}
