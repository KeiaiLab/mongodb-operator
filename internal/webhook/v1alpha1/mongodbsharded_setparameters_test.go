/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/validation/field"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// TestValidateShardSetParameters — 키는 mongod 파라미터 이름 꼴, 값은 비지 않음.
func TestValidateShardSetParameters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		params  map[string]mongodbv1alpha1.SetParameterValue
		wantErr int
	}{
		{"nil", nil, 0},
		{"empty map", map[string]mongodbv1alpha1.SetParameterValue{}, 0},
		{"valid", map[string]mongodbv1alpha1.SetParameterValue{"periodicNoopIntervalSecs": "1", "a.b_c": "x y"}, 0},
		{"empty key", map[string]mongodbv1alpha1.SetParameterValue{"": "1"}, 1},
		{"space in key", map[string]mongodbv1alpha1.SetParameterValue{"periodic Noop": "1"}, 1},
		{"tab in key", map[string]mongodbv1alpha1.SetParameterValue{"periodic\tNoop": "1"}, 1},
		{"equals in key", map[string]mongodbv1alpha1.SetParameterValue{"a=b": "1"}, 1},
		{"leading digit", map[string]mongodbv1alpha1.SetParameterValue{"1a": "1"}, 1},
		{"empty value", map[string]mongodbv1alpha1.SetParameterValue{"periodicNoopIntervalSecs": ""}, 1},
		{"newline in value", map[string]mongodbv1alpha1.SetParameterValue{"periodicNoopIntervalSecs": "1\n--auth"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := validateShardSetParameters(field.NewPath("spec", "shards", "setParameters"), tc.params)
			if len(errs) != tc.wantErr {
				t.Fatalf("want %d errors, got %v", tc.wantErr, errs)
			}
		})
	}
}

// TestValidateMongoDBShardedSpec_SetParameters — 조합 검증이 setParameters 를 부른다.
func TestValidateMongoDBShardedSpec_SetParameters(t *testing.T) {
	t.Parallel()
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Shards.SetParameters = map[string]mongodbv1alpha1.SetParameterValue{"bad key": "1"}

	errs := validateMongoDBShardedSpec(m)
	for _, e := range errs {
		if e.Field == "spec.shards.setParameters[bad key]" {
			return
		}
	}
	t.Fatalf("want spec.shards.setParameters[bad key] error, got %v", errs)
}
