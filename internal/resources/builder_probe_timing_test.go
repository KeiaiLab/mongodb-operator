/*
Copyright 2026 Keiailab.
*/

// builder_probe_timing_test.go — mongod exec 프로브 시간축 회귀 가드.
//
// 왜 이 가드가 있나: exec 프로브는 mongosh(Node.js 런타임)를 띄운다. 노드의 컨테이너 exec
// 경로가 잠깐 막히면 프로브만 느려지는데, 창이 좁으면 kubelet 이 그걸 mongod 장애로 오독해
// 건강한 DB 를 SIGKILL 한다 — 라이브 실측 2026-08-25: exec 타임아웃 10,007건, 파드 1본당
// 재시작 148회. 창을 다시 좁히면 같은 사고가 재발하므로 수치를 못박는다.

package resources

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// probeSpec — 한 컨테이너의 프로브 한 쌍을 검사할 때 쓰는 기대치.
type probeSpec struct {
	periodSeconds    int32
	timeoutSeconds   int32
	failureThreshold int32 // 0 = 검사 안 함(서버 기본값 유지)
}

func assertProbe(t *testing.T, label string, got *corev1.Probe, want probeSpec) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: 프로브가 없다", label)
	}

	if got.PeriodSeconds != want.periodSeconds {
		t.Errorf("%s: PeriodSeconds = %d, 기대 %d", label, got.PeriodSeconds, want.periodSeconds)
	}
	if got.TimeoutSeconds != want.timeoutSeconds {
		t.Errorf("%s: TimeoutSeconds = %d, 기대 %d", label, got.TimeoutSeconds, want.timeoutSeconds)
	}
	if want.failureThreshold != 0 && got.FailureThreshold != want.failureThreshold {
		t.Errorf("%s: FailureThreshold = %d, 기대 %d", label, got.FailureThreshold, want.failureThreshold)
	}
}

// assertNoRC — mongosh 를 부르는 프로브는 --norc 로 rc 파일 로딩을 건너뛴다(기동 비용 절감).
func assertNoRC(t *testing.T, label string, p *corev1.Probe) {
	t.Helper()
	if p == nil || p.Exec == nil {
		return
	}

	cmd := strings.Join(p.Exec.Command, " ")
	if !strings.Contains(cmd, "mongosh") {
		return
	}
	if !strings.Contains(cmd, "--norc") {
		t.Errorf("%s: mongosh 프로브에 --norc 누락: %s", label, cmd)
	}
}

var (
	wantLiveness  = probeSpec{periodSeconds: 30, timeoutSeconds: 15, failureThreshold: 4}
	wantReadiness = probeSpec{periodSeconds: 15, timeoutSeconds: 10}
)

func TestProbeTiming_ConfigServer(t *testing.T) {
	t.Parallel()
	c := BuildConfigServerStatefulSet(newTestSharded()).Spec.Template.Spec.Containers[0]

	assertProbe(t, "cfg liveness", c.LivenessProbe, wantLiveness)
	assertProbe(t, "cfg readiness", c.ReadinessProbe, wantReadiness)
	assertNoRC(t, "cfg liveness", c.LivenessProbe)
	assertNoRC(t, "cfg readiness", c.ReadinessProbe)
}

func TestProbeTiming_Shard(t *testing.T) {
	t.Parallel()
	c := BuildShardStatefulSet(newTestSharded(), 0).Spec.Template.Spec.Containers[0]

	assertProbe(t, "shard liveness", c.LivenessProbe, wantLiveness)
	assertProbe(t, "shard readiness", c.ReadinessProbe, wantReadiness)
	assertNoRC(t, "shard liveness", c.LivenessProbe)
	assertNoRC(t, "shard readiness", c.ReadinessProbe)
}

func TestProbeTiming_ReplicaSet(t *testing.T) {
	t.Parallel()
	c := BuildReplicaSetStatefulSet(newTestMongoDB()).Spec.Template.Spec.Containers[0]

	assertProbe(t, "rs liveness", c.LivenessProbe, wantLiveness)
	assertProbe(t, "rs readiness", c.ReadinessProbe, wantReadiness)
	assertNoRC(t, "rs liveness", c.LivenessProbe)
}

// TestMongotSidecarHasResources — mongot 은 JVM 이고 힙 상한을 cgroup 에서 읽는다.
// limit 이 없으면 노드 RAM 기준으로 잡아 상한이 사라진다(실측: 571Mi~3956Mi 제각각).
// 파드 QoS 도 이 컨테이너 하나 때문에 Burstable 로 떨어진다.
func TestMongotSidecarHasResources(t *testing.T) {
	t.Parallel()
	mongotC, _, _ := MongotSidecar("ks", "mongodb/mongodb-community-search:latest", "ks-sync")

	for _, axis := range []struct {
		name string
		list corev1.ResourceList
	}{
		{"requests", mongotC.Resources.Requests},
		{"limits", mongotC.Resources.Limits},
	} {
		if axis.list.Cpu().IsZero() {
			t.Errorf("mongot %s.cpu 미지정", axis.name)
		}
		if axis.list.Memory().IsZero() {
			t.Errorf("mongot %s.memory 미지정", axis.name)
		}
	}
}
