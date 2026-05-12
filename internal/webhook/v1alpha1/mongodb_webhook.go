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
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	commonswebhook "github.com/keiailab/operator-commons/pkg/webhook"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	auditpkg "github.com/keiailab/mongodb-operator/internal/controller/audit"
	authpkg "github.com/keiailab/mongodb-operator/internal/controller/auth"
	encryptionpkg "github.com/keiailab/mongodb-operator/internal/controller/encryption"
)

// SetupMongoDBWebhookWithManager registers the validating webhook for MongoDB.
func SetupMongoDBWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &mongodbv1alpha1.MongoDB{}).
		WithValidator(&MongoDBCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-mongodb-keiailab-com-v1alpha1-mongodb,mutating=false,failurePolicy=fail,sideEffects=None,groups=mongodb.keiailab.com,resources=mongodbs,verbs=create;update,versions=v1alpha1,name=vmongodb-v1alpha1.kb.io,admissionReviewVersions=v1

// MongoDBCustomValidator — admission validation.
type MongoDBCustomValidator struct{}

// ValidateCreate — 신규 CR 검증. controller-runtime v0.23+ generic API
// (compiler 가 type 강제 → type assertion 불필요).
func (v *MongoDBCustomValidator) ValidateCreate(_ context.Context, m *mongodbv1alpha1.MongoDB) (admission.Warnings, error) {
	if errs := validateMongoDBSpec(m); len(errs) > 0 {
		return nil, apiError("MongoDB", m.GetName(), errs)
	}
	return nil, nil
}

// ValidateUpdate — spec 검증 + immutable 가드 + F11 (cycle 7) upgrade path 검증.
func (v *MongoDBCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *mongodbv1alpha1.MongoDB) (admission.Warnings, error) {
	if errs := validateMongoDBSpec(newObj); len(errs) > 0 {
		return nil, apiError("MongoDB", newObj.GetName(), errs)
	}

	// F11 cycle 7: version upgrade path 가드. old 와 new 의 version 이
	// 다르면 IsValidUpgradePath 호출. 사용자가 8.0 → 8.3 같이 minor skip
	// 또는 downgrade 시도 시 webhook 단계에서 reject.
	if oldObj != nil && oldObj.Spec.Version.Version != "" {
		oldV := oldObj.Spec.Version.Version
		newV := newObj.Spec.Version.Version
		if oldV != newV {
			if err := mongodbv1alpha1.IsValidUpgradePath(oldV, newV); err != nil {
				errs := field.ErrorList{
					field.Invalid(field.NewPath("spec", "version", "version"), newV, err.Error()),
				}
				return nil, apiError("MongoDB", newObj.GetName(), errs)
			}
		}
	}

	return nil, nil
}

// ValidateDelete — 항상 허용.
func (v *MongoDBCustomValidator) ValidateDelete(_ context.Context, _ *mongodbv1alpha1.MongoDB) (admission.Warnings, error) {
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

	// cycle 19 — G-11 standalone mode (members=1) 자동 검증:
	// autoScaling.enabled=true 인데 members=1 이면 reject (단일 member RS scale 의미 없음).
	if m.Spec.IsStandaloneMode() {
		if m.Spec.AutoScaling != nil && m.Spec.AutoScaling.Enabled {
			errs = append(errs, field.Invalid(p.Child("autoScaling", "enabled"), true,
				"autoScaling not allowed when members=1 (standalone mode)"))
		}
	}

	// cycle 18 — LDAP/OIDC/Encryption/Audit 검증 호출 (webhook integration).
	if m.Spec.Auth.LDAP != nil {
		if err := authpkg.ValidateLDAPSpec(m.Spec.Auth.LDAP); err != nil {
			errs = append(errs, field.Invalid(p.Child("auth", "ldap"), m.Spec.Auth.LDAP, err.Error()))
		}
	}
	if m.Spec.Auth.OIDC != nil {
		if err := authpkg.ValidateOIDCSpec(m.Spec.Auth.OIDC); err != nil {
			errs = append(errs, field.Invalid(p.Child("auth", "oidc"), m.Spec.Auth.OIDC, err.Error()))
		}
	}
	if m.Spec.Storage.Encryption != nil {
		if err := encryptionpkg.ValidateEncryptionSpec(m.Spec.Storage.Encryption); err != nil {
			errs = append(errs, field.Invalid(p.Child("storage", "encryption"), m.Spec.Storage.Encryption, err.Error()))
		}
	}
	if m.Spec.AuditLog != nil {
		if err := auditpkg.ValidateAuditLogSpec(m.Spec.AuditLog); err != nil {
			errs = append(errs, field.Invalid(p.Child("auditLog"), m.Spec.AuditLog, err.Error()))
		}
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

	// tls.{certManager,customCert} omitempty trap — pointer 활성화 시 내부
	// required string 이 빈 값 가능.
	errs = append(errs, validateTLSSpec(p.Child("tls"), m.Spec.TLS)...)

	// backup.storage.s3 omitempty trap — Bucket / CredentialsRef.Name 검증.
	errs = append(errs, validateBackupSpec(p.Child("backup"), m.Spec.Backup)...)

	return errs
}

// validateTLSSpec — TLS pointer 활성화 시 nested required string non-empty 강제.
// CertManager 와 CustomCert 둘 다 nil 이면 controller self-signed 사용 가능
// (통과). 활성화된 분기만 internal field 검증.
func validateTLSSpec(path *field.Path, tls *mongodbv1alpha1.TLSSpec) field.ErrorList {
	if tls == nil {
		return nil
	}
	var errs field.ErrorList
	if tls.CertManager != nil && tls.CertManager.IssuerRef.Name == "" {
		errs = append(errs, field.Invalid(
			path.Child("certManager", "issuerRef", "name"), "",
			"tls.certManager.issuerRef.name must be non-empty when certManager is set",
		))
	}
	if tls.CustomCert != nil && tls.CustomCert.SecretName == "" {
		errs = append(errs, field.Invalid(
			path.Child("customCert", "secretName"), "",
			"tls.customCert.secretName must be non-empty when customCert is set",
		))
	}
	return errs
}

// validateBackupSpec — Backup 활성화 시 Storage.Type 분기에 따른 nested
// required string non-empty 강제. controller 가 backup job 시작 시 이 값들이
// 누락되면 silent skip 또는 즉시 실패 → webhook 단계 reject.
//
// Schedule 검증: postgres-operator 의 webhook 과 동일 패턴 (cross-cut audit).
func validateBackupSpec(path *field.Path, b *mongodbv1alpha1.BackupSpec) field.ErrorList {
	if b == nil || !b.Enabled {
		return nil
	}
	var errs field.ErrorList
	// schedule 비어있으면 controller 가 자동 backup CronJob 미생성 → silent
	// failure. Enabled=true 의도 위반.
	if b.Schedule == "" {
		errs = append(errs, field.Invalid(
			path.Child("schedule"), "",
			"backup.schedule must be non-empty cron expression when backup.enabled=true (e.g. \"0 2 * * *\")",
		))
	}
	if b.Storage.Type == "s3" && b.Storage.S3 != nil {
		s3 := b.Storage.S3
		if s3.Bucket == "" {
			errs = append(errs, field.Invalid(
				path.Child("storage", "s3", "bucket"), "",
				"backup.storage.s3.bucket must be non-empty when type=s3",
			))
		}
		if s3.CredentialsRef.Name == "" {
			errs = append(errs, field.Invalid(
				path.Child("storage", "s3", "credentialsRef", "name"), "",
				"backup.storage.s3.credentialsRef.name must be non-empty when type=s3",
			))
		}
	}
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
