package transfer

import (
	"crypto/sha256"
	"encoding/hex"
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
		ActionName: "transfer",
		Args:       args,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return a, events
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
		r := <-ch
		p, ok := r.Payload.(payload)
		if !ok {
			t.Fatalf("unexpected payload type %T", r.Payload)
		}
		return p
	}
}

func TestCopySuccessPreservesBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	content := []byte{0x00, 0x01, 0x02, 'a', 'b', '\n'}
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "sub", "dst.bin")
	a, events := newTestAction(t, map[string]string{"file": src, "destination_file": dst})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("expected dest to exist: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: %q vs %q", got, content)
	}
	sum := sha256.Sum256(content)
	if p.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha mismatch: %q", p.SHA256)
	}
	if p.Bytes != int64(len(content)) {
		t.Fatalf("bytes mismatch: %d", p.Bytes)
	}
}

func TestCollisionDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("SRC"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("DST"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, events := newTestAction(t, map[string]string{"file": src, "destination_file": dst})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p.Type != "error" {
		t.Fatalf("expected error on collision, got %+v", p)
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "DST" {
		t.Fatalf("dest was overwritten: %q", b)
	}
}

func TestPermissionsApplied(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.txt")
	a, events := newTestAction(t, map[string]string{"file": src, "destination_file": dst, "permissions": "0644"})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p.PermissionsApplied != "0644" {
		t.Fatalf("expected 0644 applied, got %q", p.PermissionsApplied)
	}
}

func TestPermissionsPreservedWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.txt")
	a, events := newTestAction(t, map[string]string{"file": src, "destination_file": dst})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p.PermissionsApplied == "" {
		t.Fatal("expected preserved permissions to be reported")
	}
}

func TestInvalidPermissionsFailsClosed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"999", "abc", "0888"} {
		dst := filepath.Join(dir, "dst-"+bad+".txt")
		a, events := newTestAction(t, map[string]string{"file": src, "destination_file": dst, "permissions": bad})
		a.Execute("exec1", nil)
		if p := readPayload(t, events); p.Type != "error" {
			t.Fatalf("expected error for %q, got %+v", bad, p)
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Fatalf("partial file left for %q", bad)
		}
	}
}

func TestWindowsIllegalNamesRejectedEverywhere(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"bad:name.txt", "bad|name.txt", "NUL.txt", "con"} {
		a, events := newTestAction(t, map[string]string{"file": src, "destination_file": filepath.Join(dir, base)})
		a.Execute("exec1", nil)
		if p := readPayload(t, events); p.Type != "error" {
			t.Fatalf("expected error for %q, got %+v", base, p)
		}
	}
}

func TestInputResolution(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.pdf")
	a, events := newTestAction(t, map[string]string{"file": "$input.file", "destination_file": dst})
	inputs := []any{common.ResultData{
		ExecutionID: "exec1",
		ActionID:    "trigger_test",
		ActionType:  "trigger",
		ActionName:  "filechange",
		Payload:     map[string]any{"type": "create", "file": src},
	}}
	a.Execute("exec1", inputs)
	if p := readPayload(t, events); p.Type != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
}

func TestDestinationClientEchoed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.txt")
	a, events := newTestAction(t, map[string]string{
		"file": src, "destination_file": dst, "destination_client": "client_123",
	})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p.Type != "success" || p.DestinationClient != "client_123" {
		t.Fatalf("expected echoed destination_client, got %+v", p)
	}
}
