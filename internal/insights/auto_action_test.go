package insights

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestPlanMissingIndexActions_DisabledSpec(t *testing.T) {
	actions := PlanMissingIndexActions(nil, nil)
	if actions != nil {
		t.Fatalf("nil spec should return nil, got %v", actions)
	}
	actions = PlanMissingIndexActions([]mongodbv1alpha1.Recommendation{{Type: "MissingIndex"}}, &mongodbv1alpha1.AutoIndexSpec{Enabled: false})
	if actions != nil {
		t.Fatalf("disabled spec should return nil, got %v", actions)
	}
}

func TestPlanMissingIndexActions_FiltersByType(t *testing.T) {
	// analyzer 는 NS 를 구조화 필드(DB/Collection)로 채운다 — joinNS 가 이를 사용.
	recs := []mongodbv1alpha1.Recommendation{
		{Type: "MissingIndex", Severity: "warning", DB: "db", Collection: "users", Detail: "create index on db.users keys: [email]"},
		{Type: "UnusedIndex", Severity: "warning", DB: "db", Collection: "users", Detail: "drop index on db.users"},
		{Type: "SlowQueryPattern", Severity: "warning", DB: "db", Collection: "orders", Detail: "slow query on db.orders"},
	}
	actions := PlanMissingIndexActions(recs, &mongodbv1alpha1.AutoIndexSpec{Enabled: true, MinSeverity: "warning"})
	if len(actions) != 1 {
		t.Fatalf("expected 1 MissingIndex action, got %d", len(actions))
	}
	if actions[0].NS != "db.users" {
		t.Errorf("expected NS=db.users, got %s", actions[0].NS)
	}
}

func TestPlanMissingIndexActions_FiltersBySeverity(t *testing.T) {
	recs := []mongodbv1alpha1.Recommendation{
		{Type: "MissingIndex", Severity: "info", Detail: "db.a keys: [x]"},
		{Type: "MissingIndex", Severity: "warning", Detail: "db.b keys: [y]"},
		{Type: "MissingIndex", Severity: "critical", Detail: "db.c keys: [z]"},
	}
	actions := PlanMissingIndexActions(recs, &mongodbv1alpha1.AutoIndexSpec{Enabled: true, MinSeverity: "warning"})
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (warning + critical), got %d", len(actions))
	}
}

func TestPlanMissingIndexActions_DryRunPropagated(t *testing.T) {
	recs := []mongodbv1alpha1.Recommendation{
		{Type: "MissingIndex", Severity: "warning", Detail: "db.users keys: [email]"},
	}
	actions := PlanMissingIndexActions(recs, &mongodbv1alpha1.AutoIndexSpec{Enabled: true, MinSeverity: "warning", DryRun: true})
	if len(actions) != 1 || !actions[0].DryRun {
		t.Fatalf("DryRun not propagated: %+v", actions)
	}
}

func TestPlanSlowQueryHints_DisabledSpec(t *testing.T) {
	if r := PlanSlowQueryHints(nil, nil); r != nil {
		t.Fatalf("nil spec must return nil, got %v", r)
	}
}

func TestPlanSlowQueryHints_FiltersByLatency(t *testing.T) {
	recs := []mongodbv1alpha1.Recommendation{
		{Type: "SlowQueryPattern", AvgLatencyMs: 500, Detail: "db.fast"},
		{Type: "SlowQueryPattern", AvgLatencyMs: 1500, Detail: "db.slow"},
	}
	actions := PlanSlowQueryHints(recs, &mongodbv1alpha1.AutoQueryHintSpec{Enabled: true, SlowQueryThresholdMs: 1000})
	if len(actions) != 1 {
		t.Fatalf("expected 1 slow query action (>=1000ms), got %d", len(actions))
	}
}

func TestMeetsSeverity(t *testing.T) {
	cases := []struct {
		got, min string
		want     bool
	}{
		{"info", "info", true},
		{"warning", "info", true},
		{"critical", "info", true},
		{"info", "warning", false},
		{"warning", "warning", true},
		{"critical", "warning", true},
		{"info", "critical", false},
		{"warning", "critical", false},
		{"critical", "critical", true},
		{"bogus", "warning", false},
	}
	for _, c := range cases {
		if got := meetsSeverity(c.got, c.min); got != c.want {
			t.Errorf("meetsSeverity(%s,%s) = %v, want %v", c.got, c.min, got, c.want)
		}
	}
}

func TestJoinNS(t *testing.T) {
	cases := []struct {
		db   string
		coll string
		want string
	}{
		{"db", "users", "db.users"},
		{"mydb", "orders.items", "mydb.orders.items"}, // collection 에 점이 있어도 정확
		{"", "users", ""},                             // db 누락 → schema-level hint
		{"db", "", ""},                                // collection 누락
		{"", "", ""},
	}
	for _, c := range cases {
		if got := joinNS(c.db, c.coll); got != c.want {
			t.Errorf("joinNS(%q,%q) = %q, want %q", c.db, c.coll, got, c.want)
		}
	}
}
