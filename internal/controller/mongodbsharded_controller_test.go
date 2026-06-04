/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
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

func markShardedStatefulSetReady(ctx context.Context, name string, ready int32) {
	key := types.NamespacedName{Name: name, Namespace: shardedTestNS}
	Eventually(func() error {
		sts := &appsv1.StatefulSet{}
		if err := k8sClient.Get(ctx, key, sts); err != nil {
			return err
		}
		sts.Status.Replicas = ready
		sts.Status.ReadyReplicas = ready
		return k8sClient.Status().Update(ctx, sts)
	}, shardedTestTimeout, shardedTestInterval).Should(Succeed())
}

func assertRestrictedPodSpec(spec corev1.PodSpec) {
	Expect(spec.SecurityContext).NotTo(BeNil())
	Expect(spec.SecurityContext.RunAsNonRoot).NotTo(BeNil())
	Expect(*spec.SecurityContext.RunAsNonRoot).To(BeTrue())
	for _, c := range spec.InitContainers {
		assertRestrictedContainer(c.Name, c.SecurityContext)
	}
	for _, c := range spec.Containers {
		assertRestrictedContainer(c.Name, c.SecurityContext)
	}
}

func assertRestrictedContainer(name string, sc *corev1.SecurityContext) {
	Expect(sc).NotTo(BeNil(), "%s: SecurityContext nil", name)
	Expect(sc.AllowPrivilegeEscalation).NotTo(BeNil(), "%s: AllowPrivilegeEscalation nil", name)
	Expect(*sc.AllowPrivilegeEscalation).To(BeFalse(), "%s: AllowPrivilegeEscalation must be false", name)
	Expect(sc.Capabilities).NotTo(BeNil(), "%s: Capabilities nil", name)
	Expect(sc.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")),
		"%s: capabilities.drop must include ALL", name)
	Expect(sc.SeccompProfile).NotTo(BeNil(), "%s: SeccompProfile nil", name)
	Expect(sc.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault),
		"%s: seccompProfile.type must be RuntimeDefault", name)
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

	Context("When reconciling sharded workload PodSpecs", func() {
		It("Should keep controller-created cfg, shard, and mongos PodSpecs restricted-compliant", func() {
			ctx := context.Background()
			name := "test-sharded-podsecurity"
			adminSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-admin", Namespace: shardedTestNS},
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("test_password_20260507"),
				},
			}
			Expect(k8sClient.Create(ctx, adminSecret)).Should(Succeed())

			sharded := newTestMongoDBSharded(name, 1, 1, "1Gi")
			sharded.Spec.ConfigServer.Members = 1
			sharded.Spec.Mongos.Replicas = 1
			//lint:ignore SA1019 ADR-0018 Phase 1: MonitoringSpec deprecated 명시, test 영역 보존 (Phase 2 trigger 후 결정).
			sharded.Spec.Monitoring = &mongodbv1alpha1.MonitoringSpec{Enabled: true} //nolint:staticcheck
			Expect(k8sClient.Create(ctx, sharded)).Should(Succeed())

			cfg := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-cfg", Namespace: shardedTestNS}, cfg)
			}, shardedTestTimeout, shardedTestInterval).Should(Succeed())
			assertRestrictedPodSpec(cfg.Spec.Template.Spec)

			markShardedStatefulSetReady(ctx, name+"-cfg", 1)

			shard := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-shard-0", Namespace: shardedTestNS}, shard)
			}, shardedTestTimeout, shardedTestInterval).Should(Succeed())
			assertRestrictedPodSpec(shard.Spec.Template.Spec)

			markShardedStatefulSetReady(ctx, name+"-shard-0", 1)

			mongos := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-mongos", Namespace: shardedTestNS}, mongos)
			}, shardedTestTimeout, shardedTestInterval).Should(Succeed())
			assertRestrictedPodSpec(mongos.Spec.Template.Spec)

			deleteShardedAndWait(ctx, sharded)
			Expect(k8sClient.Delete(ctx, adminSecret)).Should(Succeed())
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

	// C37 (cluster-ops audit) 회귀 가드 — envtest 한계로 Skip.
	//
	// *Why skipped*: reconcile 의 updateStatus 호출은 *config server pods ready
	// 도달 후* (mongodbsharded_controller.go:reconcile 의 'Waiting for config
	// server to be ready' 분기 통과 후). envtest 환경에서는 *kube-apiserver +
	// CRD 만* — *실제 mongo 프로세스 미실행* → config server pod ready 영구
	// 미도달 → updateStatus 도달 안 함 → conditions 갱신 0.
	//
	// 본 conditions 의 정확성 검증은 *별 영역*:
	//   1. Manual production verification: keiailab-mongo (운영) 의 kubectl get
	//      mongodbsharded -o yaml 의 status.conditions 6 baseline 확인.
	//   2. C37 4차 작업 cycle 에서 isolated unit test (updateStatus 함수 직접
	//      호출, mock CR + ComponentStatus 주입) 영역.
	Context("[Skipped C37 conditions test — envtest 한계]", func() {
		It("Should report 6 baseline conditions (manual/production 검증 영역)", func() {
			Skip("envtest 환경 한계: config server pod 가 실제 mongo 미실행 → updateStatus 도달 안 함. " +
				"conditions 정확성은 production cluster 에서 검증 (keiailab-mongo).")
		})
	})
})
