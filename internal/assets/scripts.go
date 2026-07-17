/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// Package assets — embed.FS로 외부화된 bash + JS 스크립트 템플릿 렌더러.
//
// builder.go의 multiline string literal에 박혀 있던 스크립트 3종을 분리하여:
//  1. IDE syntax highlight 가능 (.sh.tpl 확장자)
//  2. shellcheck/lint 가능
//  3. golden file test로 byte-for-byte 동일성 보장
//
// text/template `{{.Field}}` placeholder로 변수 주입. fmt.Sprintf 대비 type-safe.
package assets

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed scripts/*.tpl
var scriptFS embed.FS

// readinessData는 readiness.sh.tpl 렌더 컨텍스트.
type readinessData struct{ Port int }

// bootstrapData는 bootstrap-admin.sh.tpl 렌더 컨텍스트.
type bootstrapData struct{ Port int }

// stepDownData는 prestop-stepdown.sh.tpl 렌더 컨텍스트.
type stepDownData struct{ Port int }

// backupData는 backup-{s3,pvc}.sh.tpl 렌더 컨텍스트.
type backupData struct {
	ClusterName     string
	CompressionFlag string
	// WithOplog는 mongodump에 --oplog를 붙일지 여부. dump 중 들어온 write까지
	// 캡처해 *시점 일관* 스냅샷을 만든다(PITR의 기점). ReplicaSet 멤버 접속에서만
	// 유효 — mongos(sharded)에 붙이면 mongodump가 거부하므로 호출자가 판정한다.
	WithOplog bool
}

// restoreData는 restore-{fetch,replay}.sh.tpl 렌더 컨텍스트.
type restoreData struct {
	// SourceDir는 base 아카이브/덤프 + oplog.bson이 놓이는 경로. init container와
	// main 컨테이너가 같은 경로로 마운트한다.
	SourceDir string
}

// RenderReadiness는 mongod 헬스체크 스크립트를 반환.
// port는 RS=27017 / cfg=27019 / shard=27018.
func RenderReadiness(port int) (string, error) {
	return render("scripts/readiness.sh.tpl", readinessData{Port: port})
}

// RenderBootstrap는 lifecycle.postStart에서 RS init + admin user 부트스트랩
// 스크립트를 반환. localhost-exception을 활용해 첫 user 생성 deadlock을 회피.
func RenderBootstrap(port int) (string, error) {
	return render("scripts/bootstrap-admin.sh.tpl", bootstrapData{Port: port})
}

// RenderStepDown은 lifecycle.preStop에서 PRIMARY면 rs.stepDown()으로 primary를
// 이양하는 스크립트를 반환(무중단 업그레이드). PRIMARY가 아니면 no-op. 모든 에러 무시.
// port는 RS=27017 / cfg=27019 / shard=27018.
func RenderStepDown(port int) (string, error) {
	return render("scripts/prestop-stepdown.sh.tpl", stepDownData{Port: port})
}

// RenderBackup는 mongodump → S3 또는 PVC 분기로 백업 스크립트를 반환.
// storageType은 "s3" 또는 "pvc". compressionFlag는 "--gzip" / "--archive" 등.
// withOplog는 --oplog(시점 일관 스냅샷 = PITR 기점) 부착 여부 — ReplicaSet 전용.
//
// 두 변형 모두 백업 이름을 BACKUP_NAME env로 받는다(operator가 MongoDBBackup CR
// 이름을 주입). 구 구현은 컨테이너 안에서 $(date)로 지어 operator도 restore도
// 실제 키를 알 수 없었다.
func RenderBackup(storageType, clusterName, compressionFlag string, withOplog bool) (string, error) {
	tpl := "scripts/backup-pvc.sh.tpl"
	if storageType == "s3" {
		tpl = "scripts/backup-s3.sh.tpl"
	}
	return render(tpl, backupData{
		ClusterName:     clusterName,
		CompressionFlag: compressionFlag,
		WithOplog:       withOplog,
	})
}

// RenderRestoreFetch는 restore Job의 init container 스크립트를 반환(S3 전용).
// base 아카이브 + base.meta.json + PITR oplog 세그먼트를 sourceDir에 펼친다.
// 세그먼트 선택/gap 판정 계약은 템플릿 헤더 주석 참조.
func RenderRestoreFetch(sourceDir string) (string, error) {
	return render("scripts/restore-fetch.sh.tpl", restoreData{SourceDir: sourceDir})
}

// RenderRestoreReplay는 restore Job의 main 컨테이너 스크립트를 반환.
// base(임베드 oplog 포함) 복원 후 oplog.bson이 있으면 --oplogLimit까지 replay.
func RenderRestoreReplay(sourceDir string) (string, error) {
	return render("scripts/restore-replay.sh.tpl", restoreData{SourceDir: sourceDir})
}

// render는 embed.FS 안의 template 파일을 data로 실행해 결과 string 반환.
func render(path string, data interface{}) (string, error) {
	raw, err := scriptFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read embed asset %s: %w", path, err)
	}
	tpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %s: %w", path, err)
	}
	return buf.String(), nil
}
