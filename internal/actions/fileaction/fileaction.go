package fileaction

// Package fileaction implements a workflow action that creates, deletes, or renames files.
// When a matching event occurs, it emits a result to the workflow engine.
//
// Dynamic args (issue #28):
//   - args values support literal strings, `$input.<path>` references
//     (resolved against the upstream ResultData payload threaded through
//     WorkflowManager.Run inputs, i.e. the results of dependencies),
//     and `{{...}}` placeholders.
//   - `{{date}}` defaults to local YYYYMMDD (Go layout "20060102").
//     `{{date:<layout>}}` (e.g. `{{date:2006-01-02}}`) uses the given Go layout.
//   - `{{file}}`, `{{base}}`, `{{ext}}`, `{{stem}}` are derived from the
//     resolved source file. Date stamping lives in datetime —
//     thread its result in via merge_input and reference it as a bare
//     `$input.<field>` (e.g. `$input.now`); `{{...}}` never nests refs.
//   - `~` leading paths are expanded to the user home dir.
//   - collision policy: if the destination already exists, do NOT overwrite;
//     emit a Type:"error" result and leave the source untouched.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mcmx/nitejaguar/common"
)

type fileaction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

type payload struct {
	Type    string `json:"type"`               // Event type
	File    string `json:"file"`               // File name
	NewFile string `json:"new_file,omitempty"` // New file name
	Result  any    `json:"result"`             // Generic payload for event-specific data
}

func (f *fileaction) Execute(executionId string, inputs []any) {
	fmt.Println("Executing File Action with id:", f.data.Id)
	trigger := findTriggerResult(inputs)
	rawArgs, err := common.ArgsToStringMap(f.data.Args)
	if err != nil {
		fmt.Println("[fileaction] Invalid arguments:", err)
		f.sendResult(executionId, payload{Type: "error", Result: err.Error()})
		return
	}

	// Resolve source file first so {{file}}/{{base}}/{{ext}}/{{stem}}
	// in new_file can derive from it.
	src := ""
	if v, ok := rawArgs["file"]; ok {
		src, err = resolveArgValue(v, trigger, "")
		if err != nil {
			fmt.Println("[fileaction] Cannot resolve file arg:", err)
			f.sendResult(executionId, payload{Type: "error", Result: err.Error()})
			return
		}
		src, err = common.ExpandPath(src)
		if err != nil {
			fmt.Println("[fileaction] Invalid file path:", err)
			f.sendResult(executionId, payload{Type: "error", Result: err.Error()})
			return
		}
	}

	dst := ""
	if v, ok := rawArgs["new_file"]; ok {
		dst, err = resolveArgValue(v, trigger, src)
		if err != nil {
			fmt.Println("[fileaction] Cannot resolve new_file arg:", err)
			f.sendResult(executionId, payload{Type: "error", File: src, Result: err.Error()})
			return
		}
		dst, err = common.ExpandPath(dst)
		if err != nil {
			fmt.Println("[fileaction] Invalid new_file path:", err)
			f.sendResult(executionId, payload{Type: "error", File: src, Result: err.Error()})
			return
		}
	}

	// Merge resolved values back for the switch.
	args := map[string]string{"action": rawArgs["action"], "file": src}
	if dst != "" {
		args["new_file"] = dst
	}
	// Preserve any other raw args (resolved best-effort).
	for k, v := range rawArgs {
		if _, known := args[k]; !known {
			rv, rerr := resolveArgValue(v, trigger, src)
			if rerr != nil {
				rv = v
			} else {
				if expanded, eerr := common.ExpandPath(rv); eerr == nil {
					rv = expanded
				}
			}
			args[k] = rv
		}
	}

	switch args["action"] {
	case "create":
		if args["file"] == "" {
			msg := "missing file argument for create"
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", Result: msg})
			return
		}
		if _, statErr := os.Stat(args["file"]); statErr == nil {
			msg := fmt.Sprintf("destination already exists: %s", args["file"])
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: msg})
			return
		}
		if err := os.MkdirAll(filepath.Dir(args["file"]), 0o755); err != nil {
			fmt.Println("Error creating parent dirs with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: err.Error()})
			return
		}
		if _, err := os.Create(args["file"]); err != nil {
			fmt.Println("Error creating file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], Result: "File created successfully"})
	case "remove":
		if args["file"] == "" {
			msg := "missing file argument for remove"
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", Result: msg})
			return
		}
		if err := os.Remove(args["file"]); err != nil {
			fmt.Println("Error removing file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], Result: "File removed successfully"})
	case "rename":
		if args["file"] == "" || args["new_file"] == "" {
			msg := "missing file or new_file argument for rename"
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: msg})
			return
		}
		if _, statErr := os.Stat(args["new_file"]); statErr == nil {
			msg := fmt.Sprintf("destination already exists: %s", args["new_file"])
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: msg})
			return
		}
		if err := os.MkdirAll(filepath.Dir(args["new_file"]), 0o755); err != nil {
			fmt.Println("Error creating parent dirs with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: err.Error()})
			return
		}
		if err := os.Rename(args["file"], args["new_file"]); err != nil {
			fmt.Println("Error renaming file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], NewFile: args["new_file"], Result: "File renamed successfully"})
	default:
		fmt.Println("Unknown action with id:", f.data.Id)
		f.sendResult(executionId, payload{Type: "error", Result: fmt.Sprintf("unknown action %q", args["action"])})
	}
}

func (f *fileaction) Stop() error {
	fmt.Println("Stopping File Action with id:", f.data.Id)
	return nil
}

func (f *fileaction) GetArgs() common.ActionArgs {
	return f.data
}

func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &fileaction{events: events, data: data}
	s.data.ActionType = "action"
	fmt.Println("Initializing File Action with id:", s.data.Id)
	return s, nil
}

func (t *fileaction) sendResult(executionId string, payload payload) {
	t.events <- common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		Payload:     payload,
	}
}

