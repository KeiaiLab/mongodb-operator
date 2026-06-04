/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package v1alpha1

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// apiError — field.ErrorList → apierrors.Invalid wrapper. valkey-operator
// 와 동일 패턴 (3 operator 통일). GroupVersion.Group 참조 — group string 변경
// 시 자동 추적 (하드코딩 회피).
func apiError(kind, name string, errs field.ErrorList) error {
	gv := schema.GroupKind{Group: mongodbv1alpha1.GroupVersion.Group, Kind: kind}
	return apierrors.NewInvalid(gv, name, errs)
}
