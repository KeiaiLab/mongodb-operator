/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package mongodb

import "encoding/json"

// jsString은 임의의 문자열을 JS/JSON에서 안전한 string literal로 인코딩한다.
// JSON은 ECMAScript의 부분집합이므로 json.Marshal 결과는 동시에 유효한 JS literal이며,
// 모든 특수문자(따옴표, 백슬래시, 제어문자 등)가 적절히 escape된다.
//
// 이 함수는 mongosh --eval로 전달되는 사용자 제어 문자열(username, password,
// host, database 이름 등)을 fmt.Sprintf("'%s'", v) 패턴 대신 사용해 JS 코드
// 인젝션을 차단하기 위한 것이다.
func jsString(s string) string {
	// json.Marshal은 string에 대해 절대 실패하지 않는다.
	b, _ := json.Marshal(s)
	return string(b)
}
