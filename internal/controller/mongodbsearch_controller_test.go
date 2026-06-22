/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

var _ = Describe("MongoDBSearch Controller", func() {
	Context("When a MongoDBSearch references a MongoDB", func() {
		It("deploys mongot resources and wires the source mongod via annotation", func() {
			ctx := context.Background()
			const name = "search-src"

			// source MongoDB (RS) — 기존 헬퍼 재사용.
			mdb := newTestMongoDB(name, 3, "1Gi")
			Expect(k8sClient.Create(ctx, mdb)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, mdb) })

			// searchCoordinator sync 사용자 secret(MVP 필수).
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-sync", Namespace: mongoDBTestNS},
				Data:       map[string][]byte{"username": []byte("search-sync"), "password": []byte("x")},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, secret) })

			// MongoDBSearch.
			search := &mongodbv1beta1.MongoDBSearch{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mongoDBTestNS},
				Spec: mongodbv1beta1.MongoDBSearchSpec{
					Source:            mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: name}, Kind: "MongoDB"},
					Replicas:          1,
					Storage:           mongodbv1beta1.StorageSpec{Size: resource.MustParse("2Gi")},
					SyncUserSecretRef: &corev1.LocalObjectReference{Name: name + "-sync"},
				},
			}
			Expect(k8sClient.Create(ctx, search)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, search) })

			// mongot StatefulSet/Service/ConfigMap/NetworkPolicy 생성 확인.
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-mongot", Namespace: mongoDBTestNS}, &appsv1.StatefulSet{})
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed(), "mongot StatefulSet")
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-mongot", Namespace: mongoDBTestNS}, &corev1.Service{})
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed(), "mongot Service")
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-mongot-config", Namespace: mongoDBTestNS}, &corev1.ConfigMap{})
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed(), "mongot ConfigMap")
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-mongot", Namespace: mongoDBTestNS}, &netv1.NetworkPolicy{})
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed(), "mongot NetworkPolicy")

			// source MongoDB 에 mongot endpoint annotation 설정 확인.
			Eventually(func() string {
				m := &mongodbv1alpha1.MongoDB{}
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: mongoDBTestNS}, m)
				return m.Annotations[resources.MongotSearchEndpointAnnotation]
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Equal(resources.MongotEndpoint(name, mongoDBTestNS)), "source annotation")

			// 전체 통합: annotation 으로 source mongod STS args 에 mongotHost setParameter 주입.
			Eventually(func() bool {
				sts := &appsv1.StatefulSet{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: mongoDBTestNS}, sts); err != nil {
					return false
				}
				for _, c := range sts.Spec.Template.Spec.Containers {
					for _, a := range c.Args {
						if strings.Contains(a, "mongotHost=") {
							return true
						}
					}
				}
				return false
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(BeTrue(), "mongod STS gets mongotHost setParameter")
		})
	})
})
