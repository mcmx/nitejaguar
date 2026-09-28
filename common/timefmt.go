package common

import (
	"strconv"
	"strings"
	"time"
)

// FormatTimestamp formats t with a user-supplied format spec, shared by every
// node that exposes a timestamp to the workflow (currently the datetime action
// and the cron trigger, so both produce byte-identical payloads for the same
// spec).
//
// Supported specs:
//   - "", "iso8601", "rfc3339" → time.RFC3339
//   - "unix"                   → epoch seconds
//   - "unix_ms" ("unixms", "unix_milli") → epoch milliseconds
//   - anything else            → t.Format(spec) (a Go time layout)
func FormatTimestamp(t time.Time, spec string) string {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "", "iso8601", "rfc3339":
		return t.Format(time.RFC3339)
	case "unix":
		return strconv.FormatInt(t.Unix(), 10)
	case "unix_ms", "unixms", "unix_milli":
		return strconv.FormatInt(t.UnixMilli(), 10)
	default:
		return t.Format(spec)
	}
}

// EffectiveTimestampFormat is the spec echoed back in result payloads: the
// empty spec is reported as its concrete default (RFC3339), everything else
// verbatim so "unix"/"unix_ms"/custom layouts stay visible downstream.
func EffectiveTimestampFormat(spec string) string {
	if spec == "" {
		return time.RFC3339
	}
	return spec
}
