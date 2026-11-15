/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// resync-marker.sh 를 실제 sh 로 돌려 잠근다. 표지 ConfigMap 은 디렉터리
// 하나(파일 = pod 이름, 내용 = token), dbpath 는 임시 디렉터리다.

package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	resyncPod   = "mongo-shard-1-0"
	resyncToken = "2026-11-15T03-00-00Z.a1"
	tokenFile   = ".keiailab-resync-token"
)

type resyncRun struct {
	exitCode int
	output   string
	dbpath   string
}

type resyncCase struct {
	marker  *string
	current *string
	files   []string
}

func strPtr(s string) *string { return &s }

func runResync(t *testing.T, c resyncCase) resyncRun {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	markers := filepath.Join(dir, "resync")
	dbpath := filepath.Join(dir, "db")
	for _, d := range []string{markers, dbpath, filepath.Join(dbpath, "search-index")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if c.marker != nil {
		writeFile(t, filepath.Join(markers, resyncPod), *c.marker)
	}
	if c.current != nil {
		writeFile(t, filepath.Join(dbpath, tokenFile), *c.current)
	}
	for _, f := range c.files {
		writeFile(t, filepath.Join(dbpath, f), "x")
	}

	script, err := RenderResyncMarker()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "resync-marker.sh")
	writeFile(t, path, script)

	cmd := exec.Command(sh, path)
	cmd.Env = append(os.Environ(), "POD_NAME="+resyncPod, "RESYNC_DIR="+markers, "DBPATH="+dbpath)
	out, _ := cmd.CombinedOutput()
	return resyncRun{exitCode: cmd.ProcessState.ExitCode(), output: string(out), dbpath: dbpath}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dbEntries(t *testing.T, dbpath string) []string {
	t.Helper()
	ents, err := os.ReadDir(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func readToken(t *testing.T, dbpath string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dbpath, tokenFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestResync_NoMarkerKeepsData — 이 pod 의 표지가 없으면 아무것도 지우지 않는다.
func TestResync_NoMarkerKeepsData(t *testing.T) {
	r := runResync(t, resyncCase{files: []string{"WiredTiger", "collection-0.wt"}})

	if r.exitCode != 0 {
		t.Fatalf("exit %d: %s", r.exitCode, r.output)
	}
	if got := strings.Join(dbEntries(t, r.dbpath), ","); got != "WiredTiger,collection-0.wt,search-index" {
		t.Fatalf("dbpath = %s", got)
	}
}

// TestResync_NewTokenWipes — 새 token 이면 dbpath 를 비우고 token 을 남긴다.
func TestResync_NewTokenWipes(t *testing.T) {
	r := runResync(t, resyncCase{
		marker:  strPtr(resyncToken + "\n"),
		current: strPtr("older"),
		files:   []string{"WiredTiger", "collection-0.wt", ".hidden"},
	})

	if r.exitCode != 0 {
		t.Fatalf("exit %d: %s", r.exitCode, r.output)
	}
	if got := strings.Join(dbEntries(t, r.dbpath), ","); got != tokenFile {
		t.Fatalf("dbpath = %s, want only %s", got, tokenFile)
	}
	if got := readToken(t, r.dbpath); got != resyncToken {
		t.Fatalf("token = %q", got)
	}
}

// TestResync_SameTokenKeepsData — 이미 그 token 으로 비웠으면 다시 지우지 않는다.
func TestResync_SameTokenKeepsData(t *testing.T) {
	r := runResync(t, resyncCase{
		marker:  strPtr(resyncToken),
		current: strPtr(resyncToken),
		files:   []string{"WiredTiger"},
	})

	if r.exitCode != 0 {
		t.Fatalf("exit %d: %s", r.exitCode, r.output)
	}
	if got := strings.Join(dbEntries(t, r.dbpath), ","); got != tokenFile+",WiredTiger,search-index" {
		t.Fatalf("dbpath = %s", got)
	}
}

// TestResync_AmbiguousFailsClosed — 비었거나 · 여러 줄이거나 · 허용 밖 문자면 지우지 않고 exit ≠ 0.
func TestResync_AmbiguousFailsClosed(t *testing.T) {
	for name, marker := range map[string]string{
		"empty":     "",
		"blank":     " \n",
		"multiline": "a\nb\n",
		"space":     "a b",
		"slash":     "../x",
		"long":      strings.Repeat("a", 129),
	} {
		t.Run(name, func(t *testing.T) {
			r := runResync(t, resyncCase{marker: strPtr(marker), files: []string{"WiredTiger"}})

			if r.exitCode == 0 {
				t.Fatalf("exit 0 for %q: %s", marker, r.output)
			}
			if got := strings.Join(dbEntries(t, r.dbpath), ","); got != "WiredTiger,search-index" {
				t.Fatalf("dbpath = %s", got)
			}
		})
	}
}
