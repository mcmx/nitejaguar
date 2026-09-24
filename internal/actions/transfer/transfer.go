// Package transfer implements the `transfer` workflow action.
//
// Foundation slice: local byte-for-byte file copy with cross-OS safe
// semantics. Distributed WebRTC P2P + server relay ships in a later
// slice; the `destination_client` / `destination_client_tags` arguments
// are accepted and echoed today so workflows can already target the
// receiver, while execution still copies locally.
//
// Arguments (all strings, `$input.<path>` + `{{...}}` templating + `~`
// expansion, same as fileaction):
//   - file: source path (required).
//   - destination_file (alias new_file): destination path (required).
//     A single full path — not dir+name split. Expanded on the executing
//     (receiver-equivalent) side; parent dirs are created (0755).
//   - destination_client: optional receiver client id. Reserved for
//     distributed routing; echoed in the result payload.
//   - destination_client_tags: optional receiver tags. Reserved; echoed.
//   - permissions: optional Unix octal mode ("0644", "0755", "644").
//     Applied to the destination file via Chmod. Omitted = preserve
//     source mode, fallback 0644. Best-effort on Windows (Chmod only
//     honors the readonly bit there): a chmod failure on Windows warns
//     but still succeeds.
//
// Cross-OS rules:
//   - Bytes are copied verbatim (no line-ending conversion); result
//     carries size + sha256.
//   - Destination basenames containing Windows-illegal characters
//     (<>:"|?*) or reserved names (CON, PRN, AUX, NUL, COM1-9, LPT1-9)
//     are rejected on every OS so a Linux sender can never produce a
//     file a Windows receiver could not store.
//   - Collision policy mirrors fileaction: existing destination ->
//     Type:"error", source untouched.
package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/mcmx/nitejaguar/common"
)

type transfer struct {
	data   common.ActionArgs
	events chan common.ResultData
}

type payload struct {
	Type                 string `json:"type"`
	File                 string `json:"file,omitempty"`
	DestinationFile      string `json:"destination_file,omitempty"`
	DestinationClient    string `json:"destination_client,omitempty"`
	DestinationTags      string `json:"destination_client_tags,omitempty"`
	Bytes                int64  `json:"bytes,omitempty"`
	SHA256               string `json:"sha256,omitempty"`
	PermissionsApplied   string `json:"permissions_applied,omitempty"`
	PermissionsRequested string `json:"permissions_requested,omitempty"`
	Result               any    `json:"result"`
}

func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &transfer{events: events, data: data}
	s.data.ActionType = "action"
	return s, nil
}

func (t *transfer) Stop() error { return nil }

func (t *transfer) GetArgs() common.ActionArgs { return t.data }

func (t *transfer) Execute(executionID string, inputs []any) {
	trigger := findTriggerResult(inputs)
	rawArgs, err := common.ArgsToStringMap(t.data.Args)
	if err != nil {
		t.send(executionID, payload{Type: "error", Result: err.Error()})
		return
	}

	src := ""
	if v, ok := rawArgs["file"]; ok {
		src, err = resolveArgValue(v, trigger, "")
		if err != nil {
			t.send(executionID, payload{Type: "error", Result: err.Error()})
			return
		}
		src, err = common.ExpandPath(src)
		if err != nil {
			t.send(executionID, payload{Type: "error", Result: err.Error()})
			return
		}
	}

	dstRaw, ok := rawArgs["destination_file"]
	if !ok || strings.TrimSpace(dstRaw) == "" {
		if v, ok2 := rawArgs["new_file"]; ok2 {
			dstRaw = v
			ok = true
		}
	}
	dst := ""
	if ok {
		dst, err = resolveArgValue(dstRaw, trigger, src)
		if err != nil {
			t.send(executionID, payload{Type: "error", File: src, Result: err.Error()})
			return
		}
		dst, err = common.ExpandPath(dst)
		if err != nil {
			t.send(executionID, payload{Type: "error", File: src, Result: err.Error()})
			return
		}
	}

	dstClient := strings.TrimSpace(rawArgs["destination_client"])
	dstTags := strings.TrimSpace(rawArgs["destination_client_tags"])
	permReq := strings.TrimSpace(rawArgs["permissions"])

	if src == "" || dst == "" {
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, Result: "missing file or destination_file argument"})
		return
	}
	if err := validateDestinationBase(dst); err != nil {
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, Result: err.Error()})
		return
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, DestinationClient: dstClient, Result: fmt.Sprintf("cannot stat source: %v", err)})
		return
	}
	if srcInfo.IsDir() {
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, DestinationClient: dstClient, Result: "source is a directory"})
		return
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, DestinationClient: dstClient, Result: fmt.Sprintf("destination already exists: %s", dst)})
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, DestinationClient: dstClient, Result: err.Error()})
		return
	}

	n, sum, err := copyFile(src, dst)
	if err != nil {
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, DestinationClient: dstClient, Result: err.Error()})
		return
	}

	applied, warn, err := applyPermissions(dst, srcInfo, permReq)
	if err != nil {
		_ = os.Remove(dst)
		t.send(executionID, payload{Type: "error", File: src, DestinationFile: dst, DestinationClient: dstClient, Result: err.Error()})
		return
	}
	result := "File transferred successfully"
	if warn != "" {
		result = fmt.Sprintf("File transferred successfully (%s)", warn)
	}
	t.send(executionID, payload{
		Type:                 "success",
		File:                 src,
		DestinationFile:      dst,
		DestinationClient:    dstClient,
		DestinationTags:      dstTags,
		Bytes:                n,
		SHA256:               sum,
		PermissionsApplied:   applied,
		PermissionsRequested: permReq,
		Result:               result,
	})
}

