package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/common"
	transferaction "github.com/mcmx/nitejaguar/internal/actions/transfer"
)

// Distributed transfer execution (roadmap slice 6): P2P first, server
// relay fallback. Semantics:
//   - A transfer node with no destination (no destination_client/tags)
//     or addressed to self executes locally (plain copy).
//   - A transfer node addressed elsewhere is intercepted: this runner
//     reads the source, opens a server session, attempts WebRTC P2P,
//     and falls back to relay upload. Its result advances the workflow
//     (downstream nexts run on the sender; cross-client compute handoff
//     still uses the assignment mechanism).
//   - Receivers poll the transfer inbox each tick, take P2P deliveries
//     offered to them, otherwise download the relay, write with the
//     transfer safety rules, and complete the session. Receivers post no
//     workflow result (the sender already did).

const (
	// transferMaxBytes mirrors the server relay cap
	// (MaxTransferChunks x MaxTransferChunkBytes).
	transferMaxBytes = 32 << 20
	transferP2PWait  = 10 * time.Second
	transferDialInfo = `{"webrtc":true,"v":1}`
)

// transferTargetFromArgs reads the receiver routing of a transfer node.
// Tags arrive as a comma-separated string (node arguments are strings).
func transferTargetFromArgs(actionName string, args map[string]string) transferTarget {
	if actionName != "transfer" || len(args) == 0 {
		return transferTarget{}
	}
	var tags []string
	if raw := strings.TrimSpace(args["destination_client_tags"]); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	return transferTarget{destClient: strings.TrimSpace(args["destination_client"]), destTags: tags}
}

// isRemoteTransfer reports whether the sender flow (not a local copy)
// handles this node. Tag-only targets are always remote: runners carry
// no tags, so any tag match resolves server-side to other clients.
func isRemoteTransfer(t transferTarget, selfID string) bool {
	if t.destClient != "" {
		return t.destClient != selfID
	}
	return len(t.destTags) > 0
}

// executeNode runs a node, intercepting remote transfer deliveries.
func (r *runner) executeNode(nodeID, executionID string, inputs []any) {
	r.mu.Lock()
	cfg := r.nodeMeta[nodeID]
	a := r.nodeAction[nodeID]
	self := r.selfID
	r.mu.Unlock()
	if a == nil {
		return
	}
	if cfg.actionName == "transfer" && isRemoteTransfer(cfg.transferDest, self) {
		go r.runTransferSender(nodeID, executionID, inputs)
		return
	}
	go a.Execute(executionID, inputs)
}

func firstResult(inputs []any) *common.ResultData {
	for _, in := range inputs {
		switch v := in.(type) {
		case common.ResultData:
			c := v
			return &c
		case *common.ResultData:
			if v != nil {
				return v
			}
		}
	}
	return nil
}

