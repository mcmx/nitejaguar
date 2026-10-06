// Package execaction implements the `exec` workflow action: it runs a
// local program or script on the executing node (server or client) and
// captures the outcome — the Tidal-style job primitive.
//
// Arguments (all templatable via the shared engine):
//   - command: binary or script path (required), e.g. "/usr/local/bin/etl.sh".
//     `~` expansion applies.
//   - args: one argv element each (default []). Accepts a real JSON array;
//     a single string is treated as one element. Each element is
//     individually template-resolved.
//   - workdir: working directory (default ""). `~` expansion applies.
//     Empty means the process default.
//   - timeout: Go duration (default "5m"), parsed with time.ParseDuration.
//     Invalid or non-positive values fail load (New) for literals and
//     fail the node at Execute time for templated values.
//   - env: extra environment as an object (default {}), merged on top of
//     the process env. Values are template-resolved.
//
// Execution runs directly, with no shell: pipes, globs, and redirects
// need an explicit `sh -c` command. The process group is killed on
// timeout, so spawned children die with the parent.
//
// Payload (conditions API): success emits
// {type: "success", command, exit_code: 0, stdout, stderr, duration_ms}.
// Failures emit {type: "error", ...} with a `result` message, routable to
// alerting via `$result.type == error`: missing/empty command, timeout
// (partial stdout/stderr plus "timed out"), non-zero exit (exit_code plus
// captured output), and invalid workdir. stdout/stderr truncate at 64KiB
// each so results cannot blow up the DB.
package execaction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

// maxOutputBytes caps captured stdout/stderr each, so results cannot blow
// up the DB.
const maxOutputBytes = 64 << 10 // 64KiB

// defaultTimeout applies when the `timeout` arg is absent.
const defaultTimeout = "5m"

type execAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// New creates the action; ActionType is forced to "action". Literal
// (non-templated) command/timeout values are validated here so a bad
// definition fails at load; templated values defer to Execute.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &execAction{events: events, data: data}
	s.data.ActionType = "action"
	rawArgs, err := rawArgsMap(data.Args)
	if err != nil {
		return nil, err
	}
	if err := validateStatic(rawArgs); err != nil {
		return nil, err
	}
	return s, nil
}

func (e *execAction) Stop() error { return nil }

func (e *execAction) GetArgs() common.ActionArgs { return e.data }

func (e *execAction) send(executionID string, payload map[string]any) {
	e.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    e.data.Id,
		ActionType:  e.data.ActionType,
		ActionName:  e.data.ActionName,
		Payload:     payload,
	}
}

