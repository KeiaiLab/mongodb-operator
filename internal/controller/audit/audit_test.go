/*
Copyright 2026 Keiailab.
*/

package audit

import (
	"strings"
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestMongodArgs_DisabledOrNil(t *testing.T) {
	t.Parallel()
	if got := MongodArgs(nil); got != nil {
		t.Errorf("nil: got %v want nil", got)
	}
	if got := MongodArgs(&mongodbv1alpha1.AuditLogSpec{Enabled: false}); got != nil {
		t.Errorf("disabled: got %v want nil", got)
	}
}

func TestMongodArgs_FileDestinationDefault(t *testing.T) {
	t.Parallel()
	args := MongodArgs(&mongodbv1alpha1.AuditLogSpec{Enabled: true})
	joined := strings.Join(args, " ")
	for _, w := range []string{"--auditDestination=file", "--auditFormat=JSON", "/var/log/mongodb-audit/audit.json"} {
		if !strings.Contains(joined, w) {
			t.Errorf("must contain %q, got: %v", w, args)
		}
	}
}

func TestMongodArgs_SyslogNoPath(t *testing.T) {
	t.Parallel()
	args := MongodArgs(&mongodbv1alpha1.AuditLogSpec{Enabled: true, Destination: "syslog"})
	for _, a := range args {
		if strings.HasPrefix(a, "--auditPath") {
			t.Errorf("syslog destination must not include auditPath, got: %v", args)
		}
	}
}

func TestMongodArgs_Filter(t *testing.T) {
	t.Parallel()
	filter := `{"atype":{"$in":["authenticate","createUser"]}}`
	args := MongodArgs(&mongodbv1alpha1.AuditLogSpec{Enabled: true, FilterJSON: filter})
	if !strings.Contains(strings.Join(args, " "), filter) {
		t.Errorf("filter must propagate, got: %v", args)
	}
}

func TestValidateAuditLogSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    *mongodbv1alpha1.AuditLogSpec
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"disabled ok", &mongodbv1alpha1.AuditLogSpec{Enabled: false}, false},
		{"default ok", &mongodbv1alpha1.AuditLogSpec{Enabled: true}, false},
		{"bad destination reject", &mongodbv1alpha1.AuditLogSpec{Enabled: true, Destination: "kafka"}, true},
		{"forwarder bad type reject", &mongodbv1alpha1.AuditLogSpec{
			Enabled:          true,
			CentralForwarder: &mongodbv1alpha1.AuditForwarderSpec{Type: "splunk", URL: "https://x"},
		}, true},
		{"forwarder bad URL reject", &mongodbv1alpha1.AuditLogSpec{
			Enabled:          true,
			CentralForwarder: &mongodbv1alpha1.AuditForwarderSpec{Type: "loki", URL: "x"},
		}, true},
		{"forwarder ok", &mongodbv1alpha1.AuditLogSpec{
			Enabled:          true,
			CentralForwarder: &mongodbv1alpha1.AuditForwarderSpec{Type: "loki", URL: "https://loki.example.com:3100"},
		}, false},
		{"alert rule empty name reject", &mongodbv1alpha1.AuditLogSpec{
			Enabled: true,
			AlertRules: []mongodbv1alpha1.AuditAlertRule{
				{Name: "", EventType: "authenticate"},
			},
		}, true},
		{"alert rule empty event reject", &mongodbv1alpha1.AuditLogSpec{
			Enabled: true,
			AlertRules: []mongodbv1alpha1.AuditAlertRule{
				{Name: "auth-spike", EventType: ""},
			},
		}, true},
		{"alert rule bad severity reject", &mongodbv1alpha1.AuditLogSpec{
			Enabled: true,
			AlertRules: []mongodbv1alpha1.AuditAlertRule{
				{Name: "auth-spike", EventType: "authenticate", Severity: "emergency"},
			},
		}, true},
		{"alert rule ok", &mongodbv1alpha1.AuditLogSpec{
			Enabled: true,
			AlertRules: []mongodbv1alpha1.AuditAlertRule{
				{Name: "auth-spike", EventType: "authenticate", Severity: "warning", Threshold: 50},
			},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAuditLogSpec(tc.spec)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("ValidateAuditLogSpec err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestPrometheusRulesYAML(t *testing.T) {
	t.Parallel()
	yaml := PrometheusRulesYAML("test-cluster", &mongodbv1alpha1.AuditLogSpec{
		Enabled: true,
		AlertRules: []mongodbv1alpha1.AuditAlertRule{
			{Name: "auth-spike", EventType: "authenticate", Severity: "warning", Threshold: 50},
			{Name: "user-drop", EventType: "dropUser", Severity: "critical"},
		},
	})
	for _, w := range []string{
		"apiVersion: monitoring.coreos.com/v1",
		"kind: PrometheusRule",
		"name: test-cluster-audit",
		"alert: auth-spike",
		"alert: user-drop",
		`atype="authenticate"`,
		`atype="dropUser"`,
		"severity: warning",
		"severity: critical",
		"> 50",
		"> 10",
	} {
		if !strings.Contains(yaml, w) {
			t.Errorf("PrometheusRulesYAML missing %q, got:\n%s", w, yaml)
		}
	}
}

func TestPrometheusRulesYAML_Empty(t *testing.T) {
	t.Parallel()
	if got := PrometheusRulesYAML("x", nil); got != "" {
		t.Errorf("nil spec must return empty, got %q", got)
	}
	if got := PrometheusRulesYAML("x", &mongodbv1alpha1.AuditLogSpec{Enabled: true}); got != "" {
		t.Errorf("no rules must return empty, got %q", got)
	}
}
