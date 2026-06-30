package controller

import (
	"context"

	commonssecrethash "github.com/keiailab/keiailab-commons/pkg/secrethash"
	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// reconcilePasswordRotation detects auth Secret changes and sets a condition
// to signal that MongoDB admin password needs rotation.
// Actual db.updateUser() call requires MongoDB connectivity (Phase 2).
func (r *MongoDBReconciler) reconcilePasswordRotation(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	if mdb.Spec.Auth.AdminCredentialsSecretRef.Name == "" {
		return nil
	}
	logger := log.FromContext(ctx)

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: mdb.Spec.Auth.AdminCredentialsSecretRef.Name, Namespace: mdb.Namespace,
	}, secret); err != nil {
		return err
	}

	currentHash := hashSecretData(secret)
	stored := getAnnotation(mdb, "mongodb.keiailab.com/auth-secret-hash")

	if stored == "" {
		setAnnotation(mdb, "mongodb.keiailab.com/auth-secret-hash", currentHash)
		if err := r.Update(ctx, mdb); err != nil {
			return err
		}
		return nil
	}

	if stored != currentHash {
		logger.Info("auth Secret changed, password rotation needed",
			"secret", mdb.Spec.Auth.AdminCredentialsSecretRef.Name)
		applyStatus := func() {
			setUpgradeCondition(mdb, "PasswordRotationPending", metav1.ConditionTrue,
				"SecretChanged", "Admin credentials Secret has changed, password rotation pending")
		}
		applyStatus()
		setAnnotation(mdb, "mongodb.keiailab.com/auth-secret-hash", currentHash)
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, applyStatus); err != nil {
			return err
		}
		if err := r.Update(ctx, mdb); err != nil {
			return err
		}
	}
	return nil
}

func hashSecretData(secret *corev1.Secret) string {
	// 결정적 정렬-누적은 keiailab-commons/pkg/secrethash.Hash 에 위임 — 기존
	// `for k, v := range secret.Data` map 순회의 *비결정성 버그*(동일 Secret 이
	// reconcile 마다 다른 해시 → 불필요한 rotation 신호) 를 구조적 교정. 16자
	// truncation 은 annotation 형식 보존을 위해 유지 (determinism 만 교정 →
	// adopt 시 1회 해시 변화 후 안정).
	return commonssecrethash.Hash(secret.Data)[:16]
}

func getAnnotation(mdb *mongodbv1alpha1.MongoDB, key string) string {
	if mdb.Annotations == nil {
		return ""
	}
	return mdb.Annotations[key]
}

func setAnnotation(mdb *mongodbv1alpha1.MongoDB, key, value string) {
	if mdb.Annotations == nil {
		mdb.Annotations = make(map[string]string)
	}
	mdb.Annotations[key] = value
}
