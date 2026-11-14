/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// spec.shards.setParameters CRD 검증 — webhook 이 꺼져 있어도(차트 기본) API 서버가 거절한다.
var _ = Describe("MongoDBSharded spec.shards.setParameters CRD validation", func() {
	ctx := context.Background()

	DescribeTable("rejects malformed entries",
		func(name string, params map[string]mongodbv1alpha1.SetParameterValue) {
			sh := newTestMongoDBSharded(name, 1, 1, "1Gi")
			sh.Spec.Shards.SetParameters = params
			err := k8sClient.Create(ctx, sh)
			Expect(errors.IsInvalid(err)).To(BeTrue(), "want Invalid, got %v", err)
		},
		Entry("space in key", "setparam-space", map[string]mongodbv1alpha1.SetParameterValue{"periodic Noop": "1"}),
		Entry("equals in key", "setparam-equals", map[string]mongodbv1alpha1.SetParameterValue{"a=b": "1"}),
		Entry("empty value", "setparam-empty", map[string]mongodbv1alpha1.SetParameterValue{"periodicNoopIntervalSecs": ""}),
		Entry("newline in value", "setparam-newline", map[string]mongodbv1alpha1.SetParameterValue{"periodicNoopIntervalSecs": "1\n--auth"}),
	)

	It("accepts a well-formed parameter", func() {
		sh := newTestMongoDBSharded("setparam-ok", 1, 1, "1Gi")
		sh.Spec.Shards.SetParameters = map[string]mongodbv1alpha1.SetParameterValue{"periodicNoopIntervalSecs": "1"}
		Expect(k8sClient.Create(ctx, sh)).To(Succeed())
		deleteShardedAndWait(ctx, sh)
	})
})
