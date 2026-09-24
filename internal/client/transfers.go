package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	transferaction "github.com/mcmx/nitejaguar/internal/actions/transfer"
)

// Relay chunk size for uploads (base64-friendly, under the 64KiB server cap).
const relayChunkBytes = 60 * 1024

// TransferSession mirrors the server session view (metadata only, no bytes).
type TransferSession struct {
	TransferID       string   `json:"transfer_id"`
	TenantID         string   `json:"tenant_id"`
	WorkflowID       string   `json:"workflow_id"`
	ExecutionID      string   `json:"execution_id"`
	NodeID           string   `json:"node_id"`
	SenderClientID   string   `json:"sender_client_id"`
	ReceiverClientID string   `json:"receiver_client_id"`
	ReceiverTags     []string `json:"receiver_tags"`
	FileName         string   `json:"file_name"`
	DestinationFile  string   `json:"destination_file"`
	Permissions      string   `json:"permissions"`
	Size             int64    `json:"size"`
	SHA256           string   `json:"sha256"`
	Status           string   `json:"status"`
	ViaP2P           bool     `json:"via_p2p"`
}

// Signal mirrors one WebRTC signaling message.
type Signal struct {
	ID           string `json:"id"`
	FromClientID string `json:"from_client_id"`
	Kind         string `json:"kind"`
	Payload      string `json:"payload"`
	CreatedAt    string `json:"created_at"`
}

