/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package v1alpha1

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// setParameterKeyPattern — mongod 서버 파라미터 이름 꼴. 빈 키 · 공백 · '=' 를 거절한다.
var setParameterKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*$`)

const lineBreaks = "\r\n"

// validateShardSetParameters — spec.shards.setParameters 키 · 값 형식 검증.
// 값은 비지 않아야 하고 줄바꿈이 없어야 한다(공백은 JSON 값 때문에 허용).
func validateShardSetParameters(path *field.Path, params map[string]mongodbv1alpha1.SetParameterValue) field.ErrorList {
	var errs field.ErrorList
	for _, key := range slices.Sorted(maps.Keys(params)) {
		value := string(params[key])
		if !setParameterKeyPattern.MatchString(key) {
			errs = append(errs, field.Invalid(path.Key(key), key,
				"setParameters key must match "+setParameterKeyPattern.String()))
			continue
		}
		if value == "" || strings.ContainsAny(value, lineBreaks) {
			errs = append(errs, field.Invalid(path.Key(key), value,
				"setParameters value must be non-empty and contain no line breaks"))
		}
	}
	return errs
}
