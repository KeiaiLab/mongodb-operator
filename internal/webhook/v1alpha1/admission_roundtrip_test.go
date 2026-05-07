/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Admission round-trip 시나리오 — webhook_suite_test 의 envtest 통합 검증.
//
// unit-level 은 validateMongoDBSpec / CustomValidator 함수를 in-process 호출.
// 본 파일은 *실제 K8s API server* 를 통한 round-trip:
//   k8sClient.Create() → kube-apiserver → ValidatingWebhookConfiguration →
//   webhook server 의 admission handler → CustomValidator → field.ErrorList →
//   apierrors.NewInvalid → kube-apiserver → client error.
// admission framework 가 정확히 우리 Setup*WithManager 를 호출함을 보장.

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

var _ = Describe("MongoDB webhook admission round-trip", func() {
	It("rejects MongoDB with members=4 (split-brain) via real apiserver", func() {
		m := &mongodbv1alpha1.MongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rt-evenmembers",
				Namespace: "default",
			},
			Spec: mongodbv1alpha1.MongoDBSpec{
				Members: 4, // 짝수 — split-brain
				Version: mongodbv1alpha1.MongoDBVersion{Version: "8.3"},
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")},
				Auth: mongodbv1alpha1.AuthSpec{
					AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "admin-secret"},
				},
			},
		}
		err := k8sClient.Create(ctx, m)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"webhook 이 admission denial 반환해야 (apierrors.IsInvalid)")
		Expect(err.Error()).To(ContainSubstring("members"),
			"denial message 에 'members' keyword 포함 (운영자 원인 식별)")
	})

	It("rejects MongoDB with empty auth.adminCredentialsSecretRef.name", func() {
		m := &mongodbv1alpha1.MongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rt-emptysecret",
				Namespace: "default",
			},
			Spec: mongodbv1alpha1.MongoDBSpec{
				Members: 3,
				Version: mongodbv1alpha1.MongoDBVersion{Version: "8.3"},
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")},
				// Auth.AdminCredentialsSecretRef.Name 의도적 비움.
			},
		}
		err := k8sClient.Create(ctx, m)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("adminCredentialsSecretRef"))
	})

	It("accepts valid MongoDB CR — admission round-trip 통과", func() {
		m := &mongodbv1alpha1.MongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rt-happy",
				Namespace: "default",
			},
			Spec: mongodbv1alpha1.MongoDBSpec{
				Members: 3,
				Version: mongodbv1alpha1.MongoDBVersion{Version: "8.3"},
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")},
				Auth: mongodbv1alpha1.AuthSpec{
					AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "admin-secret"},
				},
			},
		}
		err := k8sClient.Create(ctx, m)
		Expect(err).NotTo(HaveOccurred(), "valid spec 은 admission 통과")

		// cleanup.
		Expect(k8sClient.Delete(ctx, m)).To(Succeed())
	})
})
