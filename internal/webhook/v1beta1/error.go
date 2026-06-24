/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package v1beta1

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

// apiError — field.ErrorList → apierrors.Invalid wrapper(v1alpha1 webhook 패턴 정합).
func apiError(kind, name string, errs field.ErrorList) error {
	gk := schema.GroupKind{Group: mongodbv1beta1.GroupVersion.Group, Kind: kind}
	return apierrors.NewInvalid(gk, name, errs)
}
