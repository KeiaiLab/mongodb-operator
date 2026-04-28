/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	mongoDBTestNS = "default"
	// envtest엔 workload controller가 없어 STS가 실제로 ready되지 않으므로
	// reconcile loop가 여러 번 도는 것을 감안해 timeout을 넉넉히 잡는다.
	mongoDBTestTimeout  = time.Second * 30
	mongoDBTestInterval = time.Millisecond * 250
)

func newTestMongoDB(name string, members int32, size string) *mongodbv1alpha1.MongoDB {
	return &mongodbv1alpha1.MongoDB{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mongodb.keiailab.com/v1alpha1",
			Kind:       "MongoDB",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mongoDBTestNS,
		},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        members,
			ReplicaSetName: "rs0",
			Version:        mongodbv1alpha1.MongoDBVersion{Version: "7.0"},
			Storage:        mongodbv1alpha1.StorageSpec{Size: resource.MustParse(size)},
			Auth: mongodbv1alpha1.AuthSpec{
				AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: name + "-admin"},
			},
		},
	}
}

// deleteMongoDBAndWait는 finalizer 흐름이 끝나길 기다린다.
// reconciler 등록 이후엔 단순 Delete가 즉시 사라지지 않고 handleDeletion이
// finalizer를 제거할 때까지 객체가 남는다.
func deleteMongoDBAndWait(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) {
	Expect(k8sClient.Delete(ctx, mdb)).Should(Succeed())
	key := types.NamespacedName{Name: mdb.Name, Namespace: mdb.Namespace}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, key, &mongodbv1alpha1.MongoDB{})
		return errors.IsNotFound(err)
	}, mongoDBTestTimeout, mongoDBTestInterval).Should(BeTrue())
}

var _ = Describe("MongoDB Controller", func() {
	Context("When creating a MongoDB resource", func() {
		It("Should accept the spec and persist it", func() {
			ctx := context.Background()
			mongodb := newTestMongoDB("test-mongodb", 3, "10Gi")
			Expect(k8sClient.Create(ctx, mongodb)).Should(Succeed())

			key := types.NamespacedName{Name: mongodb.Name, Namespace: mongodb.Namespace}
			created := &mongodbv1alpha1.MongoDB{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, created)
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed())

			Expect(created.Spec.Members).Should(Equal(int32(3)))
			Expect(created.Spec.ReplicaSetName).Should(Equal("rs0"))
			Expect(created.Spec.Auth.AdminCredentialsSecretRef.Name).Should(Equal("test-mongodb-admin"))

			deleteMongoDBAndWait(ctx, mongodb)
		})
	})

	Context("When validating MongoDB spec", func() {
		It("Should accept valid member counts", func() {
			ctx := context.Background()
			mongodb := newTestMongoDB("test-valid-members", 5, "20Gi")
			Expect(k8sClient.Create(ctx, mongodb)).Should(Succeed())
			deleteMongoDBAndWait(ctx, mongodb)
		})
	})

	Context("When updating MongoDB resources", func() {
		It("Should update the spec correctly", func() {
			ctx := context.Background()
			mongodb := newTestMongoDB("test-update-mongodb", 3, "10Gi")
			Expect(k8sClient.Create(ctx, mongodb)).Should(Succeed())

			key := types.NamespacedName{Name: mongodb.Name, Namespace: mongodb.Namespace}
			created := &mongodbv1alpha1.MongoDB{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, created)
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed())

			// reconciler가 finalizer를 추가하면서 generation이 변하므로 retry
			// 패턴으로 update 충돌 회피.
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, created); err != nil {
					return err
				}
				created.Spec.Members = 5
				return k8sClient.Update(ctx, created)
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Succeed())

			Eventually(func() int32 {
				updated := &mongodbv1alpha1.MongoDB{}
				if err := k8sClient.Get(ctx, key, updated); err != nil {
					return 0
				}
				return updated.Spec.Members
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(Equal(int32(5)))

			deleteMongoDBAndWait(ctx, mongodb)
		})
	})

	Context("When deleting MongoDB resources", func() {
		It("Should drain finalizer and remove the resource", func() {
			ctx := context.Background()
			mongodb := newTestMongoDB("test-delete-mongodb", 3, "10Gi")
			Expect(k8sClient.Create(ctx, mongodb)).Should(Succeed())

			deleteMongoDBAndWait(ctx, mongodb)
		})
	})

	Context("When the reconciler runs", func() {
		It("Should create downstream resources (StatefulSet, Services, Secret, ConfigMap)", func() {
			ctx := context.Background()
			const name = "test-reconcile-resources"
			mongodb := newTestMongoDB(name, 3, "10Gi")
			Expect(k8sClient.Create(ctx, mongodb)).Should(Succeed())

			// reconcile 단계 1-5는 envtest에서도 결정적으로 실행된다.
			// 이후 areAllPodsReady에서 멈춤(workload controller 없음).
			By("Eventually creating the StatefulSet")
			Eventually(func() bool {
				return statefulSetExists(ctx, name, mongoDBTestNS)
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(BeTrue())

			By("Eventually creating the headless Service")
			Eventually(func() bool {
				return serviceExists(ctx, name+"-headless", mongoDBTestNS)
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(BeTrue())

			By("Eventually creating the keyfile Secret")
			Eventually(func() bool {
				return secretExists(ctx, name+"-keyfile", mongoDBTestNS)
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(BeTrue())

			By("Setting an OwnerReference back to MongoDB on the StatefulSet")
			Eventually(func() bool {
				sts := &appsv1.StatefulSet{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: mongoDBTestNS}, sts); err != nil {
					return false
				}
				for _, owner := range sts.OwnerReferences {
					if owner.Kind == "MongoDB" && owner.Name == name {
						return true
					}
				}
				return false
			}, mongoDBTestTimeout, mongoDBTestInterval).Should(BeTrue())

			deleteMongoDBAndWait(ctx, mongodb)
		})
	})
})

// statefulSetExists는 envtest에서 STS 객체 존재 여부만 검증한다.
// (envtest엔 workload controller가 없어 ready replicas는 항상 0.)
func statefulSetExists(ctx context.Context, name, namespace string) bool {
	sts := &appsv1.StatefulSet{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts)
	return err == nil
}

func serviceExists(ctx context.Context, name, namespace string) bool {
	svc := &corev1.Service{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, svc)
	return err == nil
}

func secretExists(ctx context.Context, name, namespace string) bool {
	secret := &corev1.Secret{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret)
	return err == nil
}
