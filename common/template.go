package common

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Template expressions (`{{ ... }}`) shared by every core action.
//
// A template holds a `$input.<path>` reference (or a quoted literal)
// followed by an optional `|` filter chain:
//
//	"{{ $input.file }}.bkp"          -> "report.pdf.bkp"
//	"{{ $input.name | upper }}"      -> "ADA"
//	"{{ $input.missing | default:n/a }}" -> "n/a"
//
// Supported filters: upper (alias uppercase), lower (lowercase), trim
// (optional cutset), trimPrefix, trimSuffix, replace (old:new),
// default (fallback for missing/null values; later filters still
// apply). Filters chain left to right and are Unicode-aware.
//
// A value that is exactly one filter-free template keeps the
// referenced native type; filtered or embedded templates produce
// strings. Filter args containing `|` or `:` must be quoted.

// RefLookup resolves a full "$input.<path>" reference to its native
// value.
type RefLookup func(ref string) (any, error)

// PayloadLookup returns a RefLookup resolving against payload.
func PayloadLookup(payload any) RefLookup {
	return func(ref string) (any, error) {
		return ResolveRefPath(payload, ref)
	}
}

// InputLookup returns a RefLookup against an upstream result payload,
// failing when no result is available.
func InputLookup(input *ResultData) RefLookup {
	if input == nil {
		return func(ref string) (any, error) {
			return nil, fmt.Errorf("no input result available to resolve %q", ref)
		}
	}
	return PayloadLookup(input.Payload)
}

var templatePattern = regexp.MustCompile(`\{\{(.+?)\}\}`)
var refPattern = regexp.MustCompile(`\$input\.[A-Za-z0-9_.\[\]]+`)

// HasTemplate reports whether s contains at least one `{{...}}`
// placeholder.
func HasTemplate(s string) bool {
	return templatePattern.MatchString(s)
}

// SplitTemplates splits s on `{{...}}` placeholders, returning
// alternating literal and inner segments: even indices are literal
// text, odd indices are template inners (braces stripped, trimmed).
// A string without placeholders yields a single literal segment.
// Splitting (instead of ordered passes) keeps bare `$input.` refs and
// templates from consuming each other when adjacent, e.g.
// `$input.now{{ext}}` splits into `$input.now` + `ext`.
func SplitTemplates(s string) []string {
	locs := templatePattern.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return []string{s}
	}
	segs := make([]string, 0, 2*len(locs)+1)
	pos := 0
	for _, loc := range locs {
		segs = append(segs, s[pos:loc[0]], strings.TrimSpace(s[loc[2]:loc[3]]))
		pos = loc[1]
	}
	return append(segs, s[pos:])
}

// SingleTemplate reports whether s is exactly one template and returns
// its inner expression.
func SingleTemplate(s string) (string, bool) {
	locs := templatePattern.FindAllStringSubmatchIndex(s, -1)
	if len(locs) != 1 {
		return "", false
	}
	if locs[0][0] != 0 || locs[0][1] != len(s) {
		return "", false
	}
	return s[locs[0][2]:locs[0][3]], true
}

// ExpandTemplates replaces every `{{ ... }}` in s with its string form.
func ExpandTemplates(s string, lookup RefLookup) (string, error) {
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
		v, err := EvalTemplate(m[2:len(m)-2], lookup)
		if err != nil {
			evalErr = err
			return m
		}
		return TemplateString(v)
	})
	if evalErr != nil {
		return "", evalErr
	}
	return out, nil
}

// EvalTemplate evaluates one inner `{{ ... }}` expression.
func EvalTemplate(inner string, lookup RefLookup) (any, error) {
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
		val, err := lookup(first)
		if err != nil || val == nil {
			def, after, ok := findDefaultFilter(rest)
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
		name, args, err := parseTemplateFilter(st, inner)
		if err != nil {
			return "", err
		}
		current, err = applyTemplateFilter(name, args, current, inner)
		if err != nil {
			return "", err
		}
	}
	return current, nil
}

// findDefaultFilter scans the filter chain for a `default` filter,
// returning its fallback value and the stages following it.
func findDefaultFilter(stages []string) (string, []string, bool) {
	for i, st := range stages {
		name, args, err := parseTemplateFilter(st, "")
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

func parseTemplateFilter(stage, inner string) (string, []string, error) {
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

func applyTemplateFilter(name string, args []string, value any, inner string) (any, error) {
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
	s := TemplateString(value)
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

// TemplateString renders a value for template output. Unlike plain
// stringification it keeps integral floats unexponentiated
// (23423532, not 2.3423532e+07).
func TemplateString(v any) string {
	if f, ok := v.(float64); ok {
		if !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) && math.Abs(f) < 1e21 {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return StringifyArgValue(v)
}

// ExpandJSONTemplates expands `{{ ... }}` templates inside a raw JSON
// template, staying quote-aware: a template inside a JSON string
// literal contributes escaped string content, while a bare template
// contributes the JSON encoding of its value (native types preserved
// when no filters apply).
func ExpandJSONTemplates(tmpl string, lookup RefLookup) (string, error) {
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
		v, err := EvalTemplate(tmpl[m[2]:m[3]], lookup)
		if err != nil {
			return "", err
		}
		out.WriteString(tmpl[pos:m[0]])
		if insideJSONString(tmpl, m[0]) {
			raw, err := json.Marshal(TemplateString(v))
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

// ExpandJSONRefs resolves bare `$input.<path>` references in a raw JSON
// template. Each reference is replaced by the JSON encoding of the
// referenced value, except inside a JSON string literal
// (`"$input.name"`), where the plain string form is substituted so the
// template stays valid JSON. Bare references keep native types
// (numbers, booleans, objects, arrays).
func ExpandJSONRefs(tmpl string, lookup RefLookup) (string, error) {
	matches := refPattern.FindAllStringIndex(tmpl, -1)
	if len(matches) == 0 {
		return tmpl, nil
	}
	var out strings.Builder
	pos := 0
	for _, m := range matches {
		val, err := lookup(tmpl[m[0]:m[1]])
		if err != nil {
			return "", err
		}
		out.WriteString(tmpl[pos:m[0]])
		if insideJSONString(tmpl, m[0]) {
			var s string
			if str, ok := val.(string); ok {
				s = str
			} else if raw, merr := json.Marshal(val); merr == nil && isJSONScalar(raw) {
				// Numbers/bools/null render in JSON form
				// (23423532, not 2.3423532e+07) inside the
				// surrounding quotes.
				out.Write(raw)
				pos = m[1]
				continue
			} else {
				s = StringifyArgValue(val)
			}
			// Reuse the JSON string escaper, minus the surrounding
			// quotes, so values with quotes or newlines stay valid.
			raw, err := json.Marshal(s)
			if err != nil {
				return "", err
			}
			out.Write(raw[1 : len(raw)-1])
		} else {
			raw, err := json.Marshal(val)
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

// isJSONScalar reports whether raw JSON is a number, boolean, or null
// (values that embed cleanly inside a surrounding string literal).
func isJSONScalar(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case '{', '[', '"':
		return false
	default:
		return true
	}
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
