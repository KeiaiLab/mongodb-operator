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

// TestMongoshArgsNoAuth_NeverContainsCredentials는 mongosh 명령행 인자에
// 자격증명이 절대 포함되지 않음을 단정한다. 이 보장이 깨지면 Kubernetes
// audit log와 컨테이너 내 `ps` 출력에 평문 password가 노출된다.
func TestMongoshArgsNoAuth_NeverContainsCredentials(t *testing.T) {
	args := mongoshArgsNoAuth(27017)

	forbidden := []string{"-u", "-p", "--username", "--password", "--authenticationDatabase"}
	for _, a := range args {
		for _, f := range forbidden {
			if a == f {
				t.Fatalf("mongosh args에 자격증명 플래그 %q 포함됨: %v", f, args)
			}
		}
	}
}

// TestBuildAuthScript_EscapesAllCredentials는 자격증명에 인젝션 페이로드가
// 들어와도 buildAuthScript가 안전한 JS literal로 인코딩하는지 검증한다.
//
// 검증 전략: 첫 줄(auth statement)에서 모든 string literal 영역을 제거한
// '코드 영역'만 추출한 뒤, 그 코드 영역이 정확히 한 개의 getSiblingDB+auth
// 호출 패턴인지 확인. payload 텍스트가 string literal 안에 들어있어도
// 코드 영역에는 영향이 없다.
func TestBuildAuthScript_EscapesAllCredentials(t *testing.T) {
	cases := []struct {
		name     string
		authDB   string
		user     string
		password string
	}{
		{"normal", "admin", "root", "secret123"},
		{"password injection", "admin", "root", `s'); db.dropDatabase(); ('`},
		{"username injection", "admin", `r"; db.shutdownServer(); ("`, "pw"},
		{"authdb injection", `admin"); db.dropDatabase(); db.getSiblingDB("`, "u", "p"},
		{"backslash payload", "admin", "u", `\"; db.shutdown(); \"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := buildAuthScript(tc.authDB, tc.user, tc.password, "db.adminCommand('ping')")
			firstLine := strings.SplitN(script, "\n", 2)[0]
			codeOnly := stripJSStringLiterals(firstLine)

			// 코드 영역(string literal 제외)은 정확히
			// `db.getSiblingDB().auth(, );` 형태여야 함.
			expected := "db.getSiblingDB().auth(, );"
			if codeOnly != expected {
				t.Fatalf("코드 영역 = %q\n기대: %q\nfullScript:\n%s", codeOnly, expected, script)
			}
		})
	}
}

// stripJSStringLiterals는 JS 코드에서 큰따옴표 string literal 내용을 제거한다.
// 즉 `db.foo("bar")`를 `db.foo()`로 만든다. 백슬래시 escape를 인식한다.
// 이는 테스트 전용 단순 파서이며, JSON 출력만 다룬다 (작은따옴표/template literal 미지원).
func stripJSStringLiterals(s string) string {
	var out strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' && i+1 < len(s) {
				i++ // skip escaped char
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}
