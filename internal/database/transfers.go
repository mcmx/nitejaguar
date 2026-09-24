package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/ent/transferchunk"
	"github.com/mcmx/nitejaguar/ent/transfersession"
	"github.com/mcmx/nitejaguar/ent/transfersignal"
	"go.jetify.com/typeid"
)

// Relay caps: chunks are small JSON-friendly blobs; sessions are capped
// so the relay stays a control-friendly fallback, not bulk storage.
const (
	MaxTransferChunkBytes = 64 * 1024
	MaxTransferChunks     = 512
	MaxTransferBytes      = int64(MaxTransferChunks) * int64(MaxTransferChunkBytes)
	MaxSignalBytes        = 64 * 1024
	MaxDialInfoBytes      = 4 * 1024
)

// Transfer participant checks live here so every caller (server routes,
// tests) gets identical tenant isolation.
func transferVisibleTo(row *ent.TransferSession, clientID string, tags []string, tenantID string) bool {
	if row.TenantID != "" && row.TenantID != "default" && tenantID != "" && tenantID != "default" && row.TenantID != tenantID {
		return false
	}
	if row.SenderClientID == clientID || row.ReceiverClientID == clientID {
		return true
	}
	if row.ReceiverClientID == "" && len(row.ReceiverTags) > 0 && len(tags) > 0 {
		set := make(map[string]struct{}, len(tags))
		for _, t := range tags {
			set[t] = struct{}{}
		}
		for _, t := range row.ReceiverTags {
			if _, ok := set[t]; ok {
				return true
			}
		}
	}
	return false
}

// InitTransferSession opens a delivery from sender to receiver. Size and
// sha256 come from the sender's local read, so the receiver (and the
// complete step) can verify end-to-end integrity.
func (s *service) InitTransferSession(tenantID, workflowID, executionID, nodeID, senderClientID, receiverClientID string, receiverTags []string, fileName, destinationFile, permissions string, size int64, sha256hex string) (*ent.TransferSession, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	if senderClientID == "" || destinationFile == "" {
		return nil, fmt.Errorf("sender_client_id and destination_file are required")
	}
	if size < 0 || size > MaxTransferBytes {
		return nil, fmt.Errorf("size %d out of range (max %d)", size, MaxTransferBytes)
	}
	tid, _ := typeid.WithPrefix("transfer")
	row, err := s.client.TransferSession.Create().
		SetID(tid.String()).
		SetTenantID(tenantID).
		SetWorkflowID(workflowID).
		SetExecutionID(executionID).
		SetNodeID(nodeID).
		SetSenderClientID(senderClientID).
		SetReceiverClientID(receiverClientID).
		SetReceiverTags(receiverTags).
		SetFileName(fileName).
		SetDestinationFile(destinationFile).
		SetPermissions(permissions).
		SetSize(size).
		SetSha256(strings.ToLower(sha256hex)).
		SetStatus("offered").
		Save(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to init transfer session: %w", err)
	}
	return row, nil
}

// GetTransferSession fetches a session by id (no auth check; callers
// enforce participant + tenant visibility).
func (s *service) GetTransferSession(id string) (*ent.TransferSession, error) {
	row, err := s.client.TransferSession.Get(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("transfer not found: %w", err)
	}
	return row, nil
}

// AppendTransferChunk stores one ordered chunk. Only the sender uploads;
// seq must be the next expected offset (no gaps, no overwrites).
func (s *service) AppendTransferChunk(transferID string, seq int, data []byte) (received int, err error) {
	if len(data) > MaxTransferChunkBytes {
		return 0, fmt.Errorf("chunk exceeds %d bytes", MaxTransferChunkBytes)
	}
	ctx := context.Background()
	row, err := s.client.TransferSession.Get(ctx, transferID)
	if err != nil {
		return 0, fmt.Errorf("transfer not found: %w", err)
	}
	if row.Status == "done" || row.Status == "failed" {
		return 0, fmt.Errorf("transfer is %s", row.Status)
	}
	count, err := s.client.TransferChunk.Query().
		Where(transferchunk.TransferID(transferID)).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count chunks: %w", err)
	}
	if seq != count {
		return 0, fmt.Errorf("expected seq %d, got %d", count, seq)
	}
	if count >= MaxTransferChunks {
		return 0, fmt.Errorf("transfer exceeds %d chunks", MaxTransferChunks)
	}
	cid, _ := typeid.WithPrefix("tchunk")
	if err := s.client.TransferChunk.Create().
		SetID(cid.String()).
		SetTransferID(transferID).
		SetSeq(seq).
		SetData(append([]byte(nil), data...)).
		Exec(ctx); err != nil {
		return 0, fmt.Errorf("failed to store chunk: %w", err)
	}
	stored, err := s.transferStoredBytes(ctx, transferID)
	if err != nil {
		return count + 1, err
	}
	status := "uploading"
	if stored >= row.Size && row.Size > 0 {
		status = "ready"
	} else if row.Size == 0 && len(data) == 0 {
		status = "ready"
	}
	_ = s.client.TransferSession.UpdateOneID(transferID).SetStatus(status).Exec(ctx)
	return count + 1, nil
}

