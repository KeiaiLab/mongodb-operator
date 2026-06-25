/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package v1alpha1

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	commonswebhook "github.com/keiailab/keiailab-commons/pkg/webhook"

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

func (v *MongoDBShardedCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *mongodbv1alpha1.MongoDBSharded) (admission.Warnings, error) {
	if errs := validateMongoDBShardedSpec(newObj); len(errs) > 0 {
		return nil, apiError("MongoDBSharded", newObj.GetName(), errs)
	}
	// F11 cycle 7: upgrade path 가드 (sharded 동일).
	if oldObj != nil && oldObj.Spec.Version.Version != "" {
		oldV, newV := oldObj.Spec.Version.Version, newObj.Spec.Version.Version
		if oldV != newV {
			if err := mongodbv1alpha1.IsValidUpgradePath(oldV, newV); err != nil {
				errs := field.ErrorList{
					field.Invalid(field.NewPath("spec", "version", "version"), newV, err.Error()),
				}
				return nil, apiError("MongoDBSharded", newObj.GetName(), errs)
			}
		}
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

	// Shards.Arbiter 일관성 (ROADMAP 4.2-a).
	errs = append(errs, validateShardArbiter(p.Child("shards", "arbiter"), m.Spec.Shards.Arbiter, m.Spec.Shards.MembersPerShard)...)

	// ShardedCollections 형식 검증 (Track B). timeseries 적합성은 런타임.
	errs = append(errs, validateShardedCollections(p.Child("shardedCollections"), m.Spec.ShardedCollections)...)

	return errs
}

// validateShardedCollections — 선언적 샤딩 컬렉션 형식 검증.
//
// CRD marker(MinLength/Enum/MinItems)가 일부 강제하지만, *조합 모순* 은 webhook
// 에서만 검증 가능:
//   - db/coll non-empty(MinLength=1 중복이지만 명시 가드).
//   - shard key 필드 non-empty + order ∈ {1,-1,hashed}(Enum 중복 가드).
//   - 동일 namespace(db.coll) 중복 선언 금지 — 마지막 선언이 silent 우선되는
//     혼란 차단.
//   - timeseries: unique 와 동시 설정 금지(MongoDB 8.x 제약), timeField non-empty.
//
// timeseries shard key 적합성(metaField/그 하위 또는 timeField 만 허용)은 컬렉션
// 생성 옵션을 알아야 하므로 런타임(reconcile)에서 server 가 거부 → ShardKeyDrift
// 가 아닌 일반 에러로 surface.
// orderHashed 는 shard key field 의 hashed sharding order 값(MongoDB 표현).
const orderHashed = "hashed"

func validateShardedCollections(path *field.Path, scs []mongodbv1alpha1.ShardedCollectionSpec) field.ErrorList {
	var errs field.ErrorList
	seen := make(map[string]struct{}, len(scs))
	for i, sc := range scs {
		ip := path.Index(i)
		if sc.Database == "" {
			errs = append(errs, field.Invalid(ip.Child("database"), sc.Database, "database must not be empty"))
		}
		if sc.Collection == "" {
			errs = append(errs, field.Invalid(ip.Child("collection"), sc.Collection, "collection must not be empty"))
		}
		ns := sc.Database + "." + sc.Collection
		if _, dup := seen[ns]; dup {
			errs = append(errs, field.Duplicate(ip, ns))
		}
		seen[ns] = struct{}{}

		if len(sc.ShardKey) == 0 {
			errs = append(errs, field.Invalid(ip.Child("shardKey"), sc.ShardKey, "shardKey must have at least 1 field"))
		}
		for j, f := range sc.ShardKey {
			kp := ip.Child("shardKey").Index(j)
			if f.Field == "" {
				errs = append(errs, field.Invalid(kp.Child("field"), f.Field, "shard key field must not be empty"))
			}
			if f.Order != "1" && f.Order != "-1" && f.Order != orderHashed {
				errs = append(errs, field.NotSupported(kp.Child("order"), f.Order, []string{"1", "-1", orderHashed}))
			}
		}

		if sc.Timeseries != nil {
			tp := ip.Child("timeseries")
			if sc.Unique {
				errs = append(errs, field.Invalid(ip.Child("unique"), sc.Unique,
					"unique constraint is not supported for timeseries collections (MongoDB 8.x)"))
			}
			if sc.Timeseries.TimeField == "" {
				errs = append(errs, field.Invalid(tp.Child("timeField"), sc.Timeseries.TimeField,
					"timeField must not be empty for timeseries collections"))
			}
			// timeField 를 hashed shard key 로 쓰는 것은 MongoDB 가 금지 — 조기 차단.
			for j, f := range sc.ShardKey {
				if f.Field == sc.Timeseries.TimeField && f.Order == orderHashed {
					errs = append(errs, field.Invalid(ip.Child("shardKey").Index(j).Child("order"), f.Order,
						"timeField cannot be a hashed shard key for timeseries collections"))
				}
			}
		}
	}
	return errs
}

// validateShardArbiter — sharded cluster 의 shard 별 arbiter 설정 검증.
//
// MongoDB invariant:
//   - RS 당 arbiter 는 최대 1개. CRD marker 가 Maximum=1 강제하므로 본 함수는
//     *조합 모순* 만 검증.
//   - Enabled=true 인데 Replicas=0 이면 모순 — Replicas>=1 강제.
//   - Enabled=true + MembersPerShard 가 짝수 RS 멤버 + arbiter (홀수) →
//     vote 총합 짝수 → split-brain 위험. PSA 전형 권장: data 2 + arbiter 1
//     = 멤버 3 (홀수). 따라서 MembersPerShard 가 짝수일 때 arbiter 1 추가는
//     OK (홀수 됨). MembersPerShard 홀수 + arbiter 1 = 짝수 → reject.
func validateShardArbiter(path *field.Path, arb *mongodbv1alpha1.ShardArbiterSpec, membersPerShard int32) field.ErrorList {
	if arb == nil || !arb.Enabled {
		return nil
	}
	var errs field.ErrorList
	if arb.Replicas == 0 {
		errs = append(errs, field.Invalid(
			path.Child("replicas"), arb.Replicas,
			"arbiter.replicas must be >= 1 when enabled=true (0 contradicts enabled)",
		))
	}
	if arb.Replicas > 1 {
		errs = append(errs, field.Invalid(
			path.Child("replicas"), arb.Replicas,
			"arbiter.replicas must be 0 or 1 — MongoDB allows at most 1 arbiter per replica set",
		))
	}
	// 짝수 vote 방지. MembersPerShard 가 0 (미설정) 이면 CRD default=3 로 채워짐 →
	// 본 분기는 명시값만 검사.
	if membersPerShard > 0 && arb.Replicas > 0 {
		totalVotes := membersPerShard + arb.Replicas
		if totalVotes%2 == 0 {
			errs = append(errs, field.Invalid(
				path.Child("replicas"), arb.Replicas,
				"shard total vote count would be even (membersPerShard + arbiter.replicas) — odd quorum required to avoid split-brain",
			))
		}
	}
	return errs
}
