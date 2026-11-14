/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

const crdBasesGlob = "../../config/crd/bases/*.yaml"

// missingProperties 는 served 에 있고 storage 에 없는 필드 경로를 모은다.
// e.g. ".spec.shards.setParameters"
func missingProperties(served, storage apiextensionsv1.JSONSchemaProps, path string) []string {
	var missing []string
	for name, child := range served.Properties {
		stored, ok := storage.Properties[name]
		if !ok {
			missing = append(missing, path+"."+name)
			continue
		}
		missing = append(missing, missingProperties(child, stored, path+"."+name)...)
	}

	if served.Items != nil && served.Items.Schema != nil && storage.Items != nil && storage.Items.Schema != nil {
		missing = append(missing, missingProperties(*served.Items.Schema, *storage.Items.Schema, path+"[]")...)
	}
	return missing
}

func storageSchema(crd apiextensionsv1.CustomResourceDefinition) apiextensionsv1.JSONSchemaProps {
	for _, version := range crd.Spec.Versions {
		if version.Storage {
			return *version.Schema.OpenAPIV3Schema
		}
	}
	return apiextensionsv1.JSONSchemaProps{}
}

// 변환 전략이 None 이라 API 서버는 저장 버전 스키마에 없는 필드를 저장할 때 잘라낸다.
// served 버전의 모든 필드는 저장 버전에도 있어야 한다.
func TestStorageVersionKeepsServedFields(t *testing.T) {
	files, err := filepath.Glob(crdBasesGlob)
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs at %s: %v", crdBasesGlob, err)
	}

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s: %v", file, err)
		}

		storage := storageSchema(crd)
		for _, version := range crd.Spec.Versions {
			if version.Storage {
				continue
			}
			missing := missingProperties(*version.Schema.OpenAPIV3Schema, storage, "")
			slices.Sort(missing)
			if len(missing) > 0 {
				t.Errorf("%s: fields in %s are pruned at storage: %v", crd.Name, version.Name, missing)
			}
		}
	}
}
