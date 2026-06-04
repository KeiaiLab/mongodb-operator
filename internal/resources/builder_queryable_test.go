/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// builder_queryable_test.go — BuildQueryableStatefulSet 특성화 테스트 (ROADMAP §3.1.1).
// verification controller 가 의존하는 gating(nil/disabled→nil) + 구조(read-only
// 단일 멤버) 회귀 가드. 데이터 복원 drill 은 후속 (deferred).

package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func queryableTestBackup() *mongodbv1alpha1.MongoDBBackup {
	return &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "data"},
	}
}

func TestBuildQueryableStatefulSet_NilSpecReturnsNil(t *testing.T) {
	// controller 의 `if sts := Build...; sts != nil` gating 보장.
	assert.Nil(t, BuildQueryableStatefulSet(queryableTestBackup(), nil),
		"nil spec → nil (instance 미생성)")
}

func TestBuildQueryableStatefulSet_DisabledReturnsNil(t *testing.T) {
	spec := &mongodbv1alpha1.QueryableBackupSpec{Enabled: false}
	assert.Nil(t, BuildQueryableStatefulSet(queryableTestBackup(), spec),
		"Enabled=false → nil (opt-in 기본)")
}

func TestBuildQueryableStatefulSet_EnabledShape(t *testing.T) {
	spec := &mongodbv1alpha1.QueryableBackupSpec{Enabled: true, TTL: "168h"}
	sts := BuildQueryableStatefulSet(queryableTestBackup(), spec)

	require.NotNil(t, sts, "Enabled=true → StatefulSet 생성")
	assert.Equal(t, "nightly-queryable", sts.Name, "<backup>-queryable 명명")
	assert.Equal(t, "data", sts.Namespace, "backup namespace 상속")

	require.NotNil(t, sts.Spec.Replicas)
	assert.Equal(t, int32(1), *sts.Spec.Replicas, "단일 멤버 read-only instance")

	require.Len(t, sts.Spec.Template.Spec.Containers, 1, "mongod 컨테이너 1개")
	c := sts.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "mongod", c.Name)
	assert.Contains(t, c.Image, "mongo:", "mongo 이미지 (Bitnami 아님 — ADR-0136 정합)")

	require.Len(t, sts.Spec.VolumeClaimTemplates, 1, "data PVC 1개")
}
