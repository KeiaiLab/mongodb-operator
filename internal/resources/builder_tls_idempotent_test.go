/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// tls-pem-merge init container 의 재시작 idempotency 회귀 테스트.
// emptyDir 는 pod 내 container 재시작 간 유지되므로, init 이 재시작되면
// 이전 실행이 남긴 server.pem(0400, 쓰기불가)이 그대로 있다. 명령이
// `cat > server.pem` 으로 시작하면 0400 파일 트렁케이트가 Permission denied 로
// 실패 → CrashLoopBackOff (라이브 keiailab-mongo-mongos 구 pod 증상).
package resources

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBuildPEMMergeInitContainer_IdempotentAcrossRestart(t *testing.T) {
	inDir := t.TempDir()  // /tls-input 모사 (cert-manager secret, ro)
	pemDir := t.TempDir() // /tls-pem 모사 (emptyDir, 재시작 간 유지)
	if err := os.WriteFile(inDir+"/tls.crt", []byte("CERT\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inDir+"/tls.key", []byte("KEY\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	c := BuildPEMMergeInitContainer()
	if len(c.Command) != 3 || c.Command[0] != "sh" || c.Command[1] != "-c" {
		t.Fatalf("예상치 못한 command 형태: %v", c.Command)
	}
	script := c.Command[2]
	script = strings.ReplaceAll(script, "/tls-input", inDir)
	script = strings.ReplaceAll(script, "/tls-pem", pemDir)

	run := func() ([]byte, error) {
		return exec.Command("sh", "-c", script).CombinedOutput()
	}

	if out, err := run(); err != nil {
		t.Fatalf("첫 실행 실패: %v\n%s", err, out)
	}
	// init container 재시작: emptyDir 의 server.pem(0400) 이 잔존한 상태에서 재실행.
	if out, err := run(); err != nil {
		t.Fatalf("재시작 실행(idempotency) 실패: %v\n%s", err, out)
	}

	got, err := os.ReadFile(pemDir + "/server.pem")
	if err != nil {
		t.Fatalf("server.pem read: %v", err)
	}
	if string(got) != "CERT\nKEY\n" {
		t.Fatalf("merge 결과 불일치: %q", got)
	}
}
