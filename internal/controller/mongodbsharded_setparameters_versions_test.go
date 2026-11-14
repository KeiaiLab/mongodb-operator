/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	noopParam      = "periodicNoopIntervalSecs"
	noopParamValue = "1"
	noopParamArg   = noopParam + "=" + noopParamValue
)

// readSetParameters 는 주어진 API 버전으로 CR 을 읽어 spec.shards.setParameters 를 돌려준다.
func readSetParameters(ctx context.Context, version, name string) map[string]string {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: mongodbv1alpha1.GroupVersion.Group, Version: version, Kind: "MongoDBSharded",
	})
	key := types.NamespacedName{Name: name, Namespace: shardedTestNS}
	Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())

	params, _, err := unstructured.NestedStringMap(obj.Object, "spec", "shards", "setParameters")
	Expect(err).NotTo(HaveOccurred())
	return params
}

// 저장 버전은 v1beta1 이고 변환은 None 이다 — 필드가 한 버전 스키마에만 있으면
// API 서버가 저장할 때 잘라내 컨트롤러(v1alpha1 로 읽음)에 닿지 않는다.
var _ = Describe("MongoDBSharded spec.shards.setParameters across API versions", func() {
	ctx := context.Background()

	It("survives storage and reaches the shard StatefulSet", func() {
		name := "setparam-versions"
		adminSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-admin", Namespace: shardedTestNS},
			Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("test_password_20261114")},
		}
		Expect(k8sClient.Create(ctx, adminSecret)).To(Succeed())

		sh := newTestMongoDBSharded(name, 1, 1, "1Gi")
		sh.Spec.ConfigServer.Members = 1
		sh.Spec.Mongos.Replicas = 1
		sh.Spec.Shards.SetParameters = map[string]mongodbv1alpha1.SetParameterValue{noopParam: noopParamValue}
		Expect(k8sClient.Create(ctx, sh)).To(Succeed())

		want := map[string]string{noopParam: noopParamValue}
		Expect(readSetParameters(ctx, "v1alpha1", name)).To(Equal(want), "read as v1alpha1")
		Expect(readSetParameters(ctx, "v1beta1", name)).To(Equal(want), "read as v1beta1 (storage version)")

		markShardedStatefulSetReady(ctx, name+"-cfg", 1)

		shard := &appsv1.StatefulSet{}
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-shard-0", Namespace: shardedTestNS}, shard)
		}, shardedTestTimeout, shardedTestInterval).Should(Succeed())

		args := shard.Spec.Template.Spec.Containers[0].Args
		Expect(args).To(ContainElement(noopParamArg), "shard mongod args")

		deleteShardedAndWait(ctx, sh)
		Expect(k8sClient.Delete(ctx, adminSecret)).To(Succeed())
	})
})
