package common

import (
	"testing"
	"time"
)

func TestFormatTimestamp(t *testing.T) {
	when := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	cases := []struct {
		spec string
		want string
	}{
		{"", when.Format(time.RFC3339)},
		{"rfc3339", when.Format(time.RFC3339)},
		{"ISO8601", when.Format(time.RFC3339)},
		{"unix", "1772600767"},
		{"unix_ms", "1772600767000"},
		{"unix_milli", "1772600767000"},
		{"2006-01-02", "2026-03-04"},
		{"15:04:05", "05:06:07"},
	}
	for _, c := range cases {
		if got := FormatTimestamp(when, c.spec); got != c.want {
			t.Errorf("FormatTimestamp(%q) = %q, want %q", c.spec, got, c.want)
		}
	}
}

func TestEffectiveTimestampFormat(t *testing.T) {
	if got := EffectiveTimestampFormat(""); got != time.RFC3339 {
		t.Errorf("EffectiveTimestampFormat(\"\") = %q, want RFC3339", got)
	}
	for _, spec := range []string{"unix", "2006-01-02"} {
		if got := EffectiveTimestampFormat(spec); got != spec {
			t.Errorf("EffectiveTimestampFormat(%q) = %q, want %q", spec, got, spec)
		}
	}
}
