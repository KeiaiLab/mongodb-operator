/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_uploader_test.go — F03 (cycle 1) PITR uploader controller 회귀 가드.

package controller

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestOplogUploaderReconciler_IsApplicable(t *testing.T) {
	t.Parallel()
	r := &OplogUploaderReconciler{}
	cases := []struct {
		name string
		spec *mongodbv1alpha1.BackupSpec
		want bool
	}{
		{"nil", nil, false},
		{"backup disabled", &mongodbv1alpha1.BackupSpec{Enabled: false, PITREnabled: true, OplogRetentionHours: 24}, false},
		{"PITR disabled", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: false, OplogRetentionHours: 24}, false},
		{"retention zero", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 0}, false},
		{"retention positive", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 24}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := r.IsApplicable(tc.spec); got != tc.want {
				t.Errorf("IsApplicable(%v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}
