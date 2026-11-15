/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// bootstrap-admin.sh 의 rs.initiate 피어 가드를 실제 bash 로 돌려 잠근다.
// mongosh · sleep 은 PATH 앞의 stub 이 대신한다 — 피어 상태는 파일 하나씩:
//
//	peer-<host:port> = bare      응답, setName 없음
//	                 = set:<rs>  응답, setName=<rs>
//	(파일 없음)                  응답 없음 (연결 실패)

package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeMongosh = `#!/bin/bash
args="$*"
input=""
if [ ! -t 0 ]; then input=$(cat); fi
case "$args" in
  *mongodb://*)
    host="${args#*mongodb://}"; host="${host%%/*}"
    [ -f "$FAKE_DIR/peer-$host" ] || exit 1
    st=$(cat "$FAKE_DIR/peer-$host")
    [ "$st" = "bare" ] && { echo ""; exit 0; }
    echo "${st#set:}"; exit 0 ;;
  *"rs.status()"*) echo "init"; exit 0 ;;
  *isWritablePrimary*) echo "true"; exit 0 ;;
  *ping*) echo "1"; exit 0 ;;
esac
case "$input" in
  *rs.initiate*) echo initiate >> "$FAKE_DIR/calls"; echo "rs.initiate OK" ;;
  *createUser*) echo createUser >> "$FAKE_DIR/calls"; echo "createUser OK" ;;
esac
exit 0
`

const (
	self  = "rs-0.rs-headless.ns.svc.cluster.local:27017"
	peer1 = "rs-1.rs-headless.ns.svc.cluster.local:27017"
	peer2 = "rs-2.rs-headless.ns.svc.cluster.local:27017"
)

type bootstrapRun struct {
	exitCode int
	calls    string
	output   string
}

func runBootstrap(t *testing.T, peers map[string]string) bootstrapRun {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(bin, "mongosh"), fakeMongosh)
	writeExec(t, filepath.Join(bin, "sleep"), "#!/bin/sh\nexit 0\n")
	for host, state := range peers {
		if err := os.WriteFile(filepath.Join(dir, "peer-"+host), []byte(state), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	script, err := RenderBootstrap(27017)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bootstrap.sh")
	writeExec(t, path, script)

	cmd := exec.Command(bash, path)
	cmd.Env = []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"HOSTNAME=rs-0",
		"FAKE_DIR=" + dir,
		"MONGO_REPLSET=rs0",
		"MONGO_PEER_WAIT_SECONDS=0",
		"MONGO_MEMBERS=" + strings.Join([]string{self, peer1, peer2}, ","),
	}
	out, err := cmd.CombinedOutput()
	run := bootstrapRun{output: string(out)}
	if exitErr, ok := err.(*exec.ExitError); ok {
		run.exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run bootstrap: %v", err)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	run.calls = string(calls)
	return run
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
}

func TestBootstrap_InitiatesWhenAllPeersBare(t *testing.T) {
	run := runBootstrap(t, map[string]string{peer1: "bare", peer2: "bare"})
	if run.exitCode != 0 {
		t.Fatalf("exit=%d, want 0\n%s", run.exitCode, run.output)
	}
	if run.calls != "initiate\ncreateUser\n" {
		t.Fatalf("calls=%q, want initiate then createUser\n%s", run.calls, run.output)
	}
}

func TestBootstrap_SkipsWhenPeerHasSetName(t *testing.T) {
	run := runBootstrap(t, map[string]string{peer1: "set:rs0", peer2: "bare"})
	if run.exitCode != 0 {
		t.Fatalf("exit=%d, want 0\n%s", run.exitCode, run.output)
	}
	if run.calls != "" {
		t.Fatalf("calls=%q, want none (replica set exists)\n%s", run.calls, run.output)
	}
}

func TestBootstrap_SkipsWhenAnySetNameDespiteSilentPeer(t *testing.T) {
	run := runBootstrap(t, map[string]string{peer2: "set:rs0"})
	if run.exitCode != 0 {
		t.Fatalf("exit=%d, want 0\n%s", run.exitCode, run.output)
	}
	if run.calls != "" {
		t.Fatalf("calls=%q, want none\n%s", run.calls, run.output)
	}
}

func TestBootstrap_FailsClosedWhenPeerSilent(t *testing.T) {
	run := runBootstrap(t, map[string]string{peer1: "bare"})
	if run.exitCode == 0 {
		t.Fatalf("exit=0, want non-zero (peer silent)\n%s", run.output)
	}
	if run.calls != "" {
		t.Fatalf("calls=%q, want no rs.initiate\n%s", run.calls, run.output)
	}
}