// Execute resolves templates, runs the command, and emits the result.
func (e *execAction) Execute(executionID string, inputs []any) {
	rawArgs, err := rawArgsMap(e.data.Args)
	if err != nil {
		e.send(executionID, errorPayload("", 0, "", "", 0, err.Error()))
		return
	}
	input := findInputResult(inputs)

	command, err := resolveScalar(rawArgs, input, "command", "cmd", "binary", "script")
	if err != nil {
		e.send(executionID, errorPayload("", 0, "", "", 0, err.Error()))
		return
	}
	command = expandHome(strings.TrimSpace(command))
	if command == "" {
		e.send(executionID, errorPayload("", 0, "", "", 0, "missing command argument (binary or script path)"))
		return
	}

	argv, err := resolveArgv(rawArgs, input)
	if err != nil {
		e.send(executionID, errorPayload(command, 0, "", "", 0, err.Error()))
		return
	}

	workdir, err := resolveScalar(rawArgs, input, "workdir", "work_dir", "dir", "cwd")
	if err != nil {
		e.send(executionID, errorPayload(command, 0, "", "", 0, err.Error()))
		return
	}
	workdir = expandHome(strings.TrimSpace(workdir))
	if workdir != "" {
		if st, err := os.Stat(workdir); err != nil || !st.IsDir() {
			msg := fmt.Sprintf("invalid workdir %q: not a directory", workdir)
			if err != nil {
				msg = fmt.Sprintf("invalid workdir %q: %v", workdir, err)
			}
			e.send(executionID, errorPayload(command, 0, "", "", 0, msg))
			return
		}
	}

	timeoutRaw, err := resolveScalar(rawArgs, input, "timeout", "timeout_seconds", "timeoutSeconds")
	if err != nil {
		e.send(executionID, errorPayload(command, 0, "", "", 0, err.Error()))
		return
	}
	if strings.TrimSpace(timeoutRaw) == "" {
		timeoutRaw = defaultTimeout
	}
	timeout, err := parseTimeout(timeoutRaw)
	if err != nil {
		e.send(executionID, errorPayload(command, 0, "", "", 0, err.Error()))
		return
	}

	extraEnv, err := resolveEnv(rawArgs, input)
	if err != nil {
		e.send(executionID, errorPayload(command, 0, "", "", 0, err.Error()))
		return
	}

	stdout, stderr, exitCode, elapsed, runErr := run(command, argv, workdir, extraEnv, timeout)
	out, errOut := stdout, stderr
	ms := float64(elapsed) / float64(time.Millisecond)
	if runErr != nil {
		if isTimeout(runErr) {
			e.send(executionID, map[string]any{
				"type": "error", "command": command, "exit_code": exitCode,
				"stdout": out, "stderr": errOut, "duration_ms": ms,
				"result": fmt.Sprintf("command %q timed out after %s", command, timeout),
			})
			return
		}
		e.send(executionID, errorPayload(command, exitCode, out, errOut, ms, runErr.Error()))
		return
	}
	e.send(executionID, map[string]any{
		"type": "success", "command": command, "exit_code": 0,
		"stdout": out, "stderr": errOut, "duration_ms": ms,
	})
}

func errorPayload(command string, exitCode int, stdout, stderr string, ms float64, msg string) map[string]any {
	payload := map[string]any{"type": "error", "result": msg}
	if command != "" {
		payload["command"] = command
	}
	if exitCode != 0 {
		payload["exit_code"] = exitCode
	}
	if stdout != "" {
		payload["stdout"] = stdout
	}
	if stderr != "" {
		payload["stderr"] = stderr
	}
	if ms != 0 {
		payload["duration_ms"] = ms
	}
	return payload
}

// validateStatic rejects bad literal command/timeout values at load while
// letting templated values ($input./{{...}}) through to Execute-time
// resolution.
func validateStatic(rawArgs map[string]any) error {
	strOf := func(names ...string) string {
		v, ok := firstPresent(rawArgs, names...)
		if !ok {
			return ""
		}
		if s, ok := stringifyScalar(v); ok {
			return s
		}
		if str, isStr := v.(string); isStr {
			return str
		}
		return ""
	}
	if cmd := strOf("command", "cmd", "binary", "script"); !looksTemplated(cmd) {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("missing command argument (binary or script path)")
		}
	}
	if t := strOf("timeout", "timeout_seconds", "timeoutSeconds"); t != "" && !looksTemplated(t) {
		if _, err := parseTimeout(t); err != nil {
			return err
		}
	}
	return nil
}

// parseTimeout parses a Go duration; it must be positive.
func parseTimeout(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: use a Go duration like \"5m\" or \"30s\"", raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid timeout %q: duration must be positive", raw)
	}
	return d, nil
}

func looksTemplated(s string) bool {
	// $result./$args. refs are unsupported in args, but they defer to
	// Execute-time resolution so the failure carries the explicit
	// "unsupported reference" error instead of a misleading shape error.
	return strings.Contains(s, "$input.") || strings.Contains(s, "{{") ||
		strings.Contains(s, "$result.") || strings.Contains(s, "$args.")
}

