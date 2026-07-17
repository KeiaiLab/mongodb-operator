/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// scripts_test.go — embed.FS 외부화 후의 회귀 보호.
//
// 본 test는 RenderXxx의 출력에 *반드시 포함되어야 하는 토큰*을 검증해
// 향후 누군가 .sh.tpl 파일을 잘못 편집하면 즉시 fail한다. 본질적으로
// integration test가 라이브 클러스터에서 수행하는 *셰이크 다운*의 unit
// 보호막 역할.

package assets

import (
	"strings"
	"testing"
)

// codeOnly는 셸 스크립트에서 주석 줄을 걷어낸 *실행되는 부분*만 돌려준다.
// "이 플래그가 나오면 안 된다" 류의 부정 assert는 반드시 이걸 통과시켜야 한다 —
// 헤더 주석이 계약을 설명하며 그 플래그 이름을 언급하는 것은 정상이고,
// 원문 그대로 부정 검사하면 주석 문구에 걸려 오탐이 난다.
// (줄 첫 비공백이 '#'인 줄만 제거하므로 `10#${x}` 같은 코드 내 '#'은 남는다.)
func codeOnly(script string) string {
	var b strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func TestRenderReadiness_PortInjection(t *testing.T) {
	got, err := RenderReadiness(27017)
	if err != nil {
		t.Fatalf("RenderReadiness err= %v", err)
	}
	expected := []string{
		"#!/bin/bash",
		"set -e",
		"--port 27017",
		"db.adminCommand('ping')",
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("readiness 스크립트에 %q 누락:\n%s", want, got)
		}
	}
}

func TestRenderBootstrap_ContainsCriticalLogic(t *testing.T) {
	got, err := RenderBootstrap(27017)
	if err != nil {
		t.Fatalf("RenderBootstrap err= %v", err)
	}
	// 부트스트랩 핵심 로직이 모두 보존되었는지 회귀 보호.
	expected := []string{
		"#!/bin/bash",
		"set -eu",
		`PORT="${MONGO_PORT:-27017}"`,
		`ORDINAL="${HOSTNAME##*-}"`,
		`if [ "$ORDINAL" != "0" ]`,
		"rs.status().ok",
		"e.code===94", // NotYetInitialized
		"rs.initiate(cfg)",
		"isWritablePrimary",
		"db.createUser",
		"already exists, idempotent skip",
		"e.code === 11000", // DuplicateKey
		"e.code === 51003", // UserAlreadyExists
		"bootstrap complete",
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("bootstrap 스크립트에 %q 누락 — RS init 로직 회귀 위험", want)
		}
	}
}

func TestRenderBackup_S3Variant(t *testing.T) {
	got, err := RenderBackup("s3", "rs-test", "--gzip", true)
	if err != nil {
		t.Fatalf("RenderBackup s3 err= %v", err)
	}
	expected := []string{
		// 백업 이름은 이제 env — 컨테이너 안에서 $(date)로 짓지 않는다.
		// 그래야 operator/restore가 S3 키를 결정론적으로 계산할 수 있다.
		`: "${BACKUP_NAME:?`,
		// aws CLI v2 zip 설치 — Ubuntu Noble 에서 apt awscli(v1) 는 제거돼
		// "no installation candidate" 로 실패하므로 공식 zip 을 쓴다.
		`awscli-exe-linux-${_awsarch}.zip`,
		`mongodump --uri="${MONGODB_URI}" --gzip --oplog --archive`,
		// 키 계약: <prefix>/<backup>/base.archive.gz + base.meta.json
		`base.archive.gz`,
		`base.meta.json`,
		// 파이프 무결성 — 없으면 mongodump 실패해도 aws의 exit이 이겨
		// 잘린 아카이브를 올리고 성공을 보고한다.
		`set -euo pipefail`,
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("backup-s3에 %q 누락:\n%s", want, got)
		}
	}
	if strings.Contains(codeOnly(got), "/backup/${BACKUP_NAME}") {
		t.Error("backup-s3에 PVC 경로(/backup/) 잘못 포함됨")
	}
	// 구 구현 회귀 가드: 런타임 $(date)로 이름을 지으면 키를 아무도 모른다.
	if strings.Contains(got, `BACKUP_NAME="rs-test-$(date`) {
		t.Error("backup-s3가 컨테이너 안에서 BACKUP_NAME을 짓고 있다 — 복원 불가 회귀")
	}
	// S3_ENDPOINT는 optional(실 AWS면 빈 값) — 빈 값에 --endpoint-url=를
	// 무조건 붙이면 aws가 Invalid endpoint로 죽는다.
	if strings.Contains(got, `--endpoint-url="${S3_ENDPOINT}"`) {
		t.Error("S3_ENDPOINT를 무조건부로 붙이고 있다 — 빈 값이면 aws가 죽는다")
	}
}

