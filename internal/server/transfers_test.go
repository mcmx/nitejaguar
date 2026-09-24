package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func testTransferServer(t *testing.T) (database.Service, humatest.TestAPI) {
	t.Helper()
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	s := &Server{db: db, wm: workflow.NewWorkflowManager(false, db)}
	_, api := humatest.New(t)
	addApiRoutes(api, s)
	return db, api
}

func registerTransferClient(t *testing.T, api humatest.TestAPI, db database.Service, name, tenant string, tags []string) (string, string) {
	t.Helper()
	joinToken := mintEnrollmentToken(t, db, tenant, 0)
	body := map[string]any{"name": name, "enrollment_token": joinToken}
	if len(tags) > 0 {
		body["tags"] = tags
	}
	resp := api.Post("/api/clients/register", body)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register %s status = %v, body = %s", name, resp.Code, resp.Body.String())
	}
	var out struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &out)
	return out.ClientID, out.Token
}

func TestTransferRelayRoundTrip(t *testing.T) {
	db, api := testTransferServer(t)
	senderID, senderTok := registerTransferClient(t, api, db, "sender", "default", nil)
	receiverID, receiverTok := registerTransferClient(t, api, db, "receiver", "default", nil)

	content := []byte("hello distributed world, bytes go here")
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	auth := func(tok string) string { return "Authorization: Bearer " + tok }

	// init
	resp := api.Post("/api/transfers/init", auth(senderTok), map[string]any{
		"receiver_client_id": receiverID,
		"file_name":          "hello.txt",
		"destination_file":   "~/received/hello.txt",
		"permissions":        "0644",
		"size":               len(content),
		"sha256":             sha,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("init status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var initOut struct {
		TransferID string `json:"transfer_id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &initOut)
	if !strings.HasPrefix(initOut.TransferID, "transfer_") {
		t.Fatalf("transfer id %q missing transfer_ prefix", initOut.TransferID)
	}

	// two ordered chunks
	mid := len(content) / 2
	for seq, part := range [][]byte{content[:mid], content[mid:]} {
		resp = api.Post("/api/transfers/"+initOut.TransferID+"/chunks", auth(senderTok), map[string]any{
			"seq": seq, "data_b64": base64.StdEncoding.EncodeToString(part),
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("chunk %d status = %v, body = %s", seq, resp.Code, resp.Body.String())
		}
	}
	// wrong seq rejected
	resp = api.Post("/api/transfers/"+initOut.TransferID+"/chunks", auth(senderTok), map[string]any{
		"seq": 0, "data_b64": base64.StdEncoding.EncodeToString([]byte("x")),
	})
	if resp.Code == http.StatusOK {
		t.Fatalf("duplicate seq accepted: %s", resp.Body.String())
	}

	// receiver inbox lists it
	resp = api.Get("/api/clients/"+receiverID+"/transfers/pending", auth(receiverTok))
	if resp.Code != http.StatusOK {
		t.Fatalf("pending status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var pending struct {
		Transfers []struct {
			TransferID      string `json:"transfer_id"`
			Status          string `json:"status"`
			DestinationFile string `json:"destination_file"`
			Size            int64  `json:"size"`
			SHA256          string `json:"sha256"`
		} `json:"transfers"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &pending)
	if len(pending.Transfers) != 1 || pending.Transfers[0].Status != "ready" {
		t.Fatalf("unexpected inbox: %s", resp.Body.String())
	}

	// sender must not see its own session in its inbox
	resp = api.Get("/api/clients/"+senderID+"/transfers/pending", auth(senderTok))
	var senderPending struct {
		Transfers []any `json:"transfers"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &senderPending)
	if len(senderPending.Transfers) != 0 {
		t.Fatalf("sender inbox should be empty: %s", resp.Body.String())
	}

	// fetch + reassemble
	resp = api.Get("/api/transfers/"+initOut.TransferID+"/chunks?from_seq=0", auth(receiverTok))
	if resp.Code != http.StatusOK {
		t.Fatalf("fetch status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var fetched struct {
		Chunks []struct {
			Seq     int    `json:"seq"`
			DataB64 string `json:"data_b64"`
		} `json:"chunks"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &fetched)
	var rebuilt []byte
	for _, c := range fetched.Chunks {
		raw, err := base64.StdEncoding.DecodeString(c.DataB64)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt = append(rebuilt, raw...)
	}
	if string(rebuilt) != string(content) {
		t.Fatalf("reassembled %q, want %q", rebuilt, content)
	}

	// wrong sha rejected, right sha completes
	resp = api.Post("/api/transfers/"+initOut.TransferID+"/complete", auth(receiverTok), map[string]any{
		"sha256": strings.Repeat("0", 64),
	})
	if resp.Code == http.StatusOK {
		t.Fatalf("bad sha accepted: %s", resp.Body.String())
	}
	resp = api.Post("/api/transfers/"+initOut.TransferID+"/complete", auth(receiverTok), map[string]any{
		"sha256": sha,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("complete status = %v, body = %s", resp.Code, resp.Body.String())
	}
	// done sessions leave the inbox
	resp = api.Get("/api/clients/"+receiverID+"/transfers/pending", auth(receiverTok))
	var after struct {
		Transfers []any `json:"transfers"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &after)
	if len(after.Transfers) != 0 {
		t.Fatalf("inbox should be empty after complete: %s", resp.Body.String())
	}
}

func TestTransferTenantIsolation(t *testing.T) {
	db, api := testTransferServer(t)
	_, senderTok := registerTransferClient(t, api, db, "sender", "default", nil)
	otherID, otherTok := registerTransferClient(t, api, db, "other", "othertenant", nil)
	auth := func(tok string) string { return "Authorization: Bearer " + tok }

	resp := api.Post("/api/transfers/init", auth(senderTok), map[string]any{
		"receiver_client_id": otherID, // cross-tenant receiver rejected
		"destination_file":   "/tmp/x",
		"size":               1,
		"sha256":             strings.Repeat("a", 64),
	})
	if resp.Code == http.StatusOK || resp.Code == http.StatusCreated {
		t.Fatalf("cross-tenant init accepted: %s", resp.Body.String())
	}

	resp = api.Post("/api/transfers/init", auth(senderTok), map[string]any{
		"destination_file": "/tmp/x",
		"size":             1,
		"sha256":           strings.Repeat("a", 64),
	})
	var initOut struct {
		TransferID string `json:"transfer_id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &initOut)

	// foreign tenant cannot fetch, signal, or list
	for _, target := range []string{
		"/api/transfers/" + initOut.TransferID + "/chunks?from_seq=0",
		"/api/transfers/" + initOut.TransferID + "/signal",
	} {
		resp = api.Get(target, auth(otherTok))
		if resp.Code == http.StatusOK {
			t.Fatalf("cross-tenant read accepted for %s", target)
		}
	}
	_ = otherID
}

func TestTransferSignalingRoundTrip(t *testing.T) {
	db, api := testTransferServer(t)
	senderID, senderTok := registerTransferClient(t, api, db, "sender", "default", nil)
	receiverID, receiverTok := registerTransferClient(t, api, db, "receiver", "default", nil)
	auth := func(tok string) string { return "Authorization: Bearer " + tok }

	resp := api.Post("/api/transfers/init", auth(senderTok), map[string]any{
		"receiver_client_id": receiverID,
		"destination_file":   "/tmp/x",
		"size":               1,
		"sha256":             strings.Repeat("b", 64),
	})
	var initOut struct {
		TransferID string `json:"transfer_id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &initOut)

	resp = api.Post("/api/transfers/"+initOut.TransferID+"/signal", auth(senderTok), map[string]any{
		"kind": "offer", "payload": `{"sdp":"offer-sdp"}`,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("offer status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Post("/api/transfers/"+initOut.TransferID+"/signal", auth(senderTok), map[string]any{
		"kind": "bogus", "payload": "x",
	})
	if resp.Code == http.StatusOK {
		t.Fatalf("bad signal kind accepted: %s", resp.Body.String())
	}
	resp = api.Get("/api/transfers/"+initOut.TransferID+"/signal", auth(receiverTok))
	var sigs struct {
		Signals []struct {
			Kind         string `json:"kind"`
			Payload      string `json:"payload"`
			FromClientID string `json:"from_client_id"`
		} `json:"signals"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &sigs)
	if len(sigs.Signals) != 1 || sigs.Signals[0].Kind != "offer" || sigs.Signals[0].FromClientID != senderID {
		t.Fatalf("unexpected signals: %s", resp.Body.String())
	}
	_ = receiverID
}

func TestHeartbeatDialInfo(t *testing.T) {
	db, api := testTransferServer(t)
	clientID, tok := registerTransferClient(t, api, db, "p2p-node", "default", nil)
	auth := "Authorization: Bearer " + tok

	resp := api.Post("/api/clients/heartbeat", auth, map[string]any{
		"client_id": clientID, "dial_info": `{"webrtc":true,"v":1}`,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/clients")
	var clients struct {
		Clients []struct {
			ID       string `json:"client_id"`
			DialInfo string `json:"dial_info"`
		} `json:"clients"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &clients)
	found := false
	for _, c := range clients.Clients {
		if c.ID == clientID {
			found = true
			if c.DialInfo != `{"webrtc":true,"v":1}` {
				t.Fatalf("dial_info = %q", c.DialInfo)
			}
		}
	}
	if !found {
		t.Fatalf("client missing from list: %s", resp.Body.String())
	}
}
