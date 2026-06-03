/*
Copyright 2026 Keiailab.
*/

// audit.go — F61-F65 (cycle 8) MongoDB Enterprise audit log helper.
//
// 본 cycle acceptance: mongod audit args 생성 + alert rule 변환 + fluent-bit
// forwarder config 생성 helper. 실제 fluent-bit sidecar inject + PrometheusRule
// CR 생성 은 cycle 9 운영 강화.

package audit

import (
	"encoding/json"
	"fmt"
	"strings"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	// AuditLogMountPath — mongod 가 audit log 파일을 쓰는 경로 (file destination 시).
	AuditLogMountPath = "/var/log/mongodb-audit"
	// AuditLogFileName — destination=file 시 파일명.
	AuditLogFileName = "audit.json"

	destinationFile = "file"
)

// MongodArgs 는 AuditLogSpec 으로부터 mongod CLI args 를 생성.
//
// MongoDB Enterprise 의 audit 옵션:
//
//	--auditDestination file|syslog|console
//	--auditFormat JSON|BSON
//	--auditPath /var/log/mongodb-audit/audit.json   (file destination only)
//	--auditFilter '<json>'
func MongodArgs(spec *mongodbv1alpha1.AuditLogSpec) []string {
	if spec == nil || !spec.Enabled {
		return nil
	}
	dest := spec.Destination
	if dest == "" {
		dest = destinationFile
	}
	format := spec.Format
	if format == "" {
		format = "JSON"
	}
	args := []string{
		fmt.Sprintf("--auditDestination=%s", dest),
		fmt.Sprintf("--auditFormat=%s", format),
	}
	if dest == destinationFile {
		args = append(args, fmt.Sprintf("--auditPath=%s/%s", AuditLogMountPath, AuditLogFileName))
	}
	if strings.TrimSpace(spec.FilterJSON) != "" {
		args = append(args, fmt.Sprintf("--auditFilter=%s", spec.FilterJSON))
	}
	return args
}

// ValidateAuditLogSpec — webhook validation.
func ValidateAuditLogSpec(spec *mongodbv1alpha1.AuditLogSpec) error {
	if spec == nil || !spec.Enabled {
		return nil
	}
	switch spec.Destination {
	case "", destinationFile, "syslog", "console":
	default:
		return fmt.Errorf("auditLog.destination invalid: %q", spec.Destination)
	}
	if fwd := spec.CentralForwarder; fwd != nil {
		switch fwd.Type {
		case "loki", "elasticsearch", "opensearch":
		default:
			return fmt.Errorf("auditLog.centralForwarder.type invalid: %q", fwd.Type)
		}
		if !strings.HasPrefix(fwd.URL, "http://") && !strings.HasPrefix(fwd.URL, "https://") {
			return fmt.Errorf("auditLog.centralForwarder.url must be http(s)://")
		}
	}
	for i, rule := range spec.AlertRules {
		if rule.Name == "" {
			return fmt.Errorf("auditLog.alertRules[%d].name is required", i)
		}
		if rule.EventType == "" {
			return fmt.Errorf("auditLog.alertRules[%d].eventType is required", i)
		}
		switch rule.Severity {
		case "", "info", "warning", "critical":
		default:
			return fmt.Errorf("auditLog.alertRules[%d].severity invalid: %q", i, rule.Severity)
		}
	}
	return nil
}

// yamlScalar 는 사용자 제어 문자열을 YAML 구조를 깨지 않는 *단일 라인* scalar
// 로 인코딩한다. 안전한 값(`auth-spike` 등)은 그대로, 위험한 값(개행 / `: ` /
// 선행 indicator 문자 등)은 자동으로 quoting 된다.
//
// sigs.k8s.io/yaml.Marshal 은 multi-line 문자열을 block scalar(`|-`)로 내보내
// inline 삽입에 부적합하므로, 결과에 개행이 남으면 JSON double-quote(= 유효한
// YAML scalar)로 single-line 을 강제한다.
func yamlScalar(s string) string {
	b, err := sigsyaml.Marshal(s)
	if err != nil {
		jb, _ := json.Marshal(s) // JSON 문자열은 유효한 YAML scalar
		return string(jb)
	}
	out := strings.TrimRight(string(b), "\n")
	if strings.ContainsRune(out, '\n') {
		jb, _ := json.Marshal(s)
		return string(jb)
	}
	return out
}

// PrometheusRulesYAML 은 AuditLogSpec.AlertRules 를 PrometheusRule YAML 로
// 직렬화한다 (단순 string 빌더 — kube client 의존 없이 unit testable).
//
// 사용자 제어 CRD 필드(specName / rule.Name / severity / rule.EventType)는 모두
// yamlScalar 로 인코딩하여 YAML 구조 파괴/주입을 차단한다. 라인 끝 또는 suffix·
// 접미 문자열과 결합되는 값은 결합된 완성 문자열을 한 번에 인코딩한다.
func PrometheusRulesYAML(specName string, spec *mongodbv1alpha1.AuditLogSpec) string {
	if spec == nil || len(spec.AlertRules) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\n")
	// `-audit` suffix 와 결합된 완성 name 을 단일 scalar 로 인코딩.
	fmt.Fprintf(&sb, "metadata:\n  name: %s\nspec:\n  groups:\n  - name: audit\n    rules:\n", yamlScalar(specName+"-audit"))
	for _, rule := range spec.AlertRules {
		severity := rule.Severity
		if severity == "" {
			severity = "warning"
		}
		threshold := rule.Threshold
		if threshold <= 0 {
			threshold = 10
		}
		fmt.Fprintf(&sb, "    - alert: %s\n", yamlScalar(rule.Name))
		// expr: PromQL label 은 %q 로 escape(개행/제어문자 무력화). 전체 라인은 plain
		// scalar 로 유지 — 사용자 제어값은 EventType 뿐이며 %q 가 처리한다.
		fmt.Fprintf(&sb, "      expr: rate(mongodb_audit_events_total{atype=%q}[5m]) > %d\n", rule.EventType, threshold)
		fmt.Fprintf(&sb, "      labels:\n        severity: %s\n", yamlScalar(severity))
		// summary: EventType 와 접미 문자열을 결합한 완성 문자열을 단일 scalar 로 인코딩.
		fmt.Fprintf(&sb, "      annotations:\n        summary: %s\n", yamlScalar(rule.EventType+" threshold exceeded"))
	}
	return sb.String()
}
