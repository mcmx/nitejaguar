package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
