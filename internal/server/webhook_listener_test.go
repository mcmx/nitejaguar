package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

// The server detects webhook listeners from client heartbeats: a client
// advertises webhook_addr (empty = no listener), the server stores it, and
// GET /api/clients exposes it. A heartbeat without the field clears the
// stored value, so a client restarted without --webhook-addr stops
// advertising instead of lingering.
func TestWebhookListenerAdvertised(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := newTestAPI(t)
	addApiRoutes(api, s)

	joinToken := mintEnrollmentToken(t, db, "default", 0)
	resp := api.Post("/api/clients/register", map[string]any{
		"name":             "webhook-listener-probe",
		"enrollment_token": joinToken,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)
	auth := "Authorization: Bearer " + registered.Token

	findClient := func() (addr string, found bool) {
		readerAuth := ensureRoleToken(t, db, "default", database.RoleViewer)
		resp := api.Get("/api/clients", readerAuth)
		if resp.Code != http.StatusOK {
			t.Fatalf("clients status = %v, body = %s", resp.Code, resp.Body.String())
		}
		var out struct {
			Clients []struct {
				ID          string `json:"client_id"`
				WebhookAddr string `json:"webhook_addr"`
			} `json:"clients"`
		}
		decodeBody(t, strings.NewReader(resp.Body.String()), &out)
		for _, c := range out.Clients {
			if c.ID == registered.ClientID {
				return c.WebhookAddr, true
			}
		}
		return "", false
	}

	// Advertise a listener: stored and exposed.
	resp = api.Post("/api/clients/heartbeat", auth, map[string]any{
		"client_id": registered.ClientID, "webhook_addr": "127.0.0.1:8081",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %v, body = %s", resp.Code, resp.Body.String())
	}
	if addr, found := findClient(); !found || addr != "127.0.0.1:8081" {
		t.Fatalf("webhook_addr = %q, found = %v, want 127.0.0.1:8081", addr, found)
	}

	// Listener resolution for workflow nodes: explicit owner maps to the
	// registered client and reports its listener; broadcast counts them.
	var def workflow.Workflow
	const trigTargeted = "trigger_whooklistensat00001"
	const trigBroadcast = "trigger_whooklistensbr00001"
	if err := json.Unmarshal([]byte(webhookWorkflowJSON(
		"workflow_whooklistens000001", trigTargeted, "action_whooklistens000001", "",
	)), &def); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	n := def.Nodes[trigTargeted]
	n.Client = registered.ClientID
	def.Nodes[trigTargeted] = n
	bcastNode := def.Nodes[trigTargeted]
	bcastNode.Id = trigBroadcast
	bcastNode.Client = ""
	def.Nodes[trigBroadcast] = bcastNode

	info := s.webhookNodeInfo("default", def)
	targeted, ok := info[trigTargeted]
	if !ok || targeted.Owner != registered.ClientID || !targeted.HasListener ||
		targeted.ListenerAddr != "127.0.0.1:8081" || targeted.OwnerName != "webhook-listener-probe" {
		t.Fatalf("targeted webhook info = %+v, ok = %v", targeted, ok)
	}
	if bcast := info[trigBroadcast]; bcast.Owner != "" || bcast.ListenerCount < 1 {
		t.Fatalf("broadcast webhook info = %+v, want owner empty and >= 1 listener", bcast)
	}

	// Heartbeat without the field clears the advertisement (client
	// restarted without --webhook-addr): owner resolves, listener gone.
	resp = api.Post("/api/clients/heartbeat", auth, map[string]any{
		"client_id": registered.ClientID,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %v, body = %s", resp.Code, resp.Body.String())
	}
	if addr, found := findClient(); !found || addr != "" {
		t.Fatalf("webhook_addr after clear = %q, found = %v, want empty", addr, found)
	}
	if cleared := s.webhookNodeInfo("default", def)[trigTargeted]; cleared.HasListener {
		t.Fatalf("targeted webhook info after clear = %+v, want no listener", cleared)
	}
}

// Host info travels the same heartbeat path: a client reporting
// os/arch/hostname/ips has it stored and exposed via GET /api/clients,
// while a heartbeat without host_info leaves the stored value alone.
func TestHostInfoAdvertised(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := newTestAPI(t)
	addApiRoutes(api, s)

	joinToken := mintEnrollmentToken(t, db, "default", 0)
	resp := api.Post("/api/clients/register", map[string]any{
		"name":             "host-info-probe",
		"enrollment_token": joinToken,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)
	auth := "Authorization: Bearer " + registered.Token
	readerAuth := ensureRoleToken(t, db, "default", database.RoleViewer)

	findHost := func() (map[string]any, bool) {
		resp := api.Get("/api/clients", readerAuth)
		if resp.Code != http.StatusOK {
			t.Fatalf("clients status = %v, body = %s", resp.Code, resp.Body.String())
		}
		var out struct {
			Clients []struct {
				ID   string         `json:"client_id"`
				Host map[string]any `json:"host_info"`
			} `json:"clients"`
		}
		decodeBody(t, strings.NewReader(resp.Body.String()), &out)
		for _, c := range out.Clients {
			if c.ID == registered.ClientID {
				return c.Host, true
			}
		}
		return nil, false
	}

	resp = api.Post("/api/clients/heartbeat", auth, map[string]any{
		"client_id": registered.ClientID,
		"host_info": map[string]any{
			"os": "linux", "arch": "amd64", "hostname": "edge-9",
			"ips": []string{"10.0.0.9", "fe80::1"},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %v, body = %s", resp.Code, resp.Body.String())
	}
	host, found := findHost()
	if !found || host["os"] != "linux" || host["arch"] != "amd64" ||
		host["hostname"] != "edge-9" {
		t.Fatalf("host_info = %v, found = %v", host, found)
	}
	ips, _ := host["ips"].([]any)
	if len(ips) != 2 || ips[0] != "10.0.0.9" || ips[1] != "fe80::1" {
		t.Fatalf("host ips = %v, want [10.0.0.9 fe80::1]", host["ips"])
	}

	// A heartbeat without host_info preserves the stored value.
	resp = api.Post("/api/clients/heartbeat", auth, map[string]any{
		"client_id": registered.ClientID,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %v, body = %s", resp.Code, resp.Body.String())
	}
	if host, found := findHost(); !found || host["hostname"] != "edge-9" {
		t.Fatalf("host_info after plain heartbeat = %v, found = %v, want preserved", host, found)
	}
}

// The /clients page shows the advertised listener, host summary, and IP
// addresses per client.
func TestClientsPageShowsHostInfo(t *testing.T) {
	var buf bytes.Buffer
	data := &web.ClientsPageData{Clients: []web.ClientView{{
		ID: "client_hostinfo000001", Name: "edge-9", TenantID: "default",
		Online: true, WebhookAddr: "127.0.0.1:8081",
		Host: common.HostInfo{OS: "linux", Arch: "arm64", Hostname: "edge-9", IPs: []string{"10.0.0.9", "fe80::1"}},
	}}}
	if err := web.ClientsPage(data).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render clients: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"Webhook listener:", "127.0.0.1:8081 (on)",
		"linux/arm64 @edge-9", "10.0.0.9", "fe80::1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("clients page missing %q", want)
		}
	}
}

// The designer payload carries registered clients (with listener addrs)
// so the inspector can warn about owners running no listener.
func TestDesignerInitCarriesClientListeners(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	h := s.RegisterRoutes()

	c, _, err := db.RegisterClient("designer-hook-probe", nil, "default")
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	if err := db.SetClientWebhookAddr(c.ID, "127.0.0.1:8082"); err != nil {
		t.Fatalf("set webhook addr: %v", err)
	}
	admin, err := db.CreateUser("default", fmt.Sprintf("designer-hook-admin-%d", testAdminSeq.Add(1)), "password-12345", "admin", nil)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	_, sessionPlain, err := db.CreateSession(admin.ID, 0)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/designer", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionPlain})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /designer status = %v", rec.Code)
	}
	init := extractDesignerInitial(t, rec.Body.String())
	rawNodes, _ := json.Marshal(init["clients"])
	if !strings.Contains(string(rawNodes), c.ID) || !strings.Contains(string(rawNodes), "127.0.0.1:8082") {
		t.Fatalf("designer init clients = %s, want probe client with listener addr", rawNodes)
	}
}
