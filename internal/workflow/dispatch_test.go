package workflow

import (
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

// baseStore is a minimal common.WorkflowStore with no client-targeting
// capability.
type baseStore struct{}

func (baseStore) SaveWorkflow(string, string, string) error { return nil }
func (baseStore) ListWorkflowRecords(bool, bool) ([]common.WorkflowRecord, error) {
	return nil, nil
}
func (baseStore) SaveResultRecord(common.ResultData) error { return nil }
func (baseStore) EnqueueNodeAssignmentRecord(string, string, string, string, string, any) error {
	return nil
}
func (baseStore) CompleteNodeAssignment(string, string, string) error { return nil }
func (baseStore) LogAudit(string, string, string, string, string) error {
	return nil
}

// dispatchStore adds the optional common.ClientTargetLookup capability,
// mirroring a real store that can resolve client tags.
type dispatchStore struct {
	baseStore
	tags map[string][]string
}

func (d *dispatchStore) ClientTags(clientID string) ([]string, bool) {
	tags, ok := d.tags[clientID]
	return tags, ok
}

// plainStore omits ClientTags, so the engine must fall back to "tags
// unknown" and route tag-targeted nodes instead of returning them.
type plainStore struct{ baseStore }

func dispatchFixture() Workflow {
	return Workflow{
		Id: "workflow_dispatch",
		Nodes: map[string]Node{
			"broadcast":     {Id: "broadcast"},
			"gpu_only":      {Id: "gpu_only", ClientTags: []string{"gpu"}},
			"cpu_only":      {Id: "cpu_only", ClientTags: []string{"cpu"}},
			"client_pinned": {Id: "client_pinned", Client: "client_gpu"},
		},
	}
}

func sameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSet := map[string]bool{}
	for _, g := range got {
		gotSet[g] = true
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	if len(got) != len(wantSet) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for w := range wantSet {
		if !gotSet[w] {
			t.Fatalf("%s = %v, missing %q (want %v)", label, got, w, want)
		}
	}
}

// Every downstream node must land in exactly one bucket: owned by the
// reporting client, owned by the server, or routed to another client.
func TestDispatchNextsGivesEveryNodeOneOwner(t *testing.T) {
	def := dispatchFixture()
	store := &dispatchStore{tags: map[string][]string{
		"client_gpu": {"gpu"},
		"client_cpu": {"cpu"},
	}}
	wm := &workflowManager{executorID: "server", db: store}
	all := []string{"broadcast", "gpu_only", "cpu_only", "client_pinned"}

	for _, tc := range []struct {
		executor  string
		wantOwned []string
		wantRoute []string
	}{
		{"client_gpu", []string{"broadcast", "gpu_only", "client_pinned"}, []string{"cpu_only"}},
		{"client_cpu", []string{"broadcast", "cpu_only"}, []string{"gpu_only", "client_pinned"}},
	} {
		owned, serverOwned, routed := wm.dispatchNexts(def, all, tc.executor)
		sameSet(t, "clientOwned for "+tc.executor, owned, tc.wantOwned)
		sameSet(t, "routed for "+tc.executor, routed, tc.wantRoute)
		if serverOwned != nil {
			t.Fatalf("serverOwned for %s = %v, want none: a client-reported node is never the server's", tc.executor, serverOwned)
		}
		// Single-owner invariant: the buckets must be disjoint and cover
		// every computed next, so no node can be both returned and
		// enqueued (the double-execution bug).
		seen := map[string]bool{}
		for _, id := range append(append([]string{}, owned...), append(serverOwned, routed...)...) {
			if seen[id] {
				t.Fatalf("node %q dispatched twice for %s", id, tc.executor)
			}
			seen[id] = true
		}
		if len(seen) != len(all) {
			t.Fatalf("dispatch for %s covered %d of %d nexts", tc.executor, len(seen), len(all))
		}
	}
}

// Workflow-level default_client_tags apply when the node has no
// per-node override.
func TestDispatchNextsAppliesWorkflowDefaults(t *testing.T) {
	def := dispatchFixture()
	def.DefaultClientTags = []string{"gpu"}
	store := &dispatchStore{tags: map[string][]string{
		"client_gpu": {"gpu"},
		"client_cpu": {"cpu"},
	}}
	wm := &workflowManager{executorID: "server", db: store}

	owned, _, routed := wm.dispatchNexts(def, []string{"broadcast"}, "client_cpu")
	if len(owned) != 0 {
		t.Fatalf("client_cpu should not own the gpu-defaulted node: %v", owned)
	}
	sameSet(t, "routed", routed, []string{"broadcast"})

	owned, _, routed = wm.dispatchNexts(def, []string{"broadcast"}, "client_gpu")
	sameSet(t, "clientOwned", owned, []string{"broadcast"})
	if len(routed) != 0 {
		t.Fatalf("routed = %v, want none", routed)
	}
}

// Without the ClientTargetLookup capability the engine cannot verify tag
// targeting, so it routes those nodes (single-owner, one poll later)
// instead of guessing.
func TestDispatchNextsWithoutClientTagLookup(t *testing.T) {
	def := dispatchFixture()
	wm := &workflowManager{executorID: "server", db: &plainStore{}}

	owned, _, routed := wm.dispatchNexts(def, []string{"broadcast", "gpu_only", "client_pinned"}, "client_gpu")
	sameSet(t, "clientOwned", owned, []string{"broadcast", "client_pinned"})
	sameSet(t, "routed", routed, []string{"gpu_only"})
}

// A result produced by this server process is not a handoff: the server
// keeps its downstream nodes and enqueues nothing.
func TestDispatchNextsServerOwnedResultsStayLocal(t *testing.T) {
	def := dispatchFixture()
	wm := &workflowManager{executorID: "server", db: &dispatchStore{}}
	all := []string{"broadcast", "gpu_only"}

	for _, executor := range []string{"server", ""} {
		owned, serverOwned, routed := wm.dispatchNexts(def, all, executor)
		if len(owned) != 0 || len(routed) != 0 {
			t.Fatalf("executor %q: owned=%v routed=%v, want server-local only", executor, owned, routed)
		}
		sameSet(t, "serverOwned", serverOwned, all)
	}
}

// Nodes missing from the definition are routed so assignment polling
// retires them as stale, never executed locally.
func TestDispatchNextsUnknownNodeIsRouted(t *testing.T) {
	wm := &workflowManager{executorID: "server", db: &dispatchStore{}}
	owned, serverOwned, routed := wm.dispatchNexts(dispatchFixture(), []string{"ghost"}, "client_gpu")
	if len(owned) != 0 || len(serverOwned) != 0 {
		t.Fatalf("unknown node must not be executed: owned=%v serverOwned=%v", owned, serverOwned)
	}
	sameSet(t, "routed", routed, []string{"ghost"})
}
