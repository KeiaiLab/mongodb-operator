/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	commonswebhook "github.com/keiailab/operator-commons/pkg/webhook"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// SetupMongoDBShardedWebhookWithManager registers the validating webhook.
func SetupMongoDBShardedWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &mongodbv1alpha1.MongoDBSharded{}).
		WithValidator(&MongoDBShardedCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-mongodb-keiailab-com-v1alpha1-mongodbsharded,mutating=false,failurePolicy=fail,sideEffects=None,groups=mongodb.keiailab.com,resources=mongodbshardeds,verbs=create;update,versions=v1alpha1,name=vmongodbsharded-v1alpha1.kb.io,admissionReviewVersions=v1

// MongoDBShardedCustomValidator — sharded cluster admission validation.
type MongoDBShardedCustomValidator struct{}

func (v *MongoDBShardedCustomValidator) ValidateCreate(_ context.Context, m *mongodbv1alpha1.MongoDBSharded) (admission.Warnings, error) {
	if errs := validateMongoDBShardedSpec(m); len(errs) > 0 {
		return nil, apiError("MongoDBSharded", m.GetName(), errs)
	}
	return nil, nil
}

func (v *MongoDBShardedCustomValidator) ValidateUpdate(_ context.Context, _, newObj *mongodbv1alpha1.MongoDBSharded) (admission.Warnings, error) {
	if errs := validateMongoDBShardedSpec(newObj); len(errs) > 0 {
		return nil, apiError("MongoDBSharded", newObj.GetName(), errs)
	}
	return nil, nil
}

func (v *MongoDBShardedCustomValidator) ValidateDelete(_ context.Context, _ *mongodbv1alpha1.MongoDBSharded) (admission.Warnings, error) {
	return nil, nil
}

// validateMongoDBShardedSpec — 조합 검증.
func validateMongoDBShardedSpec(m *mongodbv1alpha1.MongoDBSharded) field.ErrorList {
	var errs field.ErrorList
	p := field.NewPath("spec")

	// version 화이트리스트 (commons 위임).
	if err := commonswebhook.ValidateWithPredicate(
		p.Child("version", "version"), m.Spec.Version.Version,
		mongodbv1alpha1.IsSupportedMongoDBVersion,
		mongodbv1alpha1.SupportedMongoDBVersions,
	); err != nil {
		errs = append(errs, err)
	}

	// ConfigServer.Members: CRD enum 1;3 강제 — 본 webhook 검증 불필요.

	// Shards.Count: minimum=1 (CRD), 최대 64 권장 (operational concern).
	if m.Spec.Shards.Count > 64 {
		errs = append(errs, field.Invalid(
			p.Child("shards", "count"), m.Spec.Shards.Count,
			"shards.count exceeds operational limit 64 — large cluster requires capacity planning ADR",
		))
	}

	// Shards.MembersPerShard: 1 (single) 또는 odd >= 3 (quorum).
	if m.Spec.Shards.MembersPerShard != 0 &&
		m.Spec.Shards.MembersPerShard != 1 &&
		m.Spec.Shards.MembersPerShard%2 == 0 {
		errs = append(errs, field.Invalid(
			p.Child("shards", "membersPerShard"), m.Spec.Shards.MembersPerShard,
			"membersPerShard must be 1 or odd >= 3 — even count risks split-brain per shard",
		))
	}

	// storage.size 하한 1Gi — configServer + shards 둘 다 검증 (mongodb_webhook
	// 의 validateStorageSize 재사용).
	errs = append(errs, validateStorageSize(p.Child("configServer", "storage", "size"), m.Spec.ConfigServer.Storage.Size)...)
	errs = append(errs, validateStorageSize(p.Child("shards", "storage", "size"), m.Spec.Shards.Storage.Size)...)

	// auth.adminCredentialsSecretRef.name 비어있지 않음 (mongodb_webhook 와 동일).
	errs = append(errs, validateAuthSecretRef(p.Child("auth", "adminCredentialsSecretRef", "name"), m.Spec.Auth.AdminCredentialsSecretRef.Name)...)

	// TLS / Backup omitempty trap (mongodb_webhook 와 동일 helper 재사용).
	errs = append(errs, validateTLSSpec(p.Child("tls"), m.Spec.TLS)...)
	errs = append(errs, validateBackupSpec(p.Child("backup"), m.Spec.Backup)...)

	return errs
}
