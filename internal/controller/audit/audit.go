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
	"fmt"
	"strings"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
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

// PrometheusRulesYAML 은 AuditLogSpec.AlertRules 를 PrometheusRule YAML 로
// 직렬화한다 (단순 string 빌더 — kube client 의존 없이 unit testable).
func PrometheusRulesYAML(specName string, spec *mongodbv1alpha1.AuditLogSpec) string {
	if spec == nil || len(spec.AlertRules) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\n")
	sb.WriteString(fmt.Sprintf("metadata:\n  name: %s-audit\nspec:\n  groups:\n  - name: audit\n    rules:\n", specName))
	for _, rule := range spec.AlertRules {
		severity := rule.Severity
		if severity == "" {
			severity = "warning"
		}
		threshold := rule.Threshold
		if threshold <= 0 {
			threshold = 10
		}
		sb.WriteString(fmt.Sprintf("    - alert: %s\n", rule.Name))
		sb.WriteString(fmt.Sprintf("      expr: rate(mongodb_audit_events_total{atype=%q}[5m]) > %d\n", rule.EventType, threshold))
		sb.WriteString(fmt.Sprintf("      labels:\n        severity: %s\n", severity))
		sb.WriteString(fmt.Sprintf("      annotations:\n        summary: \"%s threshold exceeded\"\n", rule.EventType))
	}
	return sb.String()
}
