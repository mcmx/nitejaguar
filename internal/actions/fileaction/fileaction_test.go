package fileaction

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

func newTestAction(t *testing.T, args any) (common.Action, chan common.ResultData) {
	t.Helper()
	events := make(chan common.ResultData, 4)
	a, err := New(events, common.ActionArgs{
		Id:         "action_test",
		Name:       "test",
		ActionType: "action",
		ActionName: "fileAction",
		Args:       args,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return a, events
}

func triggerWithFile(file string) []any {
	return []any{common.ResultData{
		ExecutionID: "exec_test",
		ActionID:    "trigger_test",
		ActionType:  "trigger",
		ActionName:  "filechangeTrigger",
		Payload:     map[string]any{"type": "create", "file": file},
	}}
}

func readPayload(t *testing.T, ch chan common.ResultData) payload {
	t.Helper()
	select {
	case r := <-ch:
		p, ok := r.Payload.(payload)
		if !ok {
			t.Fatalf("unexpected payload type %T", r.Payload)
		}
		return p
	default:
		// Execute is synchronous; result should already be queued.
		// Fall through to blocking read to avoid flake.
		r := <-ch
		p, ok := r.Payload.(payload)
		if !ok {
			t.Fatalf("unexpected payload type %T", r.Payload)
		}
		return p
	}
}

// $.result.file resolution: trigger file path flows into the rename.
func TestRenameResolvesResultFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "report-renamed.pdf")
	a, events := newTestAction(t, map[string]string{
		"action":   "rename",
		"file":     "$.result.file",
		"new_file": dst,
	})
	a.Execute("exec1", triggerWithFile(src))
	p := readPayload(t, events)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected dest to exist: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected src to be gone, stat err: %v", err)
	}
}

// Same resolution must work when args arrive as map[string]any (JSON decode)
// without panicking.
func TestRenameAcceptsMapStringAny(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.pdf")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.pdf")
	a, events := newTestAction(t, map[string]any{
		"action":   "rename",
		"file":     "$.result.file",
		"new_file": dst,
	})
	a.Execute("exec1", triggerWithFile(src))
	p := readPayload(t, events)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
}

// Date suffix template must produce <stem>-YYYYMMDD.ext.
func TestRenameDateSuffixTemplate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "statement.pdf")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Use absolute template anchored in dir; {{stem}}/{{date}}/{{ext}}
	// derive from the $.result.file source path.
	a2, events2 := newTestAction(t, map[string]string{
		"action":   "rename",
		"file":     "$.result.file",
		"new_file": dir + "/{{stem}}-{{date}}{{ext}}",
	})
	a2.Execute("exec1", triggerWithFile(src))
	p := readPayload(t, events2)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	wantDate := time.Now().Local().Format("20060102")
	want := filepath.Join(dir, "statement-"+wantDate+".pdf")
	if p.NewFile != want {
		t.Fatalf("expected new_file %q, got %q", want, p.NewFile)
	}
	matched, _ := regexp.MatchString(`statement-\d{8}\.pdf$`, p.NewFile)
	if !matched {
		t.Fatalf("expected -YYYYMMDD pattern, got %q", p.NewFile)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected dated file to exist: %v", err)
	}
	// Explicit {{date:20060102}} form must also work.
	src2 := filepath.Join(dir, "invoice.pdf")
	if err := os.WriteFile(src2, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	a3, events3 := newTestAction(t, map[string]string{
		"action":   "rename",
		"file":     "$.result.file",
		"new_file": dir + "/{{stem}}-{{date:20060102}}{{ext}}",
	})
	a3.Execute("exec1", triggerWithFile(src2))
	p3 := readPayload(t, events3)
	if p3.Type != "success" {
		t.Fatalf("expected success, got %+v", p3)
	}
	if !strings.HasSuffix(p3.NewFile, "-"+wantDate+".pdf") {
		t.Fatalf("expected date suffix, got %q", p3.NewFile)
	}
}

// Collision policy: dest exists -> error, source untouched.
func TestRenameCollisionDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.pdf")
	dst := filepath.Join(dir, "dst.pdf")
	if err := os.WriteFile(src, []byte("SRC"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("DST"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, events := newTestAction(t, map[string]string{
		"action":   "rename",
		"file":     "$.result.file",
		"new_file": dst,
	})
	a.Execute("exec1", triggerWithFile(src))
	p := readPayload(t, events)
	if p.Type != "error" {
		t.Fatalf("expected error on collision, got %+v", p)
	}
	// Source must remain, dest content unchanged.
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("source should still exist: %v", err)
	}
	if string(b) != "SRC" {
		t.Fatalf("source content changed: %q", b)
	}
	b, err = os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "DST" {
		t.Fatalf("dest was overwritten: %q", b)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir available")
	}
	if got := expandPath("~/Downloads"); got != filepath.Join(home, "Downloads") {
		t.Fatalf("~/Downloads expanded to %q, want %q", got, filepath.Join(home, "Downloads"))
	}
	if got := expandPath("~"); got != home {
		t.Fatalf("~ expanded to %q, want %q", got, home)
	}
	if got := expandPath("/tmp/x"); got != "/tmp/x" {
		t.Fatalf("absolute path changed: %q", got)
	}
}

func TestArgsToStringMapRobust(t *testing.T) {
	// map[string]any
	m, err := argsToStringMap(map[string]any{"action": "remove", "file": "/tmp/x"})
	if err != nil || m["action"] != "remove" {
		t.Fatalf("map[string]any failed: %v %+v", m, err)
	}
	// map[string]string
	m2, err := argsToStringMap(map[string]string{"action": "remove"})
	if err != nil {
		t.Fatalf("map[string]string failed: %v", err)
	}
	_ = m2
	// invalid (non-map) must error, not panic
	if _, err := argsToStringMap("nope"); err == nil {
		t.Fatal("expected error for string args")
	}
	if _, err := argsToStringMap(nil); err == nil {
		t.Fatal("expected error for nil args")
	}
}
