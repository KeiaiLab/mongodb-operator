/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package mongodb

import (
	"strings"
	"testing"
)

// TestJsStringEscapesInjection은 mongosh --eval로 전달되는 사용자 제어 문자열에
// JS 코드 인젝션 페이로드가 들어와도 jsString이 안전한 string literal로 인코딩하는지 검증한다.
//
// 검증 방법: jsString 결과가 항상 "..." 형태이며,
// (1) 닫는 따옴표가 escape 없이 등장하지 않고,
// (2) 페이로드의 위험 토큰(); db.dropDatabase()` 같은 것)이 코드 영역으로 빠져나가지 않는다.
func TestJsStringEscapesInjection(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"single quote", `o'brien`},
		{"double quote", `say "hi"`},
		{"backslash", `c:\windows`},
		{"newline", "line1\nline2"},
		{"createUser injection", `admin'}); db.getSiblingDB('admin').dropDatabase(); db.getSiblingDB('test').createUser({user:'`},
		{"command terminator", `'); rs.add('evil:27017'); ('`},
		{"backtick template", "evil`}); process.exit(1); `"},
		{"unicode control", "\x00\x01\x02"},
		{"empty", ""},
		{"normal ascii", "admin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := jsString(tc.payload)

			// 출력은 반드시 큰따옴표로 감싸여야 한다 (JSON literal 형식).
			if !strings.HasPrefix(out, `"`) || !strings.HasSuffix(out, `"`) {
				t.Fatalf("jsString(%q) = %q: 큰따옴표로 감싸지지 않음", tc.payload, out)
			}

			// 가운데 부분(따옴표 제외)에 escape되지 않은 닫는 따옴표가 있으면 인젝션 가능.
			inner := out[1 : len(out)-1]
			for i := 0; i < len(inner); i++ {
				if inner[i] != '"' {
					continue
				}
				// 직전 문자가 백슬래시여야 함. 백슬래시 자체도 escape 검증.
				if i == 0 || !isEscaped(inner, i) {
					t.Fatalf("jsString(%q) = %q: escape되지 않은 따옴표 발견", tc.payload, out)
				}
			}

			// 페이로드의 newline/control char가 raw로 남아있으면 안 됨.
			if strings.ContainsAny(inner, "\n\r\x00") {
				t.Fatalf("jsString(%q) = %q: 제어문자가 escape되지 않음", tc.payload, out)
			}
		})
	}
}

// isEscaped는 문자열 s의 위치 i가 짝수 개의 백슬래시 뒤에 오는지 확인한다.
// 짝수 개면 escape 안 됨, 홀수 개면 escape 됨.
func isEscaped(s string, i int) bool {
	count := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		count++
	}
	return count%2 == 1
}
