/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package v1beta1

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

func idxFixture(typ, defJSON string) *mongodbv1beta1.MongoDBSearchIndex {
	return &mongodbv1beta1.MongoDBSearchIndex{
		ObjectMeta: metav1.ObjectMeta{Name: "i", Namespace: "default"},
		Spec: mongodbv1beta1.MongoDBSearchIndexSpec{
			SearchRef:      corev1.LocalObjectReference{Name: "s"},
			Database:       "db",
			Collection:     "coll",
			IndexName:      "default",
			Type:           typ,
			DefinitionJSON: defJSON,
		},
	}
}

func TestValidateCreate_ValidSearch(t *testing.T) {
	v := &MongoDBSearchIndexCustomValidator{}
	if _, err := v.ValidateCreate(context.Background(), idxFixture("search", `{"mappings":{"dynamic":true}}`)); err != nil {
		t.Errorf("유효한 search 인덱스 거부: %v", err)
	}
}

func TestValidateCreate_ValidVectorSearch(t *testing.T) {
	v := &MongoDBSearchIndexCustomValidator{}
	def := `{"fields":[{"type":"vector","path":"emb","numDimensions":1536,"similarity":"cosine"}]}`
	if _, err := v.ValidateCreate(context.Background(), idxFixture("vectorSearch", def)); err != nil {
		t.Errorf("유효한 vectorSearch 인덱스 거부: %v", err)
	}
}

func TestValidateCreate_InvalidJSON(t *testing.T) {
	v := &MongoDBSearchIndexCustomValidator{}
	if _, err := v.ValidateCreate(context.Background(), idxFixture("search", `{not json`)); err == nil {
		t.Error("invalid JSON 통과 — 거부 기대")
	}
}

func TestValidateCreate_VectorSearchMissingFields(t *testing.T) {
	v := &MongoDBSearchIndexCustomValidator{}
	// vectorSearch 인데 fields 없음 → 거부.
	if _, err := v.ValidateCreate(context.Background(), idxFixture("vectorSearch", `{"mappings":{"dynamic":true}}`)); err == nil {
		t.Error("vectorSearch fields 누락 통과 — 거부 기대")
	}
}

func TestValidateUpdate_ImmutableFields(t *testing.T) {
	v := &MongoDBSearchIndexCustomValidator{}
	old := idxFixture("search", `{"mappings":{"dynamic":true}}`)

	// DefinitionJSON 만 변경 → 허용.
	newDef := idxFixture("search", `{"mappings":{"dynamic":false}}`)
	if _, err := v.ValidateUpdate(context.Background(), old, newDef); err != nil {
		t.Errorf("definitionJSON 변경은 허용돼야: %v", err)
	}

	// Collection 변경(불변) → 거부.
	newColl := idxFixture("search", `{"mappings":{"dynamic":true}}`)
	newColl.Spec.Collection = "other"
	if _, err := v.ValidateUpdate(context.Background(), old, newColl); err == nil {
		t.Error("collection 변경 통과 — 불변 위반 거부 기대")
	}

	// IndexName 변경(불변) → 거부.
	newName := idxFixture("search", `{"mappings":{"dynamic":true}}`)
	newName.Spec.IndexName = "renamed"
	if _, err := v.ValidateUpdate(context.Background(), old, newName); err == nil {
		t.Error("indexName 변경 통과 — 불변 위반 거부 기대")
	}

	// Type 변경(불변) → 거부.
	newType := idxFixture("vectorSearch", `{"fields":[{"type":"vector","path":"e"}]}`)
	if _, err := v.ValidateUpdate(context.Background(), old, newType); err == nil {
		t.Error("type 변경 통과 — 불변 위반 거부 기대")
	}
}
