/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// router shard readiness JS 를 node 로 실제 평가해 잠근다. db · quit · fs 는
// 가짜이고, 한 컨테이너의 연속 probe 는 같은 가짜 fs 를 공유한다.
//
// 2026-11-15 v1.16.15 롤링: 피어 하나가 재시작하자 남은 멤버의 mongot 이
// replication 을 다시 세워 색인이 잠시 queryable 이 아니었고, shard-1 세
// 멤버가 동시에 NotReady 였다(PDB available 0).

package resources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

type memberState string

const (
	statePrimary    memberState = "primary"
	stateSecondary  memberState = "secondary"
	stateRecovering memberState = "recovering"
)

type probeStep struct {
	State     memberState `json:"state"`
	Queryable []bool      `json:"queryable"`
	AuthFails bool        `json:"authFails"`
}

const probeHarness = `
const [probe, stepsJSON] = process.argv.slice(2);
const files = {'/etc/mongodb-admin/password': 'pw\n'};
const fakeFs = {
  readFileSync: (p) => { if (!(p in files)) { throw new Error('ENOENT ' + p) } return files[p] },
  existsSync: (p) => p in files,
  writeFileSync: (p, v) => { files[p] = String(v) },
};
const hello = {
  primary: {isWritablePrimary: true, secondary: false},
  secondary: {isWritablePrimary: false, secondary: true},
  recovering: {isWritablePrimary: false, secondary: false},
};
class Quit { constructor(c) { this.code = c ?? 0 } }
const codes = [];
for (const st of JSON.parse(stepsJSON)) {
  const coll = {aggregate: () => ({toArray: () => st.queryable.map((q) => ({queryable: q}))})};
  const sib = () => ({
    auth: () => { if (st.authFails) { throw new Error('auth failed') } return 1 },
    adminCommand: (c) => (c.hello ? hello[st.state] : {databases: [{name: 'admin'}, {name: 'app'}]}),
    getCollectionInfos: () => [{name: 'docs'}],
    getCollection: () => coll,
  });
  const db = Object.assign(sib(), {getSiblingDB: sib});
  let code = 0;
  try {
    new Function('db', 'quit', 'require', probe)(db, (c) => { throw new Quit(c) }, () => fakeFs);
  } catch (e) {
    code = e instanceof Quit ? e.code : 1;
  }
  codes.push(code);
}
console.log(JSON.stringify(codes));
`

func routerProbeJS(t *testing.T) string {
	t.Helper()
	cmd := mongodReadiness(27018, mongodbv1alpha1.ReadinessMemberState, searchGateOn)
	return cmd[len(cmd)-1]
}

// runProbe evaluates the router readiness JS once per step in one container
// and returns each exit code.
func runProbe(t *testing.T, steps ...probeStep) []int {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	harness := filepath.Join(t.TempDir(), "harness.js")
	require.NoError(t, os.WriteFile(harness, []byte(probeHarness), 0o600))
	stepsJSON, err := json.Marshal(steps)
	require.NoError(t, err)

	out, err := exec.Command(node, harness, routerProbeJS(t), string(stepsJSON)).Output()
	require.NoError(t, err)
	var codes []int
	require.NoError(t, json.Unmarshal(out, &codes))
	return codes
}

var (
	allQueryable = []bool{true, true}
	oneBuilding  = []bool{true, false}
)

// TestRouterProbe_PeerRestartKeepsReady — 한 번 Ready 가 된 멤버는 피어 재시작으로
// 색인이 잠시 queryable 이 아니어도 Ready 로 남는다.
func TestRouterProbe_PeerRestartKeepsReady(t *testing.T) {
	codes := runProbe(t,
		probeStep{State: stateSecondary, Queryable: allQueryable},
		probeStep{State: stateSecondary, Queryable: oneBuilding},
		probeStep{State: statePrimary, Queryable: oneBuilding},
	)

	require.Equal(t, []int{0, 0, 0}, codes)
}

// TestRouterProbe_InitialWaitsForIndexes — 새 컨테이너는 색인이 전부 queryable
// 해질 때까지 NotReady 다.
func TestRouterProbe_InitialWaitsForIndexes(t *testing.T) {
	codes := runProbe(t,
		probeStep{State: stateSecondary, Queryable: oneBuilding},
		probeStep{State: stateSecondary, Queryable: allQueryable},
	)

	require.Equal(t, []int{1, 0}, codes)
}

// TestRouterProbe_MemberStateStillGates — Ready 뒤에도 PRIMARY · SECONDARY 가
// 아니면 NotReady 다.
func TestRouterProbe_MemberStateStillGates(t *testing.T) {
	codes := runProbe(t,
		probeStep{State: statePrimary, Queryable: allQueryable},
		probeStep{State: stateRecovering, Queryable: allQueryable},
	)

	require.Equal(t, []int{0, 1}, codes)
}

// TestRouterProbe_AuthFailureFailsClosed — 첫 search 검사의 인증 실패는 NotReady 다.
func TestRouterProbe_AuthFailureFailsClosed(t *testing.T) {
	codes := runProbe(t, probeStep{State: statePrimary, Queryable: allQueryable, AuthFails: true})

	require.Equal(t, []int{1}, codes)
}
