/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbsearchindex_webhook.go — MongoDBSearchIndex validating webhook(v1beta1).
// DefinitionJSON 유효성 + type-별 필수 필드 + 불변 필드(Database/Collection/IndexName/Type) 검증.
package v1beta1

import (
	"context"
	"encoding/json"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

// SetupMongoDBSearchIndexWebhookWithManager registers the validating webhook.
func SetupMongoDBSearchIndexWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &mongodbv1beta1.MongoDBSearchIndex{}).
		WithValidator(&MongoDBSearchIndexCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-mongodb-keiailab-com-v1beta1-mongodbsearchindex,mutating=false,failurePolicy=fail,sideEffects=None,groups=mongodb.keiailab.com,resources=mongodbsearchindices,verbs=create;update,versions=v1beta1,name=vmongodbsearchindex-v1beta1.kb.io,admissionReviewVersions=v1

// MongoDBSearchIndexCustomValidator — search index admission validation.
type MongoDBSearchIndexCustomValidator struct{}

func (v *MongoDBSearchIndexCustomValidator) ValidateCreate(_ context.Context, idx *mongodbv1beta1.MongoDBSearchIndex) (admission.Warnings, error) {
	if errs := validateSearchIndexSpec(idx); len(errs) > 0 {
		return nil, apiError("MongoDBSearchIndex", idx.GetName(), errs)
	}
	return nil, nil
}

func (v *MongoDBSearchIndexCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *mongodbv1beta1.MongoDBSearchIndex) (admission.Warnings, error) {
	errs := validateSearchIndexSpec(newObj)
	// 불변 필드: mongot 은 rename/retarget 불가 — Database/Collection/IndexName/Type 변경은
	// drop+recreate(새 CR)여야 한다. DefinitionJSON 만 갱신 허용.
	if oldObj != nil {
		p := field.NewPath("spec")
		errs = append(errs, validateImmutable(p.Child("database"), oldObj.Spec.Database, newObj.Spec.Database)...)
		errs = append(errs, validateImmutable(p.Child("collection"), oldObj.Spec.Collection, newObj.Spec.Collection)...)
		errs = append(errs, validateImmutable(p.Child("indexName"), oldObj.Spec.IndexName, newObj.Spec.IndexName)...)
		errs = append(errs, validateImmutable(p.Child("type"), oldObj.Spec.Type, newObj.Spec.Type)...)
		errs = append(errs, validateImmutable(p.Child("searchRef", "name"), oldObj.Spec.SearchRef.Name, newObj.Spec.SearchRef.Name)...)
	}
	if len(errs) > 0 {
		return nil, apiError("MongoDBSearchIndex", newObj.GetName(), errs)
	}
	return nil, nil
}

func (v *MongoDBSearchIndexCustomValidator) ValidateDelete(_ context.Context, _ *mongodbv1beta1.MongoDBSearchIndex) (admission.Warnings, error) {
	return nil, nil
}

// validateSearchIndexSpec — DefinitionJSON 파싱 + type-별 필수 필드.
func validateSearchIndexSpec(idx *mongodbv1beta1.MongoDBSearchIndex) field.ErrorList {
	var errs field.ErrorList
	p := field.NewPath("spec")

	var def map[string]any
	if err := json.Unmarshal([]byte(idx.Spec.DefinitionJSON), &def); err != nil {
		errs = append(errs, field.Invalid(p.Child("definitionJSON"), idx.Spec.DefinitionJSON,
			"must be valid JSON: "+err.Error()))
		return errs // 파싱 실패면 type-별 검증 무의미
	}

	switch idx.Spec.Type {
	case "vectorSearch":
		// vectorSearch 는 fields 배열 필수(임베딩 정의).
		if _, ok := def["fields"]; !ok {
			errs = append(errs, field.Invalid(p.Child("definitionJSON"), idx.Spec.DefinitionJSON,
				`type=vectorSearch requires a "fields" array (vector embedding definition)`))
		}
	case "search", "":
		// search(full-text)는 mappings 권장(없으면 dynamic). 강제 아님 — warning 대신 통과.
	}
	return errs
}

// validateImmutable — update 시 불변 필드 변경 거부.
func validateImmutable(path *field.Path, oldVal, newVal string) field.ErrorList {
	if oldVal != newVal {
		return field.ErrorList{field.Invalid(path, newVal,
			"immutable: mongot 인덱스는 rename/retarget 불가 — 변경하려면 CR 삭제 후 재생성")}
	}
	return nil
}