// run executes command directly (no shell) and kills the whole process
// group on timeout, so spawned children die with the parent. Captured
// output never exceeds maxOutputBytes per stream.
func run(command string, argv []string, workdir string, extraEnv map[string]string, timeout time.Duration) (stdout, stderr string, exitCode int, elapsed time.Duration, err error) {
	var outBuf, errBuf cappedBuffer
	cmd := exec.Command(command, argv...)
	cmd.Dir = workdir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if len(extraEnv) > 0 {
		env := os.Environ()
		for k, v := range extraEnv {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return "", "", 0, time.Since(start), err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		elapsed = time.Since(start)
		if waitErr == nil {
			return outBuf.String(), errBuf.String(), 0, elapsed, nil
		}
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), elapsed, fmt.Errorf("command %q exited with code %d", command, exitErr.ExitCode())
		}
		return outBuf.String(), errBuf.String(), 0, elapsed, waitErr
	case <-time.After(timeout):
		elapsed = time.Since(start)
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done
		return outBuf.String(), errBuf.String(), -1, elapsed, errTimeout{}
	}
}

// errTimeout marks a deadline kill; it carries no message itself — the
// caller shapes the timeout result with partial output.
type errTimeout struct{}

func (errTimeout) Error() string { return "command timed out" }

func isTimeout(err error) bool {
	_, ok := err.(errTimeout)
	return ok
}

// cappedBuffer is a concurrent-safe writer keeping the first
// maxOutputBytes, so results cannot blow up the DB.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write keeps the first maxOutputBytes and reports the input fully
// consumed: returning a short count with a nil error would make the
// exec copy loop stop early and close the pipe, killing the child with
// SIGPIPE.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := maxOutputBytes - c.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		c.buf.Write(p)
	}
	return n, nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// expandHome expands a leading `~` to the user home directory.
func expandHome(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return s
	}
	if s == "~" {
		return home
	}
	return home + s[1:]
}

// resolveScalar resolves one templated string field (first present alias).
func resolveScalar(rawArgs map[string]any, input *common.ResultData, names ...string) (string, error) {
	v, ok := firstPresent(rawArgs, names...)
	if !ok {
		return "", nil
	}
	s, ok := stringifyScalar(v)
	if !ok {
		str, isStr := v.(string)
		if !isStr {
			return "", fmt.Errorf("invalid value for %q: must be a string", names[0])
		}
		s = str
	}
	return resolveStringTemplate(s, input)
}

// resolveArgv coerces the `args` value to one argv element per entry,
// template-resolving each. A native array stays an array; a single
// string that parses as a JSON array expands to its elements; any
// other single value is one element.
func resolveArgv(rawArgs map[string]any, input *common.ResultData) ([]string, error) {
	v, ok := firstPresent(rawArgs, "args", "argv", "arguments", "parameters")
	if !ok {
		return nil, nil
	}
	var items []any
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		var arr []any
		if err := json.Unmarshal([]byte(t), &arr); err == nil {
			items = arr
			break
		}
		items = []any{t}
	case []string:
		items = make([]any, len(t))
		for i, s := range t {
			items[i] = s
		}
	case []any:
		items = t
	default:
		rv := reflect.ValueOf(v)
		for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return nil, nil
			}
			rv = rv.Elem()
		}
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("invalid args value: %v", err)
			}
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("invalid args value: must be an array of strings")
			}
		default:
			if s, ok := stringifyScalar(v); ok {
				items = []any{s}
				break
			}
			return nil, fmt.Errorf("invalid args value: must be an array of strings")
		}
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := stringifyScalar(item)
		if !ok {
			str, isStr := item.(string)
			if !isStr {
				return nil, fmt.Errorf("invalid args[%d]: must be a string", i)
			}
			s = str
		}
		resolved, err := resolveStringTemplate(s, input)
		if err != nil {
			return nil, fmt.Errorf("args[%d]: %w", i, err)
		}
		out = append(out, resolved)
	}
	return out, nil
}

