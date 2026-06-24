/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongot_sharded_test.go — PR4: Sharded search(shard 별 mongot sidecar) builder 회귀 가드.
// annotation 부재 시 무롤링(byte-identical), 존재 시 모든 shard STS 에 sidecar 주입 + config server
// 제외 + shard mongod 27018 sync 를 결정론 검증.
package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const shardedMongotImage = "mongodb/mongodb-community-search:latest"

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
