package set

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/mcmx/nitejaguar/common"
)

// Template expressions (`{{ ... }}`) for `set` values.
//
// A template holds a `$input.<path>` reference (or a quoted literal)
// followed by an optional `|` filter chain:
//
//	"{{ $input.file }}.bkp"          -> "report.pdf.bkp"
//	"{{ $input.name | upper }}"      -> "ADA"
//	"{{ $input.name | lower }}"      -> "ada"
//	"{{ $input.name | trim }}"       -> surrounding whitespace removed
//	"{{ $input.p | trimPrefix:/tmp/ }}" -> prefix stripped
//	"{{ $input.p | trimSuffix:.tmp }}"  -> suffix stripped
//	"{{ $input.s | replace:a:b }}"   -> ReplaceAll
//	"{{ $input.missing | default:n/a }}" -> fallback for missing/null
//	"{{ \"hi\" | upper }}"            -> "HI"
//
// A value that is exactly one template with no filters keeps the
// referenced native type (number, boolean, object, ...); filtered or
// embedded templates always produce strings. Filter args containing
// `|` or `:` must be quoted. Templates expand before bare
// `$input.<path>` references, so both can mix in one value.
var templatePattern = regexp.MustCompile(`\{\{(.+?)\}\}`)

// singleTemplate reports whether s is exactly one template and returns
// its inner expression.
func singleTemplate(s string) (string, bool) {
	locs := templatePattern.FindAllStringSubmatchIndex(s, -1)
	if len(locs) != 1 {
		return "", false
	}
	if locs[0][0] != 0 || locs[0][1] != len(s) {
		return "", false
	}
	return s[locs[0][2]:locs[0][3]], true
}

// expandTemplates replaces every `{{ ... }}` in s with its string form.
func expandTemplates(s string, input *common.ResultData) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	if !templatePattern.MatchString(s) {
		return "", fmt.Errorf("invalid template in %q: unclosed \"{{\"", s)
	}
	var evalErr error
	out := templatePattern.ReplaceAllStringFunc(s, func(m string) string {
		if evalErr != nil {
			return m
		}
		v, err := evalTemplate(m[2:len(m)-2], input)
		if err != nil {
			evalErr = err
			return m
		}
		return templateString(v)
	})
	if evalErr != nil {
		return "", evalErr
	}
	return out, nil
}

// evalTemplate evaluates one inner `{{ ... }}` expression.
func evalTemplate(inner string, input *common.ResultData) (any, error) {
	expr := strings.TrimSpace(inner)
	if expr == "" {
		return "", fmt.Errorf("empty template {{}}")
	}
	stages := splitUnquoted(expr, '|')
	first := strings.TrimSpace(stages[0])
	rest := stages[1:]

	var current any
	if strings.HasPrefix(first, "$") {
		if !strings.HasPrefix(first, "$input.") {
			return "", fmt.Errorf("unsupported reference in {{%s}}: only $input. paths and quoted literals allowed", inner)
		}
		val, err := resolveInputRef(first, input)
		if err != nil || val == nil {
			def, after, ok := findDefault(rest)
			if !ok {
				if err != nil {
					return "", err
				}
				return nil, nil
			}
			current = def
			rest = after
		} else {
			current = val
		}
	} else if isQuoted(first) {
		current = unquoteArg(first)
	} else {
		return "", fmt.Errorf("unsupported expression in {{%s}}: use $input.<path> or a quoted literal", inner)
	}

	for _, st := range rest {
		name, args, err := parseFilter(st, inner)
		if err != nil {
			return "", err
		}
		current, err = applyFilter(name, args, current, inner)
		if err != nil {
			return "", err
		}
	}
	return current, nil
}

func resolveInputRef(ref string, input *common.ResultData) (any, error) {
	if input == nil {
		return nil, fmt.Errorf("no input result available to resolve %q", ref)
	}
	return resolveResultPath(input.Payload, ref)
}

// findDefault scans the filter chain for a `default` filter, returning
// its fallback value and the stages following it.
func findDefault(stages []string) (string, []string, bool) {
	for i, st := range stages {
		name, args, err := parseFilter(st, "")
		if err != nil || name != "default" {
			continue
		}
		if len(args) != 1 {
			continue
		}
		return args[0], stages[i+1:], true
	}
	return "", nil, false
}

func parseFilter(stage, inner string) (string, []string, error) {
	parts := splitUnquoted(stage, ':')
	name := strings.ToLower(strings.TrimSpace(parts[0]))
	if name == "" {
		return "", nil, fmt.Errorf("invalid filter in {{%s}}: empty filter name", inner)
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", nil, fmt.Errorf("invalid filter in {{%s}}: bad filter name %q", inner, parts[0])
		}
	}
	args := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		args = append(args, unquoteArg(strings.TrimSpace(p)))
	}
	return name, args, nil
}