func (s *service) transferStoredBytes(ctx context.Context, transferID string) (int64, error) {
	rows, err := s.client.TransferChunk.Query().
		Where(transferchunk.TransferID(transferID)).
		All(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, c := range rows {
		total += int64(len(c.Data))
	}
	return total, nil
}

// ListTransferChunks returns chunks from a sequence offset, ascending.
// Only participants may fetch (checked by callers via transferVisibleTo).
func (s *service) ListTransferChunks(transferID string, fromSeq int) ([]*ent.TransferChunk, error) {
	rows, err := s.client.TransferChunk.Query().
		Where(transferchunk.TransferID(transferID), transferchunk.SeqGTE(fromSeq)).
		Order(ent.Asc(transferchunk.FieldSeq)).
		All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list chunks: %w", err)
	}
	return rows, nil
}

// ListPendingTransfers returns sessions ready for a receiver: addressed
// to it (or tag-matched broadcast) in offered/uploading/ready state.
func (s *service) ListPendingTransfers(clientID string, tags []string, tenantID string) ([]*ent.TransferSession, error) {
	rows, err := s.client.TransferSession.Query().
		Where(transfersession.StatusIn("offered", "uploading", "ready")).
		Order(ent.Asc(transfersession.FieldCreatedAt)).
		All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list transfers: %w", err)
	}
	out := make([]*ent.TransferSession, 0)
	for _, r := range rows {
		if !transferVisibleTo(r, clientID, tags, tenantID) {
			continue
		}
		if r.SenderClientID == clientID && r.ReceiverClientID != clientID {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// CompleteTransferSession verifies reassembled relay bytes against the
// init-advertised sha256 and marks the session done. viaP2P skips byte
// verification (bytes moved off-relay; sha was verified by the receiver
// on the DataChannel) but still requires the receiver.
func (s *service) CompleteTransferSession(transferID, sha256hex string, viaP2P bool) (*ent.TransferSession, error) {
	ctx := context.Background()
	row, err := s.client.TransferSession.Get(ctx, transferID)
	if err != nil {
		return nil, fmt.Errorf("transfer not found: %w", err)
	}
	if row.Status == "done" {
		return row, nil
	}
	if !viaP2P {
		rows, err := s.client.TransferChunk.Query().
			Where(transferchunk.TransferID(transferID)).
			Order(ent.Asc(transferchunk.FieldSeq)).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read chunks: %w", err)
		}
		h := sha256.New()
		var total int64
		for i, c := range rows {
			if c.Seq != i {
				return nil, fmt.Errorf("chunk gap at seq %d", i)
			}
			h.Write(c.Data)
			total += int64(len(c.Data))
		}
		if total != row.Size {
			return nil, fmt.Errorf("incomplete transfer: %d of %d bytes", total, row.Size)
		}
		if got := hex.EncodeToString(h.Sum(nil)); !equalHex(got, row.Sha256) || !equalHex(sha256hex, row.Sha256) {
			return nil, fmt.Errorf("sha256 mismatch")
		}
	}
	updated, err := s.client.TransferSession.UpdateOneID(transferID).
		SetStatus("done").
		SetViaP2p(viaP2P).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to complete transfer: %w", err)
	}
	return updated, nil
}

func equalHex(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// PostTransferSignal stores one WebRTC signaling message. Only session
// participants may signal (checked by callers).
func (s *service) PostTransferSignal(transferID, fromClientID, kind, payload string) (*ent.TransferSignal, error) {
	switch kind {
	case "offer", "answer", "ice", "bye":
	default:
		return nil, fmt.Errorf("unknown signal kind %q", kind)
	}
	if len(payload) > MaxSignalBytes {
		return nil, fmt.Errorf("signal exceeds %d bytes", MaxSignalBytes)
	}
	sid, _ := typeid.WithPrefix("tsig")
	row, err := s.client.TransferSignal.Create().
		SetID(sid.String()).
		SetTransferID(transferID).
		SetFromClientID(fromClientID).
		SetKind(kind).
		SetPayload(payload).
		Save(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to store signal: %w", err)
	}
	return row, nil
}

// ListTransferSignals returns signals for a session created after since
// (exclusive), oldest first. Callers filter out own messages as needed.
func (s *service) ListTransferSignals(transferID string, since time.Time) ([]*ent.TransferSignal, error) {
	rows, err := s.client.TransferSignal.Query().
		Where(transfersignal.TransferID(transferID), transfersignal.CreatedAtGT(since)).
		Order(ent.Asc(transfersignal.FieldCreatedAt)).
		All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list signals: %w", err)
	}
	return rows, nil
}

// SetClientDialInfo stores client-advertised P2P dial info (JSON blob the
// server never dials itself; peers use it to attempt direct WebRTC).
func (s *service) SetClientDialInfo(id, dialInfo string) error {
	if len(dialInfo) > MaxDialInfoBytes {
		return fmt.Errorf("dial_info exceeds %d bytes", MaxDialInfoBytes)
	}
	if err := s.client.RemoteClient.UpdateOneID(id).SetDialInfo(dialInfo).Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to store dial info: %w", err)
	}
	return nil
}

// TransferVisibleTo reports whether a client may access a session
// (participant + tenant). Exported for server route handlers.
func TransferVisibleTo(row *ent.TransferSession, clientID string, tags []string, tenantID string) bool {
	return transferVisibleTo(row, clientID, tags, tenantID)
}
