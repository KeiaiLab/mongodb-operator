/*
Copyright 2026 Keiailab.
*/

// Isolated unit test — evaluateShardedConditions pure function.
// envtest 의존성 0 (k8s client / reconcile loop 무관). C37 회귀 가드 영역.

package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestEvaluateShardedConditions_Baseline(t *testing.T) {
	t.Parallel()
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		Status: mongodbv1alpha1.MongoDBShardedStatus{
			ConfigServer: mongodbv1alpha1.ComponentStatus{Ready: 3, Total: 3},
			Shards: []mongodbv1alpha1.ShardStatus{
				{Name: "shard-0", Ready: 3, Total: 3},
				{Name: "shard-1", Ready: 3, Total: 3},
			},
			Mongos: mongodbv1alpha1.ComponentStatus{Ready: 2, Total: 2},
		},
	}
	mdbsh.Spec.Shards.Count = 2
	mdbsh.Generation = 1

	conds := evaluateShardedConditions(mdbsh, true)

	gotTypes := map[string]metav1.ConditionStatus{}
	for _, c := range conds {
		gotTypes[c.Type] = c.Status
	}

	wantTrue := []string{"Ready", "ConfigServerReady", "ShardsReady", "MongosReady"}
	for _, w := range wantTrue {
		if got, ok := gotTypes[w]; !ok || got != metav1.ConditionTrue {
			t.Errorf("condition %s: got %v, want True", w, got)
		}
	}
	if got, ok := gotTypes["Progressing"]; !ok || got != metav1.ConditionFalse {
		t.Errorf("Progressing: got %v, want False (cluster ready 시)", got)
	}
}

func TestEvaluateShardedConditions_Initializing(t *testing.T) {
	t.Parallel()
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		Status: mongodbv1alpha1.MongoDBShardedStatus{
			ConfigServer: mongodbv1alpha1.ComponentStatus{Ready: 1, Total: 3},
		},
	}
	mdbsh.Spec.Shards.Count = 2
	mdbsh.Generation = 1

	conds := evaluateShardedConditions(mdbsh, false)
	for _, c := range conds {
		if c.Type == "Ready" && c.Status != metav1.ConditionFalse {
			t.Errorf("Ready: want False during init, got %v", c.Status)
		}
		if c.Type == "Progressing" && c.Status != metav1.ConditionTrue {
			t.Errorf("Progressing: want True during init, got %v", c.Status)
		}
		if c.Type == "ConfigServerReady" && c.Status != metav1.ConditionFalse {
			t.Errorf("ConfigServerReady: want False (1/3), got %v", c.Status)
		}
	}
}

func TestEvaluateShardedConditions_TLSBackupConditional(t *testing.T) {
	t.Parallel()
	t.Run("TLS+Backup 비활성 → conditions 6건", func(t *testing.T) {
		t.Parallel()
		mdbsh := &mongodbv1alpha1.MongoDBSharded{}
		mdbsh.Spec.Shards.Count = 1
		conds := evaluateShardedConditions(mdbsh, false)
		// Ready / Progressing / ConfigServerReady / ShardsReady / MongosReady = 5
		// (TLS / Backup 부재).
		if len(conds) != 5 {
			t.Errorf("len(conds) = %d, want 5 (no TLS/Backup)", len(conds))
		}
	})
	t.Run("TLS 활성 → TLSReady 추가 (6건)", func(t *testing.T) {
		t.Parallel()
		mdbsh := &mongodbv1alpha1.MongoDBSharded{}
		mdbsh.Spec.Shards.Count = 1
		mdbsh.Spec.TLS = &mongodbv1alpha1.TLSSpec{Enabled: true}
		conds := evaluateShardedConditions(mdbsh, false)
		var hasTLS bool
		for _, c := range conds {
			if c.Type == "TLSReady" {
				hasTLS = true
			}
		}
		if !hasTLS {
			t.Error("TLSReady condition 누락")
		}
	})
	t.Run("Backup 활성 → BackupReady 추가 + message 검증", func(t *testing.T) {
		t.Parallel()
		mdbsh := &mongodbv1alpha1.MongoDBSharded{}
		mdbsh.Spec.Shards.Count = 1
		mdbsh.Spec.Backup = &mongodbv1alpha1.BackupSpec{
			Enabled:  true,
			Schedule: "0 2 * * *",
			Storage:  mongodbv1alpha1.BackupStorageSpec{Type: "s3"},
		}
		conds := evaluateShardedConditions(mdbsh, false)
		var bk *metav1.Condition
		for i, c := range conds {
			if c.Type == "BackupReady" {
				bk = &conds[i]
			}
		}
		if bk == nil {
			t.Fatal("BackupReady 누락")
		}
		// message 에 schedule + type 포함 (informative).
		if !strings.Contains(bk.Message, "0 2 * * *") || !strings.Contains(bk.Message, "s3") {
			t.Errorf("BackupReady message 정보 부족: %q", bk.Message)
		}
	})
	t.Run("TLS+Backup 동시 활성 → 7건 (Ready/Progressing 등 5 + TLS + Backup)", func(t *testing.T) {
		t.Parallel()
		mdbsh := &mongodbv1alpha1.MongoDBSharded{}
		mdbsh.Spec.Shards.Count = 1
		mdbsh.Spec.TLS = &mongodbv1alpha1.TLSSpec{Enabled: true}
		mdbsh.Spec.Backup = &mongodbv1alpha1.BackupSpec{Enabled: true, Schedule: "* * * * *", Storage: mongodbv1alpha1.BackupStorageSpec{Type: "s3"}}
		conds := evaluateShardedConditions(mdbsh, true)
		if len(conds) != 7 {
			t.Errorf("len(conds) = %d, want 7 (5 baseline + TLS + Backup)", len(conds))
		}
	})
}

func TestEvaluateShardedConditions_PartialShard(t *testing.T) {
	t.Parallel()
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		Status: mongodbv1alpha1.MongoDBShardedStatus{
			Shards: []mongodbv1alpha1.ShardStatus{
				{Name: "shard-0", Ready: 3, Total: 3}, // ready
				{Name: "shard-1", Ready: 1, Total: 3}, // partial
			},
		},
	}
	mdbsh.Spec.Shards.Count = 2

	conds := evaluateShardedConditions(mdbsh, false)
	for _, c := range conds {
		if c.Type == "ShardsReady" && c.Status != metav1.ConditionFalse {
			t.Errorf("ShardsReady: 1 partial shard 시 False 의무, got %v", c.Status)
		}
	}
}