func applyFilter(name string, args []string, value any, inner string) (any, error) {
	fail := func(msg string) (any, error) {
		return "", fmt.Errorf("invalid filter in {{%s}}: %s", inner, msg)
	}
	// `default` with a present value is a no-op.
	if name == "default" {
		if len(args) != 1 {
			return fail(`default needs one value, e.g. | default:"n/a"`)
		}
		return value, nil
	}
	s := templateString(value)
	switch name {
	case "upper", "uppercase":
		if len(args) != 0 {
			return fail("upper takes no arguments")
		}
		return strings.ToUpper(s), nil
	case "lower", "lowercase":
		if len(args) != 0 {
			return fail("lower takes no arguments")
		}
		return strings.ToLower(s), nil
	case "trim":
		if len(args) > 1 {
			return fail("trim takes at most one cutset argument")
		}
		if len(args) == 0 {
			return strings.TrimSpace(s), nil
		}
		return strings.Trim(s, args[0]), nil
	case "trimprefix":
		if len(args) != 1 {
			return fail(`trimPrefix needs one value, e.g. | trimPrefix:"/tmp/"`)
		}
		return strings.TrimPrefix(s, args[0]), nil
	case "trimsuffix":
		if len(args) != 1 {
			return fail(`trimSuffix needs one value, e.g. | trimSuffix:".bkp"`)
		}
		return strings.TrimSuffix(s, args[0]), nil
	case "replace":
		if len(args) != 2 {
			return fail(`replace needs two values, e.g. | replace:"a":"b"`)
		}
		return strings.ReplaceAll(s, args[0], args[1]), nil
	default:
		return fail(fmt.Sprintf("unknown filter %q (supported: upper, lower, trim, trimPrefix, trimSuffix, replace, default)", name))
	}
}

// splitUnquoted splits s on sep, ignoring separators inside single or
// double quotes (backslash escapes the next char inside double quotes).
func splitUnquoted(s string, sep rune) []string {
	var parts []string
	var cur strings.Builder
	var inSingle, inDouble bool
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case inSingle && r == '\'':
			inSingle = false
			cur.WriteRune(r)
		case inDouble && r == '\\':
			escaped = true
			cur.WriteRune(r)
		case inDouble && r == '"':
			inDouble = false
			cur.WriteRune(r)
		case !inSingle && !inDouble && r == '\'':
			inSingle = true
			cur.WriteRune(r)
		case !inSingle && !inDouble && r == '"':
			inDouble = true
			cur.WriteRune(r)
		case !inSingle && !inDouble && r == sep:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(parts, cur.String())
}

func isQuoted(s string) bool {
	if len(s) < 2 {
		return false
	}
	return (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')
}

// unquoteArg strips one layer of matching quotes. Double-quoted args
// support escapes; single-quoted args are literal.
func unquoteArg(s string) string {
	if !isQuoted(s) {
		return s
	}
	if s[0] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s[1 : len(s)-1]
}

// templateString renders a value for template output. Unlike plain
// stringification it keeps integral floats unexponentiated
// (23423532, not 2.3423532e+07).
func templateString(v any) string {
	if f, ok := v.(float64); ok {
		if !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) && math.Abs(f) < 1e21 {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return common.StringifyArgValue(v)
}

// insideJSONString reports whether pos sits inside a JSON string
// literal. It works by quote parity: JSON strings cannot contain an
// unescaped quote, so an odd count of unescaped quotes before pos means
// "inside". This correctly handles templates followed by more literal
// text, e.g. `"{{ $input.file }}.bkp"`.
func insideJSONString(tmpl string, pos int) bool {
	inStr := false
	escaped := false
	for i := 0; i < pos && i < len(tmpl); i++ {
		c := tmpl[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inStr {
			escaped = true
			continue
		}
		if c == '"' {
			inStr = !inStr
		}
	}
	return inStr
}

// expandJSONTemplates expands `{{ ... }}` templates inside a raw JSON
// template, staying quote-aware: a template inside a JSON string
// literal contributes escaped string content, while a bare template
// contributes the JSON encoding of its value (native types preserved
// when no filters apply).
func expandJSONTemplates(tmpl string, input *common.ResultData) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	matches := templatePattern.FindAllStringSubmatchIndex(tmpl, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("invalid template in json: unclosed \"{{\"")
	}
	var out strings.Builder
	pos := 0
	for _, m := range matches {
		v, err := evalTemplate(tmpl[m[2]:m[3]], input)
		if err != nil {
			return "", err
		}
		out.WriteString(tmpl[pos:m[0]])
		if insideJSONString(tmpl, m[0]) {
			raw, err := json.Marshal(templateString(v))
			if err != nil {
				return "", err
			}
			out.Write(raw[1 : len(raw)-1])
		} else {
			var raw []byte
			if s, ok := v.(string); ok {
				raw, err = json.Marshal(s)
			} else {
				raw, err = json.Marshal(v)
			}
			if err != nil {
				return "", err
			}
			out.Write(raw)
		}
		pos = m[1]
	}
	out.WriteString(tmpl[pos:])
	return out.String(), nil
}