// findTriggerResult returns the first ResultData found in inputs, if any.
func findTriggerResult(inputs []any) *common.ResultData {
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

var resultRefPattern = regexp.MustCompile(`\$input\.[A-Za-z0-9_.\[\]]+`)

// resolveArgValue resolves one arg value: inline `$input.<path>` references
// plus `{{...}}` placeholders (source-file names and upstream
// `{{ $input.<path> | filter }}` expressions). Literals pass through.
// $input. resolves against the upstream ResultData payload.
// $result./$args. are rejected: the node's own result does not exist
// yet at arg-resolution time.
func resolveArgValue(raw string, trigger *common.ResultData, sourceFile string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. resolves against upstream payload in action args", raw)
	}
	out := raw
	// Split out `{{...}}` regions so bare `$input.` refs and templates
	// never consume each other when adjacent (`$input.now{{ext}}`,
	// `{{ $input.tag | upper }}`).
	var sb strings.Builder
	for i, seg := range common.SplitTemplates(out) {
		if i%2 == 1 {
			repl, err := expandPlaceholder(seg, sourceFile, trigger)
			if err != nil {
				return "", err
			}
			sb.WriteString(repl)
			continue
		}
		r, err := resolveRefs(seg, trigger, raw)
		if err != nil {
			return "", err
		}
		sb.WriteString(r)
	}
	return sb.String(), nil
}

// resolveRefs expands bare `$input.<path>` references in a literal
// segment (one without `{{...}}` regions).
func resolveRefs(seg string, trigger *common.ResultData, raw string) (string, error) {
	if !strings.Contains(seg, "$input.") {
		return seg, nil
	}
	if trigger == nil {
		return "", fmt.Errorf("no trigger result available to resolve %q", raw)
	}
	var resolveErr error
	out := resultRefPattern.ReplaceAllStringFunc(seg, func(m string) string {
		if resolveErr != nil {
			return m
		}
		val, err := common.ResolveRefPath(trigger.Payload, m)
		if err != nil {
			resolveErr = err
			return m
		}
		return common.StringifyArgValue(val)
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return out, nil
}

// expandPlaceholder expands one `{{...}}` inner expression: either a
// source-file placeholder (`file`, `base`, `ext`, `stem`, with an
// optional leading dot as in `{{.file}}`) or an upstream expression
// (`{{ $input.<path> }}` with an optional `|` filter chain, resolved
// via the shared template engine).
func expandPlaceholder(inner string, sourceFile string, trigger *common.ResultData) (string, error) {
	expr := strings.TrimSpace(inner)
	// Allow `{{.file}}` style: strip one leading dot.
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return "", fmt.Errorf("empty placeholder")
	}
	if strings.HasPrefix(expr, "$") {
		v, err := common.EvalTemplate(expr, common.InputLookup(trigger))
		if err != nil {
			return "", err
		}
		return common.TemplateString(v), nil
	}
	lower := strings.ToLower(expr)
	switch lower {
	case "file":
		return sourceFile, nil
	case "base":
		if sourceFile == "" {
			return "", fmt.Errorf("no source file available for {{base}}")
		}
		return filepath.Base(sourceFile), nil
	case "ext":
		if sourceFile == "" {
			return "", fmt.Errorf("no source file available for {{ext}}")
		}
		return filepath.Ext(sourceFile), nil
	case "stem":
		if sourceFile == "" {
			return "", fmt.Errorf("no source file available for {{stem}}")
		}
		base := filepath.Base(sourceFile)
		return strings.TrimSuffix(base, filepath.Ext(base)), nil
	}
	return "", fmt.Errorf("unknown placeholder {{%s}}", inner)
}