// InitTransfer opens a relay session as the sender.
func (a API) InitTransfer(ctx context.Context, workflowID, executionID, nodeID, receiverClientID string, receiverTags []string, fileName, destinationFile, permissions string, size int64, sha string) (string, error) {
	var out struct {
		TransferID string `json:"transfer_id"`
	}
	err := a.request(ctx, "POST", "/api/transfers/init", map[string]any{
		"workflow_id": workflowID, "execution_id": executionID, "node_id": nodeID,
		"receiver_client_id": receiverClientID, "receiver_tags": receiverTags,
		"file_name": fileName, "destination_file": destinationFile,
		"permissions": permissions, "size": size, "sha256": sha,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.TransferID == "" {
		return "", fmt.Errorf("empty transfer_id in init response")
	}
	return out.TransferID, nil
}

// UploadChunk appends one ordered relay chunk (sender only).
func (a API) UploadChunk(ctx context.Context, transferID string, seq int, data []byte) (int, error) {
	var out struct {
		Received int `json:"received"`
	}
	err := a.request(ctx, "POST", "/api/transfers/"+url.PathEscape(transferID)+"/chunks", map[string]any{
		"seq": seq, "data_b64": base64.StdEncoding.EncodeToString(data),
	}, &out)
	return out.Received, err
}

// FetchChunks downloads relay chunks from an offset.
func (a API) FetchChunks(ctx context.Context, transferID string, fromSeq int) ([]struct {
	Seq     int    `json:"seq"`
	DataB64 string `json:"data_b64"`
}, error) {
	var out struct {
		Chunks []struct {
			Seq     int    `json:"seq"`
			DataB64 string `json:"data_b64"`
		} `json:"chunks"`
	}
	err := a.request(ctx, "GET", "/api/transfers/"+url.PathEscape(transferID)+"/chunks?from_seq="+strconv.Itoa(fromSeq), nil, &out)
	return out.Chunks, err
}

// CompleteTransfer verifies sha and marks the session done (receiver).
func (a API) CompleteTransfer(ctx context.Context, transferID, sha string, viaP2P bool) error {
	var out struct {
		Ok     bool   `json:"ok"`
		Status string `json:"status"`
	}
	err := a.request(ctx, "POST", "/api/transfers/"+url.PathEscape(transferID)+"/complete", map[string]any{
		"sha256": sha, "via_p2p": viaP2P,
	}, &out)
	return err
}

// PendingTransfers lists inbound sessions for a client (receiver inbox).
func (a API) PendingTransfers(ctx context.Context, id string) ([]TransferSession, error) {
	var out struct {
		ClientID  string            `json:"client_id"`
		Transfers []TransferSession `json:"transfers"`
	}
	err := a.request(ctx, "GET", "/api/clients/"+url.PathEscape(id)+"/transfers/pending", nil, &out)
	return out.Transfers, err
}

// PostSignal stores one WebRTC signaling message.
func (a API) PostSignal(ctx context.Context, transferID, kind, payload string) error {
	var out struct {
		Ok bool `json:"ok"`
	}
	return a.request(ctx, "POST", "/api/transfers/"+url.PathEscape(transferID)+"/signal", map[string]any{
		"kind": kind, "payload": payload,
	}, &out)
}

// PollSignals lists signaling messages since an RFC3339 timestamp.
func (a API) PollSignals(ctx context.Context, transferID string, since time.Time) ([]Signal, error) {
	var out struct {
		Signals []Signal `json:"signals"`
	}
	path := "/api/transfers/" + url.PathEscape(transferID) + "/signal"
	if !since.IsZero() {
		path += "?since=" + url.QueryEscape(since.UTC().Format(time.RFC3339Nano))
	}
	err := a.request(ctx, "GET", path, nil, &out)
	return out.Signals, err
}

// HeartbeatWithDial reports liveness plus advertised P2P dial info.
func (a API) HeartbeatWithDial(ctx context.Context, id, dialInfo string) error {
	return a.request(ctx, "POST", "/api/clients/heartbeat", map[string]string{"client_id": id, "dial_info": dialInfo}, nil)
}

// UploadFileRelay streams a local file through the server relay in order.
// It returns size and sha256 (also advertised at init).
func (a API) UploadFileRelay(ctx context.Context, transferID, srcPath string) (int64, string, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	buf := make([]byte, relayChunkBytes)
	var total int64
	seq := 0
	for {
		n, rerr := io.ReadFull(f, buf)
		if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return 0, "", rerr
		}
		if n == 0 {
			break
		}
		chunk := append([]byte(nil), buf[:n]...)
		h.Write(chunk)
		if _, err := a.UploadChunk(ctx, transferID, seq, chunk); err != nil {
			return 0, "", fmt.Errorf("upload chunk %d: %w", seq, err)
		}
		seq++
		total += int64(n)
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
	}
	return total, hex.EncodeToString(h.Sum(nil)), nil
}

// DownloadFileRelay fetches all relay chunks and writes the destination
// with the transfer safety rules: no overwrite, MkdirAll parents,
// cross-OS basename guard, optional octal permissions (best-effort on
// Windows), sha256 verified before reporting success.
func (a API) DownloadFileRelay(ctx context.Context, sess TransferSession, expandedDst string) (string, error) {
	if err := transferaction.ValidateDestinationBase(expandedDst); err != nil {
		return "", err
	}
	if _, statErr := os.Stat(expandedDst); statErr == nil {
		return "", fmt.Errorf("destination already exists: %s", expandedDst)
	}
	if err := os.MkdirAll(filepath.Dir(expandedDst), 0o755); err != nil {
		return "", err
	}
	out, err := os.OpenFile(expandedDst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var total int64
	seq := 0
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()
	for {
		if ctx.Err() != nil {
			_ = out.Close()
			closed = true
			_ = os.Remove(expandedDst)
			return "", ctx.Err()
		}
		chunks, err := a.FetchChunks(ctx, sess.TransferID, seq)
		if err != nil {
			_ = out.Close()
			closed = true
			_ = os.Remove(expandedDst)
			return "", err
		}
		if len(chunks) == 0 {
			break
		}
		for _, c := range chunks {
			if c.Seq != seq {
				_ = out.Close()
				closed = true
				_ = os.Remove(expandedDst)
				return "", fmt.Errorf("chunk gap at seq %d", seq)
			}
			raw, err := base64.StdEncoding.DecodeString(c.DataB64)
			if err != nil {
				_ = out.Close()
				closed = true
				_ = os.Remove(expandedDst)
				return "", fmt.Errorf("invalid chunk %d: %w", seq, err)
			}
			if _, err := out.Write(raw); err != nil {
				_ = out.Close()
				closed = true
				_ = os.Remove(expandedDst)
				return "", err
			}
			h.Write(raw)
			total += int64(len(raw))
			seq++
		}
		if total >= sess.Size && sess.Size > 0 {
			break
		}
		if len(chunks) == 0 {
			break
		}
	}
	if err := out.Close(); err != nil {
		closed = true
		_ = os.Remove(expandedDst)
		return "", err
	}
	closed = true
	sum := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(sum, sess.SHA256) {
		_ = os.Remove(expandedDst)
		return "", fmt.Errorf("sha256 mismatch")
	}
	if err := applyRelayPermissions(expandedDst, sess.Permissions); err != nil {
		_ = os.Remove(expandedDst)
		return "", err
	}
	return sum, nil
}

func applyRelayPermissions(dst, requested string) error {
	if strings.TrimSpace(requested) == "" {
		return nil
	}
	cleaned := strings.TrimSpace(requested)
	cleaned = strings.TrimPrefix(cleaned, "0o")
	cleaned = strings.TrimPrefix(cleaned, "0O")
	v, err := strconv.ParseUint(cleaned, 8, 32)
	if err != nil || v > 0o777 {
		return fmt.Errorf("invalid permissions %q", requested)
	}
	if err := os.Chmod(dst, os.FileMode(v)); err != nil {
		// Best-effort on Windows where only the readonly bit applies.
		if runtime.GOOS != "windows" {
			return fmt.Errorf("cannot apply permissions %q: %v", requested, err)
		}
	}
	return nil
}