// runTransferSender reads the source file, opens a session, attempts P2P,
// falls back to relay upload, and reports the result into the event loop.
func (r *runner) runTransferSender(nodeID, executionID string, inputs []any) {
	r.mu.Lock()
	workflowID := r.nodeWorkflow[nodeID]
	action := r.nodeAction[nodeID]
	self := r.selfID
	r.mu.Unlock()

	fail := func(msg string, fields map[string]any) {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["type"] = "error"
		fields["result"] = msg
		r.events <- common.ResultData{
			WorkflowID: workflowID, ExecutionID: executionID,
			ActionID: nodeID, ActionType: "action", ActionName: "transfer",
			Payload: fields,
		}
	}
	if action == nil {
		fail("unknown transfer node", nil)
		return
	}
	rawArgs, err := common.ArgsToStringMap(action.GetArgs().Args)
	if err != nil {
		fail(err.Error(), nil)
		return
	}
	trigger := firstResult(inputs)
	srcRaw := rawArgs["file"]
	src, err := transferaction.ResolveArgValue(srcRaw, trigger, "")
	if err != nil {
		fail(err.Error(), nil)
		return
	}
	src, err = common.ExpandPath(src)
	if err != nil || src == "" {
		if err == nil {
			err = fmt.Errorf("missing file argument")
		}
		fail(err.Error(), nil)
		return
	}
	dstKey := rawArgs["destination_file"]
	if strings.TrimSpace(dstKey) == "" {
		dstKey = rawArgs["new_file"]
	}
	dst, err := transferaction.ResolveArgValue(dstKey, trigger, src)
	if err != nil {
		fail(err.Error(), map[string]any{"file": src})
		return
	}
	if strings.TrimSpace(dst) == "" {
		fail("missing destination_file argument", map[string]any{"file": src})
		return
	}
	dstClient := strings.TrimSpace(rawArgs["destination_client"])
	var dstTags []string
	if raw := strings.TrimSpace(rawArgs["destination_client_tags"]); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				dstTags = append(dstTags, t)
			}
		}
	}
	data, err := os.ReadFile(src)
	if err != nil {
		fail(fmt.Sprintf("cannot read source: %v", err), map[string]any{"file": src})
		return
	}
	if int64(len(data)) > transferMaxBytes {
		fail(fmt.Sprintf("source exceeds %d bytes", transferMaxBytes), map[string]any{"file": src})
		return
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	transferID, err := r.api.InitTransfer(ctx, workflowID, executionID, nodeID, dstClient, dstTags,
		filepath.Base(src), dst, strings.TrimSpace(rawArgs["permissions"]), int64(len(data)), sha)
	if err != nil {
		fail(err.Error(), map[string]any{"file": src, "destination_file": dst})
		return
	}
	r.log.Info("transfer session opened", "transfer_id", transferID, "to", dstClient, "bytes", len(data))

	via := "relay"
	p2pCtx, p2pCancel := context.WithTimeout(ctx, transferP2PWait)
	defer p2pCancel()
	header := P2PHeader{FileName: filepath.Base(src), Size: int64(len(data)), SHA256: sha, Permissions: strings.TrimSpace(rawArgs["permissions"])}
	if perr := p2pSend(p2pCtx, serverSignalTransport{api: r.api, transferID: transferID}, self, header, data); perr != nil {
		r.log.Info("transfer p2p unavailable, using relay", "transfer_id", transferID, "reason", perr)
		seq := 0
		for off := 0; off < len(data); off += relayChunkBytes {
			end := off + relayChunkBytes
			if end > len(data) {
				end = len(data)
			}
			if _, err := r.api.UploadChunk(ctx, transferID, seq, data[off:end]); err != nil {
				fail(fmt.Sprintf("relay upload chunk %d: %v", seq, err),
					map[string]any{"file": src, "destination_file": dst, "transfer_id": transferID})
				return
			}
			seq++
		}
		if len(data) == 0 {
			if _, err := r.api.UploadChunk(ctx, transferID, 0, []byte{}); err != nil {
				fail(fmt.Sprintf("relay upload: %v", err),
					map[string]any{"file": src, "destination_file": dst, "transfer_id": transferID})
				return
			}
		}
	} else {
		via = "p2p"
	}
	r.log.Info("transfer sent", "transfer_id", transferID, "via", via)
	r.events <- common.ResultData{
		WorkflowID: workflowID, ExecutionID: executionID,
		ActionID: nodeID, ActionType: "action", ActionName: "transfer",
		Payload: map[string]any{
			"type": "success", "file": src, "destination_file": dst,
			"destination_client": dstClient, "transfer_id": transferID,
			"bytes": len(data), "sha256": sha, "via": via,
			"result": "File transferred successfully via " + via,
		},
	}
}

// drainTransfers receives inbound deliveries: P2P offers first, relay
// download otherwise. Terminal outcomes are remembered (transfersSeen);
// transient failures retry on later ticks.
func (r *runner) drainTransfers(ctx context.Context, selfID string) {
	sessions, err := r.api.PendingTransfers(ctx, selfID)
	if err != nil {
		r.log.Warn("transfer inbox poll failed", "error", err)
		return
	}
	for _, sess := range sessions {
		if sess.TransferID == "" {
			continue
		}
		r.mu.Lock()
		if r.transfersSeen == nil {
			r.transfersSeen = make(map[string]bool)
		}
		if r.transfersSeen[sess.TransferID] {
			r.mu.Unlock()
			continue
		}
		r.mu.Unlock()
		r.handleInboundTransfer(ctx, selfID, sess)
	}
}

func (r *runner) markTransferSeen(id string) {
	r.mu.Lock()
	if r.transfersSeen == nil {
		r.transfersSeen = make(map[string]bool)
	}
	r.transfersSeen[id] = true
	r.mu.Unlock()
}

