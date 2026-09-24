package server

import (
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mcmx/nitejaguar/internal/database"
)

// Client-to-client transfer control plane (roadmap slice 6). The server is
// the signaling/control plane: sessions, WebRTC SDP/ICE store-forward,
// and the chunked relay fallback (the guaranteed path). Data moves P2P
// first; anything the P2P path cannot deliver moves through the relay.
// Every endpoint is client-token authed and tenant-isolated; file bytes
// in the relay are opaque to the server (no secret extraction).

type TransferSessionView struct {
	TransferID       string   `json:"transfer_id"`
	TenantID         string   `json:"tenant_id"`
	WorkflowID       string   `json:"workflow_id,omitempty"`
	ExecutionID      string   `json:"execution_id,omitempty"`
	NodeID           string   `json:"node_id,omitempty"`
	SenderClientID   string   `json:"sender_client_id"`
	ReceiverClientID string   `json:"receiver_client_id,omitempty"`
	ReceiverTags     []string `json:"receiver_tags,omitempty"`
	FileName         string   `json:"file_name,omitempty"`
	DestinationFile  string   `json:"destination_file"`
	Permissions      string   `json:"permissions,omitempty"`
	Size             int64    `json:"size"`
	SHA256           string   `json:"sha256"`
	Status           string   `json:"status"`
	ViaP2P           bool     `json:"via_p2p"`
}

type InitTransferInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	Body          struct {
		WorkflowID       string   `json:"workflow_id,omitempty"`
		ExecutionID      string   `json:"execution_id,omitempty"`
		NodeID           string   `json:"node_id,omitempty"`
		ReceiverClientID string   `json:"receiver_client_id,omitempty"`
		ReceiverTags     []string `json:"receiver_tags,omitempty"`
		FileName         string   `json:"file_name,omitempty"`
		DestinationFile  string   `json:"destination_file"`
		Permissions      string   `json:"permissions,omitempty"`
		Size             int64    `json:"size"`
		SHA256           string   `json:"sha256"`
	}
}

type InitTransferOutput struct {
	Body struct {
		TransferID string `json:"transfer_id"`
	}
}

type UploadChunkInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	ID            string `path:"id"`
	Body          struct {
		Seq     int    `json:"seq"`
		DataB64 string `json:"data_b64"`
	}
}

type UploadChunkOutput struct {
	Body struct {
		Received int `json:"received"`
	}
}

type FetchChunksInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	ID            string `path:"id"`
	FromSeq       int    `query:"from_seq"`
}

type ChunkView struct {
	Seq     int    `json:"seq"`
	DataB64 string `json:"data_b64"`
}

type FetchChunksOutput struct {
	Body struct {
		Chunks []ChunkView `json:"chunks"`
	}
}

type CompleteTransferInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	ID            string `path:"id"`
	Body          struct {
		SHA256 string `json:"sha256"`
		ViaP2P bool   `json:"via_p2p,omitempty"`
	}
}

type CompleteTransferOutput struct {
	Body struct {
		Ok     bool   `json:"ok"`
		Status string `json:"status"`
	}
}

type PendingTransfersInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	ID            string `path:"id"`
}

type PendingTransfersOutput struct {
	Body struct {
		ClientID  string                `json:"client_id"`
		Transfers []TransferSessionView `json:"transfers"`
	}
}

type PostSignalInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	ID            string `path:"id"`
	Body          struct {
		Kind    string `json:"kind"`
		Payload string `json:"payload"`
	}
}

type PostSignalOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

type ListSignalsInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	ID            string `path:"id"`
	Since         string `query:"since,omitempty"`
}

type SignalView struct {
	ID           string `json:"id"`
	FromClientID string `json:"from_client_id"`
	Kind         string `json:"kind"`
	Payload      string `json:"payload"`
	CreatedAt    string `json:"created_at"`
}

type ListSignalsOutput struct {
	Body struct {
		Signals []SignalView `json:"signals"`
	}
}

// transferCaller resolves the calling client (id, tenant, tags) from its
// token. All transfer endpoints share it.
func (s *Server) transferCaller(authorization, token string) (*clientInfo, bool) {
	id, ok := s.registry().identity(bearerToken(authorization, token))
	if !ok {
		return nil, false
	}
	c, ok := s.registry().getClient(id)
	if !ok {
		return nil, false
	}
	return c, true
}