func (t *transfer) send(executionID string, p payload) {
	t.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		Payload:     p,
	}
}

func copyFile(src, dst string) (int64, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	cerr := out.Close()
	if err != nil {
		_ = os.Remove(dst)
		return 0, "", err
	}
	if cerr != nil {
		_ = os.Remove(dst)
		return 0, "", cerr
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// applyPermissions sets the destination mode. Empty request preserves the
// source mode (fallback 0644). Returns the applied octal string.
func applyPermissions(dst string, srcInfo os.FileInfo, requested string) (string, string, error) {
	var mode os.FileMode
	var applied string
	if requested == "" {
		mode = srcInfo.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		applied = fmt.Sprintf("%04o", mode)
	} else {
		cleaned := strings.TrimSpace(requested)
		cleaned = strings.TrimPrefix(cleaned, "0o")
		cleaned = strings.TrimPrefix(cleaned, "0O")
		v, err := strconv.ParseUint(cleaned, 8, 32)
		if err != nil || v > 0o777 {
			return "", "", fmt.Errorf("invalid permissions %q: use octal 000-777 (e.g. 0644)", requested)
		}
		mode = os.FileMode(v)
		applied = fmt.Sprintf("%04o", v)
	}
	if err := os.Chmod(dst, mode); err != nil {
		if runtime.GOOS == "windows" {
			return applied, "permissions are best-effort on Windows", nil
		}
		return "", "", fmt.Errorf("cannot apply permissions %s: %v", applied, err)
	}
	return applied, "", nil
}

var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateDestinationBase rejects basenames a Windows receiver could not
// store, on every OS, so transfers stay cross-OS safe by construction.
// Exported for reuse by the relay download path.
func ValidateDestinationBase(dst string) error {
	return validateDestinationBase(dst)
}

func validateDestinationBase(dst string) error {
	base := filepath.Base(dst)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("invalid destination path %q", dst)
	}
	if strings.ContainsAny(base, "<>:\"|?*") {
		return fmt.Errorf("destination name %q contains Windows-reserved characters (<>:\"|?*)", base)
	}
	stem := base
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	if windowsReserved[strings.ToLower(stem)] {
		return fmt.Errorf("destination name %q is Windows-reserved", base)
	}
	return nil
}

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
var placeholderPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// ResolveArgValue resolves one transfer arg (templating + $input refs).
// Exported so the distributed sender flow reuses identical semantics;
// path expansion (~) stays caller-side (sender expands source, receiver
// expands destination).
func ResolveArgValue(raw string, trigger *common.ResultData, sourceFile string) (string, error) {
	return resolveArgValue(raw, trigger, sourceFile)
}

func resolveArgValue(raw string, trigger *common.ResultData, sourceFile string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. resolves against upstream payload in action args", raw)
	}
	out := raw
	hasRef := strings.Contains(out, "$input.")
	if trigger != nil && hasRef {
		var resolveErr error
		out = resultRefPattern.ReplaceAllStringFunc(out, func(m string) string {
			if resolveErr != nil {
				return m
			}
			val, err := resolveResultPath(trigger.Payload, m)
			if err != nil {
				resolveErr = err
				return m
			}
			return common.StringifyArgValue(val)
		})
		if resolveErr != nil {
			return "", resolveErr
		}
	} else if trigger == nil && hasRef {
		return "", fmt.Errorf("no trigger result available to resolve %q", raw)
	}
	if strings.Contains(out, "{{") {
		var tmplErr error
		out = placeholderPattern.ReplaceAllStringFunc(out, func(m string) string {
			if tmplErr != nil {
				return m
			}
			inner := strings.TrimSpace(m[2 : len(m)-2])
			repl, err := expandPlaceholder(inner, sourceFile)
			if err != nil {
				tmplErr = err
				return m
			}
			return repl
		})
		if tmplErr != nil {
			return "", tmplErr
		}
	}
	return out, nil
}

