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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// gvOf는 clientgoscheme로 등록된 modern runtime.Scheme에서 obj의 첫 GroupVersionKind를
// 해석해 "<group>/<version>" 문자열과 Kind를 돌려준다.
//
// 왜 scheme.ObjectKinds를 쓰는가 — 빌더가 반환하는 Go 타입(예: autoscalingv2.
// HorizontalPodAutoscaler)이 *어떤 apiVersion 으로 직렬화되는가*는 컴파일 타임 import
// 경로가 결정한다. 누군가 빌더를 deprecated 패키지(autoscaling/v2beta1, policy/v1beta1)로
// 되돌리면 ObjectKinds가 그 deprecated GV를 반환(또는 modern scheme 미등록 시 에러)하므로,
// 이 helper의 결과 비교만으로 회귀가 잡힌다.
func gvOf(t *testing.T, sch *runtime.Scheme, obj runtime.Object) (string, string) {
	t.Helper()
	gvks, _, err := sch.ObjectKinds(obj)
	require.NoError(t, err)
	require.NotEmpty(t, gvks)
	return gvks[0].GroupVersion().String(), gvks[0].Kind
}

// TestBuildersUseNonDeprecatedAPIVersions는 operator가 빌드하는 HPA/PDB 객체가
// *deprecated apiVersion* 으로 회귀하지 않도록 modern GV를 핀(pin)한다.
//
// 배경 — roadmap Phase 5.6(deprecation cleanup)의 *테스트 가능한 표현*. manifest/RBAC는
// 이미 clean하므로, 실제 회귀가 일어날 수 있는 지점은 Go 빌더의 import 경로뿐이다.
// HPA: autoscaling/v2(NOT v2beta1/v2beta2) + ScaleTargetRef.APIVersion="apps/v1".
// PDB: policy/v1(NOT v1beta1).
func TestBuildersUseNonDeprecatedAPIVersions(t *testing.T) {
	sch := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(sch))

	// --- HPA: 3 빌더 테이블 ---
	// mongos는 단일 가드(AutoScaling.Enabled)만, RS/cfg는 이중 가드(Enabled +
	// ScalePolicy.Deliberate)를 통과시켜야 non-nil HPA가 나온다(ADR-0008/0009).
	hpaCases := []struct {
		name  string
		build func() *autoscalingv2.HorizontalPodAutoscaler
	}{
		{
			name: "mongos",
			build: func() *autoscalingv2.HorizontalPodAutoscaler {
				mdbsh := &mongodbv1alpha1.MongoDBSharded{
					ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
					Spec: mongodbv1alpha1.MongoDBShardedSpec{
						Mongos: mongodbv1alpha1.MongosSpec{
							Replicas: 2,
							AutoScaling: &mongodbv1alpha1.AutoScalingSpec{
								Enabled: true, MinReplicas: 2, MaxReplicas: 5,
							},
						},
					},
				}
				return BuildMongosHPA(mdbsh)
			},
		},
		{
			name: "replicaset",
			build: func() *autoscalingv2.HorizontalPodAutoscaler {
				mdb := &mongodbv1alpha1.MongoDB{
					ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
					Spec: mongodbv1alpha1.MongoDBSpec{
						Members:     3,
						AutoScaling: &mongodbv1alpha1.AutoScalingSpec{Enabled: true, MinReplicas: 1, MaxReplicas: 4},
						ScalePolicy: &mongodbv1alpha1.ScalePolicy{Deliberate: true},
					},
				}
				return BuildReplicaSetHPA(mdb)
			},
		},
		{
			name: "configserver",
			build: func() *autoscalingv2.HorizontalPodAutoscaler {
				mdbsh := &mongodbv1alpha1.MongoDBSharded{
					ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
					Spec: mongodbv1alpha1.MongoDBShardedSpec{
						ConfigServer: mongodbv1alpha1.ConfigServerSpec{
							Members:     3,
							AutoScaling: &mongodbv1alpha1.AutoScalingSpec{Enabled: true, MinReplicas: 1, MaxReplicas: 4},
							ScalePolicy: &mongodbv1alpha1.ScalePolicy{Deliberate: true},
						},
					},
				}
				return BuildConfigServerHPA(mdbsh)
			},
		},
	}

	for _, tc := range hpaCases {
		t.Run("hpa/"+tc.name, func(t *testing.T) {
			hpa := tc.build()
			require.NotNil(t, hpa, "빌더가 nil HPA 반환 — 가드 입력이 잘못됨")

			gv, kind := gvOf(t, sch, hpa)
			assert.Equal(t, "autoscaling/v2", gv, "HPA는 modern autoscaling/v2 여야 함")
			assert.NotContains(t, gv, "beta", "HPA GV에 beta(v2beta1/v2beta2) 금지")
			assert.Equal(t, "HorizontalPodAutoscaler", kind)
			// ScaleTargetRef.APIVersion은 deprecated extensions/v1beta1 아니라 apps/v1.
			assert.Equal(t, "apps/v1", hpa.Spec.ScaleTargetRef.APIVersion,
				"ScaleTargetRef.APIVersion은 apps/v1 여야 함")
		})
	}

	// --- PDB: 단일 + sharded(4개) ---
	t.Run("pdb/mongodb", func(t *testing.T) {
		mdb := &mongodbv1alpha1.MongoDB{
			ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBSpec{
				Members:             3,
				PodDisruptionBudget: &mongodbv1alpha1.PodDisruptionBudgetSpec{Enabled: true},
			},
		}
		pdb := BuildMongoDBPDB(mdb)
		require.NotNil(t, pdb)

		gv, kind := gvOf(t, sch, pdb)
		assert.Equal(t, "policy/v1", gv, "PDB는 modern policy/v1 여야 함")
		assert.NotContains(t, gv, "beta", "PDB GV에 beta(v1beta1) 금지")
		assert.Equal(t, "PodDisruptionBudget", kind)
	})

	t.Run("pdb/sharded", func(t *testing.T) {
		mdbsh := &mongodbv1alpha1.MongoDBSharded{
			ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBShardedSpec{
				ConfigServer:        mongodbv1alpha1.ConfigServerSpec{Members: 3},
				Shards:              mongodbv1alpha1.ShardSpec{Count: 2, MembersPerShard: 3},
				Mongos:              mongodbv1alpha1.MongosSpec{Replicas: 2},
				PodDisruptionBudget: &mongodbv1alpha1.PodDisruptionBudgetSpec{Enabled: true},
			},
		}
		pdbs := BuildShardedPDBs(mdbsh)
		// cfg + 2 shards + mongos = 4
		require.Len(t, pdbs, 4)

		for i, pdb := range pdbs {
			require.NotNilf(t, pdb, "PDB[%d] nil", i)
			gv, kind := gvOf(t, sch, pdb)
			assert.Equalf(t, "policy/v1", gv, "PDB[%d](%s)는 policy/v1 여야 함", i, pdb.Name)
			assert.NotContainsf(t, gv, "beta", "PDB[%d](%s) GV에 beta 금지", i, pdb.Name)
			assert.Equal(t, "PodDisruptionBudget", kind)
		}
	})
}