// resolveEnv normalizes the `env` object (native map or JSON object
// string) and template-resolves each value.
func resolveEnv(rawArgs map[string]any, input *common.ResultData) (map[string]string, error) {
	v, ok := firstPresent(rawArgs, "env", "environment", "env_vars", "envVars")
	if !ok {
		return nil, nil
	}
	var obj map[string]any
	switch t := v.(type) {
	case map[string]any:
		obj = t
	case map[string]string:
		obj = make(map[string]any, len(t))
		for k, val := range t {
			obj[k] = val
		}
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return nil, nil
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			return nil, fmt.Errorf("invalid env JSON object: %v", err)
		}
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("invalid env value: %v", err)
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("invalid env value: must be an object")
		}
	}
	out := make(map[string]string, len(obj))
	for k, val := range obj {
		if strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("env name must not be empty")
		}
		s, ok := stringifyScalar(val)
		if !ok {
			str, isStr := val.(string)
			if !isStr {
				return nil, fmt.Errorf("env %q: value must be a string", k)
			}
			s = str
		}
		resolved, err := resolveStringTemplate(s, input)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// --- templating helpers (shared engine only) ---

var resultRefPattern = regexp.MustCompile(`\$input\.[A-Za-z0-9_.\[\]]+`)

// resolveStringTemplate expands {{...}} templates then bare $input. refs.
func resolveStringTemplate(raw string, input *common.ResultData) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. resolves against upstream payload in action args", raw)
	}
	if strings.Contains(raw, "{{") && !common.HasTemplate(raw) {
		return "", fmt.Errorf("invalid template in %q: unclosed \"{{\"", raw)
	}
	segs := common.SplitTemplates(raw)
	if len(segs) == 3 && segs[0] == "" && segs[2] == "" {
		v, err := common.EvalTemplate(segs[1], common.InputLookup(input))
		if err != nil {
			return "", err
		}
		return common.TemplateString(v), nil
	}
	var sb strings.Builder
	for i, seg := range segs {
		if i%2 == 1 {
			v, err := common.EvalTemplate(seg, common.InputLookup(input))
			if err != nil {
				return "", err
			}
			sb.WriteString(common.TemplateString(v))
			continue
		}
		if !strings.Contains(seg, "$input.") {
			sb.WriteString(seg)
			continue
		}
		if input == nil {
			return "", fmt.Errorf("no input result available to resolve %q", raw)
		}
		if len(segs) == 1 {
			if trimmed := strings.TrimSpace(seg); resultRefPattern.FindString(trimmed) == trimmed {
				val, err := common.ResolveRefPath(input.Payload, trimmed)
				if err != nil {
					return "", err
				}
				return common.TemplateString(val), nil
			}
		}
		var resolveErr error
		out := resultRefPattern.ReplaceAllStringFunc(seg, func(m string) string {
			if resolveErr != nil {
				return m
			}
			val, err := common.ResolveRefPath(input.Payload, m)
			if err != nil {
				resolveErr = err
				return m
			}
			return common.TemplateString(val)
		})
		if resolveErr != nil {
			return "", resolveErr
		}
		sb.WriteString(out)
	}
	return sb.String(), nil
}

// rawArgsMap normalizes action args while preserving value types.
// Nil means no args.
func rawArgsMap(args any) (map[string]any, error) {
	if args == nil {
		return map[string]any{}, nil
	}
	if m, ok := args.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	if m, ok := args.(map[string]string); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return map[string]any{}, nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("invalid arguments type %T: map key must be string", args)
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid arguments type %T: must be a map", args)
}

func firstPresent(args map[string]any, names ...string) (any, bool) {
	for _, n := range names {
		if v, ok := args[n]; ok && v != nil && strings.TrimSpace(common.StringifyArgValue(v)) != "" {
			return v, true
		}
	}
	return nil, false
}

func stringifyScalar(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprint(rv.Interface()), true
	default:
		return "", false
	}
}

// findInputResult returns the first ResultData found in inputs, if any.
func findInputResult(inputs []any) *common.ResultData {
	for _, in := range inputs {
		switch v := in.(type) {
		case common.ResultData:
			c := v
			return &c
		case *common.ResultData:
			if v != nil {
				return v
			}
		}
	}
	return nil
}
