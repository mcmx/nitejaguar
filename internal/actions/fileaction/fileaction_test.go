package fileaction

import (
	"os"
	"path/filepath"
	"testing"

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

// $input.file resolution: upstream result flows into the rename.
func TestRenameResolvesResultFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "report-renamed.pdf")
	a, events := newTestAction(t, map[string]string{
		"action":   "rename",
		"file":     "$input.file",
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
		"file":     "$input.file",
		"new_file": dst,
	})
	a.Execute("exec1", triggerWithFile(src))
	p := readPayload(t, events)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
}

// Date suffix now comes from datetimeAction via a bare $input ref (no braces):
// the upstream merged payload carries the trigger `file` plus datetime `now`.
func TestRenameDateSuffixFromInput(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "statement.pdf")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, events := newTestAction(t, map[string]string{
		"action":   "rename",
		"file":     "$input.file",
		"new_file": dir + "/{{stem}}-$input.now{{ext}}",
	})
	inputs := []any{common.ResultData{
		ExecutionID: "exec1",
		ActionID:    "action_01m3167tvxedb9y6ghyrehz5fk",
		ActionType:  "action",
		ActionName:  "datetimeAction",
		Payload:     map[string]any{"type": "success", "file": src, "now": "20260921"},
	}}
	a.Execute("exec1", inputs)
	p := readPayload(t, events)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	want := filepath.Join(dir, "statement-20260921.pdf")
	if p.NewFile != want {
		t.Fatalf("expected new_file %q, got %q", want, p.NewFile)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected dated file to exist: %v", err)
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
		"file":     "$input.file",
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
	if got, err := common.ExpandPath("~/Downloads"); err != nil || got != filepath.Join(home, "Downloads") {
		t.Fatalf("~/Downloads expanded to %q, err %v, want %q", got, err, filepath.Join(home, "Downloads"))
	}
	if got, err := common.ExpandPath("~"); err != nil || got != home {
		t.Fatalf("~ expanded to %q, err %v, want %q", got, err, home)
	}
	if got, err := common.ExpandPath("/tmp/x"); err != nil || got != "/tmp/x" {
		t.Fatalf("absolute path changed: %q, err %v", got, err)
	}
	// Invalid tilde placements / exploits
	if _, err := common.ExpandPath("foo/~"); err == nil {
		t.Fatal("expected error for 'foo/~'")
	}
	if _, err := common.ExpandPath("~foo"); err == nil {
		t.Fatal("expected error for '~foo'")
	}
	if _, err := common.ExpandPath("path\x00null"); err == nil {
		t.Fatal("expected error for null byte path")
	}
}

func TestDollarSyntaxRejections(t *testing.T) {
	for _, ref := range []string{"$result.file", "$args.file"} {
		a, events := newTestAction(t, map[string]string{"action": "remove", "file": ref})
		a.Execute("exec1", triggerWithFile("/tmp/x"))
		if p := readPayload(t, events); p.Type != "error" {
			t.Fatalf("expected error for %s in args, got %+v", ref, p)
		}
	}
}

func TestArgsToStringMapRobust(t *testing.T) {
	// map[string]any
	m, err := common.ArgsToStringMap(map[string]any{"action": "remove", "file": "/tmp/x"})
	if err != nil || m["action"] != "remove" {
		t.Fatalf("map[string]any failed: %v %+v", m, err)
	}
	// map[string]string
	m2, err := common.ArgsToStringMap(map[string]string{"action": "remove"})
	if err != nil {
		t.Fatalf("map[string]string failed: %v", err)
	}
	_ = m2
	// invalid (non-map) must error, not panic
	if _, err := common.ArgsToStringMap("nope"); err == nil {
		t.Fatal("expected error for string args")
	}
	if _, err := common.ArgsToStringMap(nil); err == nil {
		t.Fatal("expected error for nil args")
	}
}
