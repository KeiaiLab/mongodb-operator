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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

var _ = Describe("MongoDBSearch Controller (sidecar)", func() {
	Context("When a MongoDBSearch references a MongoDB", func() {
		It("creates mongot config + annotates source → mongod gets mongot sidecar", func() {
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
					SyncUserSecretRef: &corev1.LocalObjectReference{Name: name + "-sync"},
				},
			}
			Expect(k8sClient.Create(ctx, search)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, search) })

			// 1. mongot config ConfigMap 생성(sidecar — localhost syncSource).
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: resources.MongotConfigMapName(name), Namespace: mongoDBTestNS}, &corev1.ConfigMap{})
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed(), "mongot ConfigMap")

			// 2. source MongoDB 에 sidecar image annotation 설정.
			Eventually(func() string {
				m := &mongodbv1alpha1.MongoDB{}
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: mongoDBTestNS}, m)
				return m.Annotations[resources.MongotSidecarImageAnnotation]
			}, mongoDBTestTimeout, mongoDBTestInterval).ShouldNot(BeEmpty(), "source sidecar annotation")

			// 3. 전체 통합: MongoDB controller 가 annotation reconcile → mongod STS 에
			//    mongot sidecar 컨테이너 + mongotHost(localhost) setParameter 주입.
			Eventually(func() bool {
				sts := &appsv1.StatefulSet{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: mongoDBTestNS}, sts); err != nil {
					return false
				}
				hasSidecar, hasHost := false, false
				for _, c := range sts.Spec.Template.Spec.Containers {
					if c.Name == "mongot" {
						hasSidecar = true
					}
					for _, a := range c.Args {
						if strings.Contains(a, "mongotHost=localhost:") {
							hasHost = true
						}
					}
				}
				return hasSidecar && hasHost
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(BeTrue(), "mongod STS 에 mongot sidecar + localhost mongotHost")
		})
	})
})