func (r *runner) handleInboundTransfer(ctx context.Context, selfID string, sess TransferSession) {
	// P2P first, but only when the sender actually offered: skip the
	// handshake cost when no offer signal exists yet.
	if sess.Status == "offered" && r.transferHasOffer(ctx, sess.TransferID, selfID) {
		p2pCtx, cancel := context.WithTimeout(ctx, transferP2PWait)
		got, perr := p2pReceive(p2pCtx, serverSignalTransport{api: r.api, transferID: sess.TransferID}, selfID)
		cancel()
		if perr == nil {
			if werr := writeReceivedBytes(sess, got.Data); werr != nil {
				r.log.Error("transfer p2p write failed", "transfer_id", sess.TransferID, "error", werr)
				r.markTransferSeen(sess.TransferID)
				return
			}
			if cerr := r.api.CompleteTransfer(ctx, sess.TransferID, got.Header.SHA256, true); cerr != nil {
				r.log.Error("transfer complete failed", "transfer_id", sess.TransferID, "error", cerr)
				return
			}
			r.log.Info("transfer received via p2p", "transfer_id", sess.TransferID, "bytes", len(got.Data))
			r.markTransferSeen(sess.TransferID)
			return
		}
		r.log.Info("transfer p2p failed, trying relay", "transfer_id", sess.TransferID, "reason", perr)
	}
	if sess.Status != "ready" {
		// Still uploading (or offered without an offer yet): retry later.
		return
	}
	sum, derr := r.api.DownloadFileRelay(ctx, sess, expandDst(sess.DestinationFile))
	if derr != nil {
		// Collision / sha / validation errors are terminal; network
		// errors retry next tick.
		if isTerminalTransferError(derr) {
			r.log.Error("transfer download failed", "transfer_id", sess.TransferID, "error", derr)
			r.markTransferSeen(sess.TransferID)
		} else {
			r.log.Warn("transfer download retrying", "transfer_id", sess.TransferID, "error", derr)
		}
		return
	}
	if cerr := r.api.CompleteTransfer(ctx, sess.TransferID, sum, false); cerr != nil {
		r.log.Error("transfer complete failed", "transfer_id", sess.TransferID, "error", cerr)
		return
	}
	r.log.Info("transfer received via relay", "transfer_id", sess.TransferID)
	r.markTransferSeen(sess.TransferID)
}

func (r *runner) transferHasOffer(ctx context.Context, transferID, selfID string) bool {
	sigs, err := r.api.PollSignals(ctx, transferID, time.Now().Add(-10*time.Minute))
	if err != nil {
		return false
	}
	for _, s := range sigs {
		if s.Kind == "offer" && s.FromClientID != selfID {
			return true
		}
	}
	return false
}

func expandDst(dst string) string {
	expanded, err := common.ExpandPath(dst)
	if err != nil || expanded == "" {
		return dst
	}
	return expanded
}

// writeReceivedBytes persists P2P-delivered bytes under the transfer
// safety rules (shared with the relay path semantics).
func writeReceivedBytes(sess TransferSession, data []byte) error {
	dst := expandDst(sess.DestinationFile)
	if err := transferaction.ValidateDestinationBase(dst); err != nil {
		return err
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.ToLower(strings.TrimSpace(sess.SHA256)) {
		return fmt.Errorf("sha256 mismatch")
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	if strings.TrimSpace(sess.Permissions) != "" {
		cleaned := strings.TrimSpace(sess.Permissions)
		cleaned = strings.TrimPrefix(cleaned, "0o")
		cleaned = strings.TrimPrefix(cleaned, "0O")
		v, err := strconv.ParseUint(cleaned, 8, 32)
		if err != nil || v > 0o777 {
			_ = os.Remove(dst)
			return fmt.Errorf("invalid permissions %q", sess.Permissions)
		}
		if err := os.Chmod(dst, os.FileMode(v)); err != nil && runtime.GOOS != "windows" {
			_ = os.Remove(dst)
			return fmt.Errorf("cannot apply permissions: %v", err)
		}
	}
	return nil
}

func isTerminalTransferError(err error) bool {
	msg := err.Error()
	for _, terminal := range []string{
		"destination already exists", "sha256 mismatch", "invalid permissions",
		"Windows-reserved", "Windows-reserved characters", "invalid destination",
	} {
		if strings.Contains(msg, terminal) {
			return true
		}
	}
	return false
}