// TestRenderBackup_S3NoOplog 는 sharded(mongos 접속)처럼 --oplog를 쓸 수 없는
// 경우를 가드한다. mongos에는 local.oplog.rs가 없어 mongodump가 거부하므로
// 플래그도, 접합 메타 업로드도 나오면 안 된다.
func TestRenderBackup_S3NoOplog(t *testing.T) {
	got, err := RenderBackup("s3", "sh-test", "--gzip", false)
	if err != nil {
		t.Fatalf("RenderBackup s3 err= %v", err)
	}
	code := codeOnly(got)
	if strings.Contains(code, "--oplog") {
		t.Error("withOplog=false인데 --oplog가 붙었다 — sharded 백업이 깨진다")
	}
	if strings.Contains(code, "base.meta.json") {
		t.Error("withOplog=false인데 base.meta.json을 올린다 — meta 존재는 " +
			"'이 base는 --oplog로 떠졌다'는 신호라 거짓말이 된다")
	}
	if !strings.Contains(code, `mongodump --uri="${MONGODB_URI}" --gzip --archive`) {
		t.Errorf("base dump 명령이 누락/변형됨:\n%s", got)
	}
	// oplog head 샘플링도 통째로 빠져야 한다 (mongos에는 local.oplog.rs가 없다).
	if strings.Contains(code, "oplog.rs") {
		t.Error("withOplog=false인데 local.oplog.rs를 조회한다 — mongos에서 실패한다")
	}
}

func TestRenderBackup_PVCVariant(t *testing.T) {
	got, err := RenderBackup("pvc", "rs-test", "--gzip", true)
	if err != nil {
		t.Fatalf("RenderBackup pvc err= %v", err)
	}
	expected := []string{
		`: "${BACKUP_NAME:?`,
		`mongodump --uri="${MONGODB_URI}" --out="/backup/${BACKUP_NAME}" --gzip --oplog`,
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("backup-pvc에 %q 누락:\n%s", want, got)
		}
	}
	if strings.Contains(codeOnly(got), "aws s3") {
		t.Error("backup-pvc에 S3 명령 잘못 포함됨")
	}
}

// TestRenderBackup_CompressionFlagInjection 은 compressionFlag가 그대로
// 주입되는지만 본다(렌더러의 책임). 어떤 플래그를 고를지는 호출자
// (resources.buildBackupScript) 몫이며, mongodump가 gzip 코덱만 지원하므로
// 실제로는 항상 --gzip이 온다.
func TestRenderBackup_CompressionFlagInjection(t *testing.T) {
	got, err := RenderBackup("pvc", "rs-test", "--gzip", false)
	if err != nil {
		t.Fatalf("RenderBackup err= %v", err)
	}
	if !strings.Contains(got, `--out="/backup/${BACKUP_NAME}" --gzip`) {
		t.Errorf("compression flag 주입 실패:\n%s", got)
	}
}

// TestRenderRestoreFetch 는 init container 스크립트의 *계약*을 가드한다.
// 세그먼트 키 형식과 gap/도달 검사가 빠지면 조용히 구멍 뚫린 복원이 된다.
func TestRenderRestoreFetch(t *testing.T) {
	got, err := RenderRestoreFetch("/data/source")
	if err != nil {
		t.Fatalf("RenderRestoreFetch err= %v", err)
	}
	expected := []string{
		`SRC="/data/source"`,
		// oplog-stream.sh.tpl 과 공유하는 키 계약 (%010d-%010d_%010d-%010d).
		`^[0-9]{10}-[0-9]{10}_[0-9]{10}-[0-9]{10}\.bson\.gz$`,
		`printf '%010d-%010d'`,
		// 사전식 정렬 == 시간순 — 로케일 collation에 흔들리면 안 된다.
		`export LC_ALL=C`,
		`base.archive.gz`,
		`base.meta.json`,
		// 세 가지 무결성 게이트.
		"oplog 체인에 gap",
		"base 이후에서 시작",
		"닿지 못한다",
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("restore-fetch에 %q 누락:\n%s", want, got)
		}
	}
}

// TestRenderRestoreReplay 는 main 컨테이너 스크립트의 계약을 가드한다.
func TestRenderRestoreReplay(t *testing.T) {
	got, err := RenderRestoreReplay("/data/source")
	if err != nil {
		t.Fatalf("RenderRestoreReplay err= %v", err)
	}
	expected := []string{
		`SRC="/data/source"`,
		`--oplogReplay`,
		`--oplogLimit="${OPLOG_LIMIT}"`,
		`--dir "${SRC}/oplog"`,
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("restore-replay에 %q 누락:\n%s", want, got)
		}
	}
	// 구 구현 회귀 가드 2종.
	if strings.Contains(codeOnly(got), "dump.archive") {
		t.Error("restore가 존재한 적 없는 /data/source/dump.archive를 읽고 있다")
	}
	if strings.Contains(codeOnly(got), "date -u -d") {
		t.Error("oplogLimit을 bash date로 계산하고 있다 — Go(OplogLimitArg) 책임")
	}
}
