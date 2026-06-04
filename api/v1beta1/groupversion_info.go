/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package v1beta1 contains API Schema definitions for the mongodb v1beta1 API group.
// +kubebuilder:object:generate=true
// +groupName=mongodb.keiailab.com
package v1beta1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "mongodb.keiailab.com", Version: "v1beta1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	// scheme.Builder 는 controller-runtime 에서 deprecated 되었으나 kubebuilder
	// 표준 scaffold 패턴이며 v1alpha1 안정 호환을 위해 유지. 후속 마이그레이션은
	// 별도 RFC 로 추적.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // SA1019: kubebuilder scaffold pattern, see RFC backlog

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