func toTransferView(tenantID, id, workflowID, executionID, nodeID, sender, receiver string, tags []string, fileName, dst, perms string, size int64, sha, status string, viaP2P bool) TransferSessionView {
	return TransferSessionView{
		TransferID: id, TenantID: tenantID, WorkflowID: workflowID,
		ExecutionID: executionID, NodeID: nodeID, SenderClientID: sender,
		ReceiverClientID: receiver, ReceiverTags: tags, FileName: fileName,
		DestinationFile: dst, Permissions: perms, Size: size, SHA256: sha,
		Status: status, ViaP2P: viaP2P,
	}
}

// InitTransfer opens a delivery session. The sender advertises size+sha256
// from its local read; the receiver verifies end-to-end on completion.
func (s *Server) InitTransfer(_ context.Context, input *InitTransferInput) (*InitTransferOutput, error) {
	caller, ok := s.transferCaller(input.Authorization, input.Token)
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	b := input.Body
	if strings.TrimSpace(b.DestinationFile) == "" {
		return nil, huma.Error400BadRequest("destination_file is required")
	}
	if b.Size < 0 || b.Size > database.MaxTransferBytes {
		return nil, huma.Error400BadRequest("size out of range")
	}
	if b.ReceiverClientID != "" {
		if rc, ok := s.registry().getClient(b.ReceiverClientID); !ok || rc.TenantID != caller.TenantID {
			return nil, huma.Error400BadRequest("unknown receiver client")
		}
	}
	row, err := s.db.InitTransferSession(caller.TenantID, b.WorkflowID, b.ExecutionID, b.NodeID,
		caller.ID, b.ReceiverClientID, b.ReceiverTags, b.FileName, b.DestinationFile, b.Permissions, b.Size, b.SHA256)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("transfer.init", caller.TenantID, caller.ID, row.ID,
		"to="+(func() string {
			if b.ReceiverClientID != "" {
				return b.ReceiverClientID
			}
			return "tags:" + strings.Join(b.ReceiverTags, ",")
		}())+" size="+strconv.FormatInt(row.Size, 10))
	log.Printf("transfer init: id=%s from=%s tenant=%s size=%d", row.ID, caller.ID, caller.TenantID, row.Size)
	out := &InitTransferOutput{}
	out.Body.TransferID = row.ID
	return out, nil
}

// UploadChunk appends one ordered relay chunk. Sender only.
func (s *Server) UploadChunk(_ context.Context, input *UploadChunkInput) (*UploadChunkOutput, error) {
	caller, ok := s.transferCaller(input.Authorization, input.Token)
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	row, err := s.db.GetTransferSession(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if !database.TransferVisibleTo(row, caller.ID, caller.Tags, caller.TenantID) {
		return nil, huma.Error404NotFound("transfer not found")
	}
	if row.SenderClientID != caller.ID {
		return nil, huma.Error403Forbidden("only the sender uploads")
	}
	raw, err := base64.StdEncoding.DecodeString(input.Body.DataB64)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid data_b64")
	}
	received, err := s.db.AppendTransferChunk(input.ID, input.Body.Seq, raw)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	out := &UploadChunkOutput{}
	out.Body.Received = received
	return out, nil
}

// FetchChunks downloads relay chunks from an offset. Participants only.
func (s *Server) FetchChunks(_ context.Context, input *FetchChunksInput) (*FetchChunksOutput, error) {
	caller, ok := s.transferCaller(input.Authorization, input.Token)
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	row, err := s.db.GetTransferSession(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if !database.TransferVisibleTo(row, caller.ID, caller.Tags, caller.TenantID) {
		return nil, huma.Error404NotFound("transfer not found")
	}
	from := input.FromSeq
	if from < 0 {
		from = 0
	}
	rows, err := s.db.ListTransferChunks(input.ID, from)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list chunks")
	}
	out := &FetchChunksOutput{}
	out.Body.Chunks = make([]ChunkView, 0, len(rows))
	for _, c := range rows {
		out.Body.Chunks = append(out.Body.Chunks, ChunkView{Seq: c.Seq, DataB64: base64.StdEncoding.EncodeToString(c.Data)})
	}
	return out, nil
}

