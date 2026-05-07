/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package v1alpha1

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// apiError — field.ErrorList → apierrors.Invalid wrapper. valkey-operator
// 와 동일 패턴 (3 operator 통일).
func apiError(kind, name string, errs field.ErrorList) error {
	gv := schema.GroupKind{Group: "mongodb.keiailab.com", Kind: kind}
	return apierrors.NewInvalid(gv, name, errs)
}
