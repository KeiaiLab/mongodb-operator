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
	got, err := RenderBackup("s3", "rs-test", "--gzip")
	if err != nil {
		t.Fatalf("RenderBackup s3 err= %v", err)
	}
	expected := []string{
		`BACKUP_NAME="rs-test-`,
		`apt-get install -y awscli`,
		`mongodump --uri="${MONGODB_URI}" --gzip --archive`,
		`aws s3 cp -`,
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("backup-s3에 %q 누락:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/backup/${BACKUP_NAME}") {
		t.Error("backup-s3에 PVC 경로(/backup/) 잘못 포함됨")
	}
}

func TestRenderBackup_PVCVariant(t *testing.T) {
	got, err := RenderBackup("pvc", "rs-test", "--gzip")
	if err != nil {
		t.Fatalf("RenderBackup pvc err= %v", err)
	}
	expected := []string{
		`mongodump --uri="${MONGODB_URI}" --out="/backup/${BACKUP_NAME}" --gzip`,
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("backup-pvc에 %q 누락:\n%s", want, got)
		}
	}
	if strings.Contains(got, "aws s3 cp") {
		t.Error("backup-pvc에 S3 명령 잘못 포함됨")
	}
}

func TestRenderBackup_ZstdCompression(t *testing.T) {
	got, _ := RenderBackup("s3", "rs-test", "--archive")
	if !strings.Contains(got, "--archive --archive") {
		// "--gzip" 자리에 "--archive"가 들어간 결과
		// (zstd compressionFlag 옵션이 정확히 전달됐는지)
		t.Errorf("zstd compression flag 주입 실패")
	}
}
