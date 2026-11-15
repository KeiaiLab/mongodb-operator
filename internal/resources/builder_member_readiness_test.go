/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Member-state readiness — spec.shards.readiness / spec.configServer.readiness
// 가 shard · cfg mongod readiness 를 멤버 상태(+ router shard 의 search 색인)로
// 바꾸는지, Ping · 비움은 렌더가 바이트 동일한지 잠근다.

package resources

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func mongodReadinessCmd(t *testing.T, sts *appsv1.StatefulSet) string {
	t.Helper()
	probe := sts.Spec.Template.Spec.Containers[0].ReadinessProbe
	require.NotNil(t, probe)
	require.NotNil(t, probe.Exec)
	return strings.Join(probe.Exec.Command, " ")
}

func withMongot(sh *mongodbv1alpha1.MongoDBSharded, routerShard string) *mongodbv1alpha1.MongoDBSharded {
	sh.Annotations = map[string]string{
		MongotSidecarImageAnnotation: "mongodb/mongodb-community-search:0.69.1",
		MongotSyncSecretAnnotation:   "sync",
		MongotRouterShardAnnotation:  routerShard,
	}
	return sh
}

// TestMemberReadiness_PingKeepsGolden — Ping 은 칸 없음과 바이트 동일(롤링 0).
func TestMemberReadiness_PingKeepsGolden(t *testing.T) {
	sh := goldenShardSharded()
	sh.Spec.Shards.Readiness = mongodbv1alpha1.ReadinessPing

	require.Equal(t, string(shardStatefulSetJSON(t, goldenShardSharded())), string(shardStatefulSetJSON(t, sh)))

	cfg := goldenShardSharded()
	cfg.Spec.ConfigServer.Readiness = mongodbv1alpha1.ReadinessPing
	require.Equal(t, BuildConfigServerStatefulSet(goldenShardSharded()), BuildConfigServerStatefulSet(cfg))
}

// TestMemberReadiness_PingIgnoresMongot — Ping 은 router shard 라도 ping 만 한다.
func TestMemberReadiness_PingIgnoresMongot(t *testing.T) {
	sh := withMongot(shardedWithAuth(), "shard-0")

	require.Equal(t, strings.Join(mongoshPing(27018), " "), mongodReadinessCmd(t, BuildShardStatefulSet(sh, 0)))
}

// TestMemberReadiness_MemberStateShard — PRIMARY · SECONDARY 만 Ready, liveness 는 ping 그대로.
func TestMemberReadiness_MemberStateShard(t *testing.T) {
	sh := shardedWithAuth()
	sh.Spec.Shards.Readiness = mongodbv1alpha1.ReadinessMemberState
	sts := BuildShardStatefulSet(sh, 1)

	cmd := mongodReadinessCmd(t, sts)
	require.Contains(t, cmd, "--port 27018")
	require.Contains(t, cmd, "hello:1")
	require.Contains(t, cmd, "isWritablePrimary||h.secondary")
	require.NotContains(t, cmd, "$listSearchIndexes")

	live := sts.Spec.Template.Spec.Containers[0].LivenessProbe.Exec.Command
	require.Equal(t, mongoshPing(27018), live)
}

// TestMemberReadiness_MemberStateConfigServer — cfg 는 27019, search 검사 없음.
func TestMemberReadiness_MemberStateConfigServer(t *testing.T) {
	sh := withMongot(shardedWithAuth(), "shard-0")
	sh.Spec.ConfigServer.Readiness = mongodbv1alpha1.ReadinessMemberState

	cmd := mongodReadinessCmd(t, BuildConfigServerStatefulSet(sh))
	require.Contains(t, cmd, "--port 27019")
	require.Contains(t, cmd, "isWritablePrimary||h.secondary")
	require.NotContains(t, cmd, "$listSearchIndexes")

	require.Equal(t, strings.Join(mongoshPing(27018), " "), mongodReadinessCmd(t, BuildShardStatefulSet(sh, 0)))
}

// TestMemberReadiness_RouterShardSearch — router shard 만 search 색인 queryable 을 더한다.
func TestMemberReadiness_RouterShardSearch(t *testing.T) {
	sh := withMongot(shardedWithAuth(), "shard-1")
	sh.Spec.Shards.Readiness = mongodbv1alpha1.ReadinessMemberState

	router := mongodReadinessCmd(t, BuildShardStatefulSet(sh, 1))
	require.Contains(t, router, "isWritablePrimary||h.secondary")
	require.Contains(t, router, "$listSearchIndexes")
	require.Contains(t, router, "!i.queryable")
	require.Contains(t, router, "/etc/mongodb-admin/password")

	other := mongodReadinessCmd(t, BuildShardStatefulSet(sh, 0))
	require.NotContains(t, other, "$listSearchIndexes")
}

// TestMemberReadiness_NoSidecarNoSearch — mongot 사이드카가 없으면 router shard 여도 검사 없음.
func TestMemberReadiness_NoSidecarNoSearch(t *testing.T) {
	sh := shardedWithAuth()
	sh.Annotations = map[string]string{MongotRouterShardAnnotation: "shard-0"}
	sh.Spec.Shards.Readiness = mongodbv1alpha1.ReadinessMemberState

	require.NotContains(t, mongodReadinessCmd(t, BuildShardStatefulSet(sh, 0)), "$listSearchIndexes")
}

// TestMemberReadiness_HeadlessPublishesNotReady — initial sync 중(NotReady)인 멤버도
// headless DNS 에 남아야 피어 · operator 가 닿는다(sync deadlock 방지).
func TestMemberReadiness_HeadlessPublishesNotReady(t *testing.T) {
	sh := shardedWithAuth()

	require.True(t, BuildShardService(sh, 0).Spec.PublishNotReadyAddresses)
	require.True(t, BuildConfigServerService(sh).Spec.PublishNotReadyAddresses)
}
