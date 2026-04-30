/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
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

// backupData는 backup-{s3,pvc}.sh.tpl 렌더 컨텍스트.
type backupData struct {
	ClusterName     string
	CompressionFlag string
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

// RenderBackup는 mongodump → S3 또는 PVC 분기로 백업 스크립트를 반환.
// storageType은 "s3" 또는 "pvc". compressionFlag는 "--gzip" / "--archive" 등.
func RenderBackup(storageType, clusterName, compressionFlag string) (string, error) {
	tpl := "scripts/backup-pvc.sh.tpl"
	if storageType == "s3" {
		tpl = "scripts/backup-s3.sh.tpl"
	}
	return render(tpl, backupData{ClusterName: clusterName, CompressionFlag: compressionFlag})
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
