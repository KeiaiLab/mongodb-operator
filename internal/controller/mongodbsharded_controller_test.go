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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	shardedTestNS       = "default"
	shardedTestTimeout  = time.Second * 30
	shardedTestInterval = time.Millisecond * 250
)

func newTestMongoDBSharded(name string, shards, members int32, storageSize string) *mongodbv1alpha1.MongoDBSharded {
	return &mongodbv1alpha1.MongoDBSharded{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mongodb.keiailab.com/v1alpha1",
			Kind:       "MongoDBSharded",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: shardedTestNS,
		},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{Version: "7.0"},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{
				Members: 3,
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")},
			},
			Shards: mongodbv1alpha1.ShardSpec{
				Count:           shards,
				MembersPerShard: members,
				Storage:         mongodbv1alpha1.StorageSpec{Size: resource.MustParse(storageSize)},
			},
			Mongos: mongodbv1alpha1.MongosSpec{Replicas: 2},
			Auth: mongodbv1alpha1.AuthSpec{
				AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: name + "-admin"},
			},
		},
	}
}

func deleteShardedAndWait(ctx context.Context, sh *mongodbv1alpha1.MongoDBSharded) {
	Expect(k8sClient.Delete(ctx, sh)).Should(Succeed())
	key := types.NamespacedName{Name: sh.Name, Namespace: sh.Namespace}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, key, &mongodbv1alpha1.MongoDBSharded{})
		return errors.IsNotFound(err)
	}, shardedTestTimeout, shardedTestInterval).Should(BeTrue())
}

var _ = Describe("MongoDBSharded Controller", func() {
	Context("When creating a MongoDBSharded resource", func() {
		It("Should accept the sharded spec and persist it", func() {
			ctx := context.Background()
			sharded := newTestMongoDBSharded("test-sharded", 2, 3, "50Gi")
			Expect(k8sClient.Create(ctx, sharded)).Should(Succeed())

			key := types.NamespacedName{Name: sharded.Name, Namespace: sharded.Namespace}
			created := &mongodbv1alpha1.MongoDBSharded{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, created)
			}, shardedTestTimeout, shardedTestInterval).Should(Succeed())

			Expect(created.Spec.ConfigServer.Members).Should(Equal(int32(3)))
			Expect(created.Spec.Shards.Count).Should(Equal(int32(2)))
			Expect(created.Spec.Shards.MembersPerShard).Should(Equal(int32(3)))
			Expect(created.Spec.Mongos.Replicas).Should(Equal(int32(2)))
			Expect(created.Spec.Auth.AdminCredentialsSecretRef.Name).Should(Equal("test-sharded-admin"))

			deleteShardedAndWait(ctx, sharded)
		})
	})

	Context("When validating MongoDBSharded spec", func() {
		It("Should accept valid shard configuration", func() {
			ctx := context.Background()
			sharded := newTestMongoDBSharded("test-valid-shards", 4, 3, "100Gi")
			Expect(k8sClient.Create(ctx, sharded)).Should(Succeed())
			deleteShardedAndWait(ctx, sharded)
		})

		It("Should accept custom storage class", func() {
			ctx := context.Background()
			sharded := newTestMongoDBSharded("test-storage-class", 2, 3, "50Gi")
			sharded.Spec.ConfigServer.Storage.StorageClassName = "fast-ssd"
			sharded.Spec.Shards.Storage.StorageClassName = "fast-ssd"
			Expect(k8sClient.Create(ctx, sharded)).Should(Succeed())

			key := types.NamespacedName{Name: sharded.Name, Namespace: sharded.Namespace}
			created := &mongodbv1alpha1.MongoDBSharded{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, created)
			}, shardedTestTimeout, shardedTestInterval).Should(Succeed())

			Expect(created.Spec.ConfigServer.Storage.StorageClassName).Should(Equal("fast-ssd"))
			Expect(created.Spec.Shards.Storage.StorageClassName).Should(Equal("fast-ssd"))

			deleteShardedAndWait(ctx, sharded)
		})
	})

	Context("When updating MongoDBSharded resources", func() {
		It("Should update the mongos replicas", func() {
			ctx := context.Background()
			sharded := newTestMongoDBSharded("test-update-sharded", 2, 3, "50Gi")
			Expect(k8sClient.Create(ctx, sharded)).Should(Succeed())

			key := types.NamespacedName{Name: sharded.Name, Namespace: sharded.Namespace}
			created := &mongodbv1alpha1.MongoDBSharded{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, created)
			}, shardedTestTimeout, shardedTestInterval).Should(Succeed())

			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, created); err != nil {
					return err
				}
				created.Spec.Mongos.Replicas = 4
				return k8sClient.Update(ctx, created)
			}, shardedTestTimeout, shardedTestInterval).Should(Succeed())

			Eventually(func() int32 {
				updated := &mongodbv1alpha1.MongoDBSharded{}
				if err := k8sClient.Get(ctx, key, updated); err != nil {
					return 0
				}
				return updated.Spec.Mongos.Replicas
			}, shardedTestTimeout, shardedTestInterval).Should(Equal(int32(4)))

			deleteShardedAndWait(ctx, sharded)
		})
	})

	Context("When deleting MongoDBSharded resources", func() {
		It("Should drain finalizer and remove the resource", func() {
			ctx := context.Background()
			sharded := newTestMongoDBSharded("test-delete-sharded", 2, 3, "50Gi")
			Expect(k8sClient.Create(ctx, sharded)).Should(Succeed())

			deleteShardedAndWait(ctx, sharded)
		})
	})
})