func expandPlaceholder(inner string, sourceFile string) (string, error) {
	expr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(inner), "."))
	if expr == "" {
		return "", fmt.Errorf("empty placeholder")
	}
	switch strings.ToLower(expr) {
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

func resolveResultPath(root any, fullPath string) (any, error) {
	const prefix = "$input."
	rest := strings.TrimPrefix(fullPath, prefix)
	if rest == fullPath || rest == "" {
		return nil, fmt.Errorf("unsupported path %q", fullPath)
	}
	cur := root
	for _, seg := range strings.Split(rest, ".") {
		if seg == "" {
			return nil, fmt.Errorf("unsupported path %q: empty segment", fullPath)
		}
		field, indices, err := parsePathSegment(seg, fullPath)
		if err != nil {
			return nil, err
		}
		if field != "" {
			cur, err = lookupResultField(cur, field, fullPath)
			if err != nil {
				return nil, err
			}
		}
		for _, idx := range indices {
			cur, err = lookupResultIndex(cur, idx, fullPath)
			if err != nil {
				return nil, err
			}
		}
	}
	return cur, nil
}

func parsePathSegment(seg, fullPath string) (string, []int, error) {
	open := strings.IndexByte(seg, '[')
	if open == -1 {
		return seg, nil, nil
	}
	field := seg[:open]
	rest := seg[open:]
	var indices []int
	for len(rest) > 0 {
		if !strings.HasPrefix(rest, "[") {
			return "", nil, fmt.Errorf("unsupported path %q: malformed segment %q", fullPath, seg)
		}
		closeIdx := strings.IndexByte(rest, ']')
		if closeIdx == -1 {
			return "", nil, fmt.Errorf("unsupported path %q: malformed segment %q", fullPath, seg)
		}
		n, err := strconv.Atoi(rest[1:closeIdx])
		if err != nil {
			return "", nil, fmt.Errorf("unsupported path %q: invalid index in segment %q", fullPath, seg)
		}
		indices = append(indices, n)
		rest = rest[closeIdx+1:]
	}
	return field, indices, nil
}

func derefResultValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return v
		}
		v = v.Elem()
	}
	return v
}

func lookupResultField(cur any, field, fullPath string) (any, error) {
	if cur == nil {
		return nil, fmt.Errorf("unsupported path %q: key %q not found (nil)", fullPath, field)
	}
	v := derefResultValue(reflect.ValueOf(cur))
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported path %q: cannot resolve key %q on non-string map", fullPath, field)
		}
		key := reflect.ValueOf(field)
		if key.Type() != v.Type().Key() {
			if !key.CanConvert(v.Type().Key()) {
				return nil, fmt.Errorf("unsupported path %q: cannot resolve key %q", fullPath, field)
			}
			key = key.Convert(v.Type().Key())
		}
		mv := v.MapIndex(key)
		if !mv.IsValid() {
			return nil, fmt.Errorf("unsupported path %q: key %q not found", fullPath, field)
		}
		return mv.Interface(), nil
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == field || f.Name == field {
				return v.Field(i).Interface(), nil
			}
		}
		return nil, fmt.Errorf("unsupported path %q: key %q not found", fullPath, field)
	default:
		return nil, fmt.Errorf("unsupported path %q: cannot resolve key %q on %s", fullPath, field, v.Kind())
	}
}

func lookupResultIndex(cur any, idx int, fullPath string) (any, error) {
	if cur == nil {
		return nil, fmt.Errorf("unsupported path %q: index %d out of range (nil)", fullPath, idx)
	}
	v := derefResultValue(reflect.ValueOf(cur))
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		if idx < 0 || idx >= v.Len() {
			return nil, fmt.Errorf("unsupported path %q: index %d out of range (len %d)", fullPath, idx, v.Len())
		}
		return v.Index(idx).Interface(), nil
	default:
		return nil, fmt.Errorf("unsupported path %q: cannot index %d on %s", fullPath, idx, v.Kind())
	}
}
