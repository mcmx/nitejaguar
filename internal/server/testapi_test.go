package server

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
)

// quietTB wraps *testing.T to swallow humatest's per-request full
// request/response dumps ("Making request:" / "Got response:"). Those
// multi-line bodies flood `go test -v` output while adding nothing on
// success; failures still surface via the assertions' own messages,
// which include status codes and response bodies.
type quietTB struct {
	*testing.T
}

func (quietTB) Log(args ...any)                 {}
func (quietTB) Logf(format string, args ...any) {}

// newTestAPI builds a humatest API with quiet logging. Prefer it over
// humatest.New(t) in all server tests.
func newTestAPI(t *testing.T) (http.Handler, humatest.TestAPI) {
	t.Helper()
	return humatest.New(quietTB{T: t})
}
