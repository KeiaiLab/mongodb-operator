/*
Copyright 2026 Keiailab.
*/

// Isolated unit test — buildConditions (RS controller). C37 cross-cut
// 일관성: sharded 의 evaluateShardedConditions 와 동등한 isolated test
// 가드. envtest 의존성 0.
//
// 비고: buildConditions 는 *MongoDBReconciler receiver method 지만 receiver
// 를 사용 안 함 — nil receiver 로 isolated 호출 가능.

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestBuildConditions_RS_FullyReady(t *testing.T) {
	t.Parallel()
	mdb := &mongodbv1alpha1.MongoDB{
		Spec: mongodbv1alpha1.MongoDBSpec{Members: 3},
		Status: mongodbv1alpha1.MongoDBStatus{
			ReadyMembers:          3,
			ReplicaSetInitialized: true,
			AdminUserCreated:      true,
		},
	}
	mdb.Generation = 1

	r := (*MongoDBReconciler)(nil)
	conds := r.buildConditions(mdb)

	gotTypes := map[string]metav1.ConditionStatus{}
	for _, c := range conds {
		gotTypes[c.Type] = c.Status
	}
	if gotTypes["Ready"] != metav1.ConditionTrue {
		t.Errorf("Ready: want True, got %v", gotTypes["Ready"])
	}
	if gotTypes["ReplicaSetInitialized"] != metav1.ConditionTrue {
		t.Errorf("ReplicaSetInitialized: want True, got %v", gotTypes["ReplicaSetInitialized"])
	}
	if gotTypes["AuthenticationReady"] != metav1.ConditionTrue {
		t.Errorf("AuthenticationReady: want True, got %v", gotTypes["AuthenticationReady"])
	}
}

func TestBuildConditions_RS_PartialReadyMembers(t *testing.T) {
	t.Parallel()
	mdb := &mongodbv1alpha1.MongoDB{
		Spec: mongodbv1alpha1.MongoDBSpec{Members: 3},
		Status: mongodbv1alpha1.MongoDBStatus{
			ReadyMembers:          1,
			ReplicaSetInitialized: true,
			AdminUserCreated:      true,
		},
	}
	r := (*MongoDBReconciler)(nil)
	conds := r.buildConditions(mdb)

	for _, c := range conds {
		if c.Type == "Ready" && c.Status != metav1.ConditionFalse {
			t.Errorf("Ready: 1/3 members 시 False 의무, got %v", c.Status)
		}
	}
}

func TestBuildConditions_RS_PreservesUnmanagedTypes(t *testing.T) {
	t.Parallel()
	// PrimaryUnreachable 같은 *unmanaged* type 은 buildConditions 가 보존.
	mdb := &mongodbv1alpha1.MongoDB{
		Spec: mongodbv1alpha1.MongoDBSpec{Members: 3},
		Status: mongodbv1alpha1.MongoDBStatus{
			Conditions: []metav1.Condition{
				{Type: "PrimaryUnreachable", Status: metav1.ConditionTrue, Reason: "NetworkError"},
			},
			ReadyMembers:          3,
			ReplicaSetInitialized: true,
			AdminUserCreated:      true,
		},
	}
	r := (*MongoDBReconciler)(nil)
	conds := r.buildConditions(mdb)

	var hasPrimaryUnreachable bool
	for _, c := range conds {
		if c.Type == "PrimaryUnreachable" {
			hasPrimaryUnreachable = true
		}
	}
	if !hasPrimaryUnreachable {
		t.Error("PrimaryUnreachable (unmanaged type) 보존 안 됨")
	}
}
