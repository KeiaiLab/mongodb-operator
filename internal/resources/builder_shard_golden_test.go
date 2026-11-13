/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Shard StatefulSet golden — 오퍼레이터 판 올림이 기존 샤드를 롤링 재시작하지
// 않음을 바이트 단위로 잠근다. golden 은 spec.shards.setParameters 도입 전
// 코드(origin/main 1285a49)에서 기록했다. 갱신: UPDATE_GOLDEN=1 go test.

package resources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const shardGoldenPath = "testdata/shard-statefulset.golden.json"

func goldenShardSharded() *mongodbv1alpha1.MongoDBSharded {
	sh := shardedWithAuth()
	sh.Spec.TLS = &mongodbv1alpha1.TLSSpec{Enabled: true, Mode: "requireTLS"}
	return sh
}

func shardStatefulSetJSON(t *testing.T, sh *mongodbv1alpha1.MongoDBSharded) []byte {
	t.Helper()
	out, err := json.MarshalIndent(BuildShardStatefulSet(sh, 0), "", "  ")
	require.NoError(t, err)
	return append(out, '\n')
}

// TestShardStatefulSet_Golden — 칸 없음 = 기록 당시 렌더와 바이트 동일.
func TestShardStatefulSet_Golden(t *testing.T) {
	got := shardStatefulSetJSON(t, goldenShardSharded())

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(shardGoldenPath), 0o750))
		require.NoError(t, os.WriteFile(shardGoldenPath, got, 0o600))
	}

	want, err := os.ReadFile(shardGoldenPath)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// TestShardStatefulSet_EmptySetParametersGolden — 빈 map 도 칸 없음과 바이트 동일.
func TestShardStatefulSet_EmptySetParametersGolden(t *testing.T) {
	sh := goldenShardSharded()
	sh.Spec.Shards.SetParameters = map[string]mongodbv1alpha1.SetParameterValue{}

	want, err := os.ReadFile(shardGoldenPath)
	require.NoError(t, err)
	require.Equal(t, string(want), string(shardStatefulSetJSON(t, sh)))
}

// TestShardStatefulSet_SetParameters — 키 정렬 순서로 --setParameter k=v 를 덧붙인다.
func TestShardStatefulSet_SetParameters(t *testing.T) {
	base := BuildShardStatefulSet(goldenShardSharded(), 0).Spec.Template.Spec.Containers[0].Args

	sh := goldenShardSharded()
	sh.Spec.Shards.SetParameters = map[string]mongodbv1alpha1.SetParameterValue{
		"periodicNoopIntervalSecs":        "1",
		"diagnosticDataCollectionEnabled": "false",
	}
	got := BuildShardStatefulSet(sh, 0).Spec.Template.Spec.Containers[0].Args

	want := append(append([]string{}, base...),
		"--setParameter", "diagnosticDataCollectionEnabled=false",
		"--setParameter", "periodicNoopIntervalSecs=1",
	)
	require.Equal(t, want, got)
}

// TestShardSetParameters_OtherComponentsUntouched — cfg · mongos 인자는 그대로.
func TestShardSetParameters_OtherComponentsUntouched(t *testing.T) {
	sh := goldenShardSharded()
	cfgBefore := BuildConfigServerStatefulSet(sh).Spec.Template.Spec.Containers[0].Args
	mongosBefore := BuildMongosDeployment(sh).Spec.Template.Spec.Containers[0].Args

	sh.Spec.Shards.SetParameters = map[string]mongodbv1alpha1.SetParameterValue{"periodicNoopIntervalSecs": "1"}

	require.Equal(t, cfgBefore, BuildConfigServerStatefulSet(sh).Spec.Template.Spec.Containers[0].Args)
	require.Equal(t, mongosBefore, BuildMongosDeployment(sh).Spec.Template.Spec.Containers[0].Args)
}
