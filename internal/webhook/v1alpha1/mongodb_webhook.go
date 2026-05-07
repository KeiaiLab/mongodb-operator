/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package v1alpha1 — MongoDB / MongoDBSharded admission webhook.
//
// 본 webhook 의 역할 (iteration 45):
//  1. Validation (mutating 부재 — controller 가 default 처리):
//     - spec.version.version 화이트리스트 (iteration 9 의 IsSupportedMongoDBVersion).
//     - spec.members 가 odd >= 3 (replica set quorum).
//     - immutable field (replicaSet 의 ID 등) — 향후 추가.
//
// valkey-operator iteration 8/31 패턴 차용 (검증된).
package v1alpha1

import (
	"context"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	commonswebhook "github.com/keiailab/operator-commons/pkg/webhook"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// SetupMongoDBWebhookWithManager registers the validating webhook for MongoDB.
func SetupMongoDBWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDB{}).
		WithValidator(&MongoDBCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-mongodb-keiailab-com-v1alpha1-mongodb,mutating=false,failurePolicy=fail,sideEffects=None,groups=mongodb.keiailab.com,resources=mongodbs,verbs=create;update,versions=v1alpha1,name=vmongodb-v1alpha1.kb.io,admissionReviewVersions=v1

// MongoDBCustomValidator — admission validation.
type MongoDBCustomValidator struct{}

// ValidateCreate — 신규 CR 검증. runtime.Object signature (non-generic API).
func (v *MongoDBCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	m, ok := obj.(*mongodbv1alpha1.MongoDB)
	if !ok {
		return nil, apiError("MongoDB", "<unknown>", nil)
	}
	if errs := validateMongoDBSpec(m); len(errs) > 0 {
		return nil, apiError("MongoDB", m.GetName(), errs)
	}
	return nil, nil
}

// ValidateUpdate — spec 검증 + immutable 가드.
func (v *MongoDBCustomValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	m, ok := newObj.(*mongodbv1alpha1.MongoDB)
	if !ok {
		return nil, apiError("MongoDB", "<unknown>", nil)
	}
	if errs := validateMongoDBSpec(m); len(errs) > 0 {
		return nil, apiError("MongoDB", m.GetName(), errs)
	}
	return nil, nil
}

// ValidateDelete — 항상 허용.
func (v *MongoDBCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateMongoDBSpec — 조합 검증 (CRD marker 가 표현 못 하는 영역).
func validateMongoDBSpec(m *mongodbv1alpha1.MongoDB) field.ErrorList {
	var errs field.ErrorList
	p := field.NewPath("spec")

	// iteration 9 의 IsSupportedMongoDBVersion (semver-prefix 매칭) + commons 위임.
	// FeatureGates 같은 instance-specific param 없음 — closure 불필요.
	if err := commonswebhook.ValidateWithPredicate(
		p.Child("version", "version"), m.Spec.Version.Version,
		mongodbv1alpha1.IsSupportedMongoDBVersion,
		mongodbv1alpha1.SupportedMongoDBVersions,
	); err != nil {
		errs = append(errs, err)
	}

	// ReplicaSet quorum: members odd >= 3 권장. 1 (single-instance) 도 허용 (dev).
	// 2 / 4 (even) 거부 — split-brain 위험.
	if m.Spec.Members != 0 && m.Spec.Members != 1 && m.Spec.Members%2 == 0 {
		errs = append(errs, field.Invalid(
			p.Child("members"), m.Spec.Members,
			"members must be 1 (single-instance) or odd >= 3 (quorum) — even count risks split-brain",
		))
	}

	// storage.size 하한 1Gi — PVC 가 너무 작으면 mongodb startup 실패 (data dir
	// 부족) 또는 oplog truncation 사고. 운영자가 의도해서 작게 설정한 경우라도
	// 1Gi 미만은 production 부적합 → admission 차단.
	errs = append(errs, validateStorageSize(p.Child("storage", "size"), m.Spec.Storage.Size)...)

	// auth.adminCredentialsSecretRef.name 비어있지 않음. CRD 가 *required* 로
	// 강제하지만 LocalObjectReference 는 struct value 라 빈 객체 ({}) 가 통과.
	// controller 가 secret 없이 startup 시도 → 인증 실패 / pod CrashLoop.
	errs = append(errs, validateAuthSecretRef(p.Child("auth", "adminCredentialsSecretRef", "name"), m.Spec.Auth.AdminCredentialsSecretRef.Name)...)

	return errs
}

// validateAuthSecretRef — auth.adminCredentialsSecretRef.name 비어있지 않음 강제.
// CRD 의 required marker 가 LocalObjectReference struct value 의 빈 객체 ({})
// 를 차단 못 함 — webhook 으로 보강.
func validateAuthSecretRef(path *field.Path, name string) field.ErrorList {
	if name == "" {
		return field.ErrorList{field.Invalid(
			path, name,
			"auth.adminCredentialsSecretRef.name must be non-empty — controller requires admin secret for SCRAM/X509 auth setup",
		)}
	}
	return nil
}

// validateStorageSize — 1Gi 하한 검증. resource.Quantity 의 Cmp 사용. 0 (unset)
// 은 CRD default ("10Gi") 가 채워주므로 본 함수 도달 시점엔 항상 양수.
func validateStorageSize(path *field.Path, size resource.Quantity) field.ErrorList {
	if size.IsZero() {
		// CRD default 가 채워지지 않은 dry-run / omitempty path — 별도 invariant
		// 아님 (기본값으로 fallback).
		return nil
	}
	min := resource.MustParse("1Gi")
	if size.Cmp(min) < 0 {
		return field.ErrorList{field.Invalid(
			path, size.String(),
			"storage.size must be >= 1Gi — production mongodb requires minimum data dir + oplog headroom",
		)}
	}
	return nil
}