// CompleteTransfer marks delivery done after sha verification. Receiver
// only (sender-never-completes keeps the handshake two-sided).
func (s *Server) CompleteTransfer(_ context.Context, input *CompleteTransferInput) (*CompleteTransferOutput, error) {
	caller, ok := s.transferCaller(input.Authorization, input.Token)
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	row, err := s.db.GetTransferSession(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if !database.TransferVisibleTo(row, caller.ID, caller.Tags, caller.TenantID) {
		return nil, huma.Error404NotFound("transfer not found")
	}
	if row.SenderClientID == caller.ID && row.ReceiverClientID != caller.ID {
		return nil, huma.Error403Forbidden("only the receiver completes")
	}
	updated, err := s.db.CompleteTransferSession(input.ID, input.Body.SHA256, input.Body.ViaP2P)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("transfer.complete", caller.TenantID, caller.ID, input.ID,
		"via_p2p="+boolStr(input.Body.ViaP2P)+" sha="+updated.Sha256)
	log.Printf("transfer complete: id=%s by=%s via_p2p=%v", input.ID, caller.ID, input.Body.ViaP2P)
	out := &CompleteTransferOutput{}
	out.Body.Ok = true
	out.Body.Status = updated.Status
	return out, nil
}

// PendingTransfers is the receiver inbox: sessions addressed to the caller.
func (s *Server) PendingTransfers(_ context.Context, input *PendingTransfersInput) (*PendingTransfersOutput, error) {
	if !s.registry().authenticate(input.ID, bearerToken(input.Authorization, input.Token)) {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	caller, ok := s.registry().getClient(input.ID)
	if !ok {
		return nil, huma.Error404NotFound("client not found")
	}
	rows, err := s.db.ListPendingTransfers(caller.ID, caller.Tags, caller.TenantID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list transfers")
	}
	out := &PendingTransfersOutput{}
	out.Body.ClientID = caller.ID
	out.Body.Transfers = make([]TransferSessionView, 0, len(rows))
	for _, r := range rows {
		out.Body.Transfers = append(out.Body.Transfers, toTransferView(
			r.TenantID, r.ID, r.WorkflowID, r.ExecutionID, r.NodeID,
			r.SenderClientID, r.ReceiverClientID, r.ReceiverTags, r.FileName,
			r.DestinationFile, r.Permissions, r.Size, r.Sha256, r.Status, r.ViaP2p))
	}
	return out, nil
}

// PostSignal stores one WebRTC signaling message (offer/answer/ice/bye).
func (s *Server) PostSignal(_ context.Context, input *PostSignalInput) (*PostSignalOutput, error) {
	caller, ok := s.transferCaller(input.Authorization, input.Token)
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	row, err := s.db.GetTransferSession(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if !database.TransferVisibleTo(row, caller.ID, caller.Tags, caller.TenantID) {
		return nil, huma.Error404NotFound("transfer not found")
	}
	if _, err := s.db.PostTransferSignal(input.ID, caller.ID, input.Body.Kind, input.Body.Payload); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("transfer.signal", caller.TenantID, caller.ID, input.ID, "kind="+input.Body.Kind)
	out := &PostSignalOutput{}
	out.Body.Ok = true
	return out, nil
}

// ListSignals polls signaling messages since an RFC3339 timestamp (or all).
func (s *Server) ListSignals(_ context.Context, input *ListSignalsInput) (*ListSignalsOutput, error) {
	caller, ok := s.transferCaller(input.Authorization, input.Token)
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	row, err := s.db.GetTransferSession(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if !database.TransferVisibleTo(row, caller.ID, caller.Tags, caller.TenantID) {
		return nil, huma.Error404NotFound("transfer not found")
	}
	var since time.Time
	if strings.TrimSpace(input.Since) != "" {
		if t, err := time.Parse(time.RFC3339Nano, input.Since); err == nil {
			since = t
		} else if t, err := time.Parse(time.RFC3339, input.Since); err == nil {
			since = t
		}
	}
	rows, err := s.db.ListTransferSignals(input.ID, since)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list signals")
	}
	out := &ListSignalsOutput{}
	out.Body.Signals = make([]SignalView, 0, len(rows))
	for _, r := range rows {
		out.Body.Signals = append(out.Body.Signals, SignalView{
			ID: r.ID, FromClientID: r.FromClientID, Kind: r.Kind,
			Payload: r.Payload, CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return out, nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

var _ = http.MethodGet
