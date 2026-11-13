/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"maps"
	"slices"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// setParameterArgs 는 params 를 키 정렬 순서의 `--setParameter k=v` 인자로 만든다.
// 정렬은 렌더 결정성(map 순회 순서 무작위 → 불필요한 롤링 재시작 방지)을 위해서다.
// params 가 비면 nil — 호출부 인자가 바뀌지 않는다.
func setParameterArgs(params map[string]mongodbv1alpha1.SetParameterValue) []string {
	if len(params) == 0 {
		return nil
	}

	args := make([]string, 0, 2*len(params))
	for _, key := range slices.Sorted(maps.Keys(params)) {
		args = append(args, setParameterFlag, key+"="+string(params[key]))
	}
	return args
}
