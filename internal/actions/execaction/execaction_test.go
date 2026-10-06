package execaction

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

func testArgs(args map[string]any) common.ActionArgs {
	return common.ActionArgs{Id: "action_test", Name: "test", ActionType: "action", ActionName: "exec", Args: args}
}

func runExecute(t *testing.T, args map[string]any, inputs []any) map[string]any {
	t.Helper()
	events := make(chan common.ResultData, 1)
	a, err := New(events, testArgs(args))
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	a.Execute("execution_test", inputs)
	select {
	case r := <-events:
		payload, ok := r.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload type %T, want map[string]any", r.Payload)
		}
		return payload
	default:
		t.Fatal("expected a result")
		return nil
	}
}

func TestEchoCapturesStdout(t *testing.T) {
	payload := runExecute(t, map[string]any{"command": "echo", "args": []any{"hello"}}, nil)
	if payload["type"] != "success" {
		t.Fatalf("type = %v, want success (%v)", payload["type"], payload)
	}
	if payload["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", payload["exit_code"])
	}
	if strings.TrimSpace(payload["stdout"].(string)) != "hello" {
		t.Errorf("stdout = %q, want hello", payload["stdout"])
	}
	if _, ok := payload["duration_ms"]; !ok {
		t.Error("missing duration_ms")
	}
}

func TestFalseYieldsNonZeroError(t *testing.T) {
	payload := runExecute(t, map[string]any{"command": "false"}, nil)
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want error (%v)", payload["type"], payload)
	}
	if payload["exit_code"] != 1 {
		t.Errorf("exit_code = %v, want 1", payload["exit_code"])
	}
	if _, ok := payload["result"]; !ok {
		t.Error("missing result message")
	}
}

func TestSleepTinyTimeoutYieldsTimeoutError(t *testing.T) {
	payload := runExecute(t, map[string]any{"command": "sleep", "args": []any{"5"}, "timeout": "50ms"}, nil)
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want error (%v)", payload["type"], payload)
	}
	msg, _ := payload["result"].(string)
	if !strings.Contains(msg, "timed out") {
		t.Errorf("result = %q, want it to mention timed out", msg)
	}
}

func TestNewRejectsInvalidTimeout(t *testing.T) {
	for _, timeout := range []string{"banana", "0s", "-5s"} {
		if _, err := New(make(chan common.ResultData, 1), testArgs(map[string]any{"command": "echo", "timeout": timeout})); err == nil {
			t.Errorf("New(timeout=%q) succeeded, want error", timeout)
		}
	}
}

func TestNewRejectsEmptyCommand(t *testing.T) {
	for _, args := range []map[string]any{nil, {}, {"command": ""}, {"command": "  "}} {
		if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
			t.Fatalf("New(%v) succeeded, want missing command error", args)
		}
	}
}

func TestNewRejectsNonMapArgs(t *testing.T) {
	if _, err := New(make(chan common.ResultData, 1), common.ActionArgs{
		ActionType: "action", ActionName: "exec", Args: "echo",
	}); err == nil {
		t.Error("New(string args) succeeded, want error")
	}
}

func TestInvalidWorkdirIsErrorResult(t *testing.T) {
	payload := runExecute(t, map[string]any{"command": "echo", "workdir": "/no/such/dir/execaction"}, nil)
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want error (%v)", payload["type"], payload)
	}
}

func TestSingleStringArgIsOneElement(t *testing.T) {
	payload := runExecute(t, map[string]any{"command": "echo", "args": "hello world"}, nil)
	if payload["type"] != "success" {
		t.Fatalf("type = %v, want success (%v)", payload["type"], payload)
	}
	if strings.TrimSpace(payload["stdout"].(string)) != "hello world" {
		t.Errorf("stdout = %q, want single-element echo", payload["stdout"])
	}
}

func TestTemplatesResolveFromInput(t *testing.T) {
	inputs := []any{common.ResultData{Payload: map[string]any{"day": "2024-01-02", "key": "s3cr3t"}}}
	payload := runExecute(t, map[string]any{
		"command": "echo",
		"args":    []any{"{{ $input.day | upper }}", "$input.day"},
		"env":     map[string]any{"DAY": "$input.day"},
	}, inputs)
	if payload["type"] != "success" {
		t.Fatalf("type = %v, want success (%v)", payload["type"], payload)
	}
	if strings.TrimSpace(payload["stdout"].(string)) != "2024-01-02 2024-01-02" {
		t.Errorf("stdout = %q, want templated days; filter upper on a date is a no-op", payload["stdout"])
	}
}

func TestResultRefsRejected(t *testing.T) {
	payload := runExecute(t, map[string]any{"command": "$result.foo"}, nil)
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want error (%v)", payload["type"], payload)
	}
}

func TestStdoutTruncatesAt64KiB(t *testing.T) {
	// seq prints ~100KiB; the result must cap at exactly 64KiB.
	payload := runExecute(t, map[string]any{"command": "seq", "args": []any{"1", "20000"}, "timeout": "30s"}, nil)
	if payload["type"] != "success" {
		t.Fatalf("type = %v, want success (%v)", payload["type"], payload)
	}
	if got := len(payload["stdout"].(string)); got != maxOutputBytes {
		t.Errorf("len(stdout) = %d, want %d", got, maxOutputBytes)
	}
}

func TestAddActionDispatches(t *testing.T) {
	events := make(chan common.ResultData, 1)
	a, err := New(events, testArgs(CatalogEntry().Args))
	if err != nil {
		t.Fatalf("New with catalog defaults error: %v", err)
	}
	if a == nil {
		t.Fatal("expected action")
	}
}

func TestExampleExecWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/workflow-exec.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var wf struct {
		Nodes map[string]struct {
			ID         string            `json:"id"`
			ActionType string            `json:"action_type"`
			ActionName string            `json:"action_name"`
			Arguments  map[string]string `json:"arguments"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse example: %v", err)
	}
	found := false
	for _, n := range wf.Nodes {
		if n.ActionType != "action" || n.ActionName != "exec" {
			continue
		}
		found = true
		a, err := New(make(chan common.ResultData, 1), common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("New(exec example) error: %v", err)
		}
		if a == nil {
			t.Fatal("expected action")
		}
	}
	if !found {
		t.Fatal("example workflow has no exec action node")
	}
}
