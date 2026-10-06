/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package v1alpha1

import (
	"slices"
	"testing"
)

// FuzzIsSupportedMongoDBVersion — webhook 이 받는 임의 문자열에서 panic 이 없고,
// 화이트리스트 통과(IsSupportedMongoDBVersion)는 parseMongoVersion 이 같은
// major.minor 를 읽어 낼 때만 일어나는지 본다.
//
// 반대 방향은 성립하지 않는다 — "08.0" 은 (8, 0) 으로 파싱되지만 화이트리스트
// 문자열 "8.0" 과 다르므로 거부된다(의도된 엄격함).
func FuzzIsSupportedMongoDBVersion(f *testing.F) {
	for _, seed := range []string{"8.0", "8.3.1", "9.0.2", "7.0", "", "x.y"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, v string) {
		supported := IsSupportedMongoDBVersion(v)
		major, minor, ok := parseMongoVersion(v)
		_ = IsValidUpgradePath(v, v)

		if !supported {
			return
		}
		if !ok {
			t.Fatalf("IsSupportedMongoDBVersion(%q) = true but parseMongoVersion failed", v)
		}
		if mm := formatMongoVersion(major, minor); !slices.Contains(SupportedMongoDBVersions, mm) {
			t.Fatalf("IsSupportedMongoDBVersion(%q) = true but parsed %q is not in %v", v, mm, SupportedMongoDBVersions)
		}
	})
}
