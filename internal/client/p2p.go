package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// WebRTC P2P data plane for client-to-client transfer. Signaling (SDP +
// ICE) travels through the server control plane via SignalTransport; bulk
// bytes move on an ordered reliable DataChannel. Anything P2P cannot
// deliver falls back to the server relay (the guaranteed path).
//
// Framing: first DataChannel message is a JSON P2PHeader, followed by raw
// binary messages of at most p2pChunkBytes. The receiver reassembles in
// arrival order (ordered channel) and verifies sha256.

// P2PHeader opens every P2P delivery.
type P2PHeader struct {
	FileName    string `json:"file_name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Permissions string `json:"permissions,omitempty"`
}

const (
	p2pChunkBytes  = 16 * 1024
	p2pPollEvery   = 200 * time.Millisecond
	p2pMaxHeaderB  = 8 * 1024
	p2pMaxTotalB   = 32 * 1024 * 1024
	p2pChannelName = "transfer"
)

// SignalTransport exchanges signaling messages for one transfer session.
// serverSignalTransport (below) implements it over the HTTP API; tests use
// an in-memory transport.
type SignalTransport interface {
	Post(ctx context.Context, kind, payload string) error
	Poll(ctx context.Context, since time.Time) ([]Signal, error)
}

type serverSignalTransport struct {
	api        API
	transferID string
}

func (t serverSignalTransport) Post(ctx context.Context, kind, payload string) error {
	return t.api.PostSignal(ctx, t.transferID, kind, payload)
}

func (t serverSignalTransport) Poll(ctx context.Context, since time.Time) ([]Signal, error) {
	return t.api.PollSignals(ctx, t.transferID, since)
}

func p2pConfig() webrtc.Configuration {
	// Host candidates only: no external STUN dependency. Mutually
	// reachable peers (LAN / open ingress) connect directly; everyone
	// else uses the server relay fallback.
	return webrtc.Configuration{}
}

func newP2PPeer() (*webrtc.PeerConnection, error) {
	pc, err := webrtc.NewPeerConnection(p2pConfig())
	if err != nil {
		return nil, fmt.Errorf("peer connection: %w", err)
	}
	return pc, nil
}

// waitGathered resolves when ICE gathering completes (or ctx expires).
func waitGathered(ctx context.Context, pc *webrtc.PeerConnection) error {
	gatherDone := webrtc.GatheringCompletePromise(pc)
	select {
	case <-gatherDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func marshalSDP(desc *webrtc.SessionDescription) (string, error) {
	raw, err := json.Marshal(desc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalSDP(payload string) (webrtc.SessionDescription, error) {
	var desc webrtc.SessionDescription
	if err := json.Unmarshal([]byte(payload), &desc); err != nil {
		return desc, fmt.Errorf("invalid sdp: %w", err)
	}
	return desc, nil
}

func postICE(ctx context.Context, t SignalTransport, pc *webrtc.PeerConnection) {
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		raw, err := json.Marshal(c.ToJSON())
		if err != nil {
			return
		}
		_ = t.Post(ctx, "ice", string(raw))
	})
}

func drainICE(ctx context.Context, t SignalTransport, pc *webrtc.PeerConnection, selfID string, since *time.Time, seen map[string]bool) {
	sigs, err := t.Poll(ctx, *since)
	if err != nil {
		return
	}
	for _, s := range sigs {
		if s.FromClientID == selfID || seen[s.ID] {
			continue
		}
		if s.Kind != "ice" {
			continue
		}
		seen[s.ID] = true
		if ts, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil && ts.After(*since) {
			*since = ts
		}
		var cand webrtc.ICECandidateInit
		if err := json.Unmarshal([]byte(s.Payload), &cand); err != nil {
			continue
		}
		_ = pc.AddICECandidate(cand)
	}
}

// p2pSend offers a P2P delivery and streams header+bytes. selfID filters
// out our own signals. Any error means "use the relay".
func p2pSend(ctx context.Context, t SignalTransport, selfID string, header P2PHeader, data []byte) error {
	if int64(len(data)) != header.Size || int64(len(data)) > p2pMaxTotalB {
		return fmt.Errorf("p2p payload size mismatch")
	}
	pc, err := newP2PPeer()
	if err != nil {
		return err
	}
	defer func() { _ = pc.Close() }()

	dc, err := pc.CreateDataChannel(p2pChannelName, nil)
	if err != nil {
		return fmt.Errorf("data channel: %w", err)
	}
	opened := make(chan struct{})
	var openOnce sync.Once
	dc.OnOpen(func() { openOnce.Do(func() { close(opened) }) })
	postICE(ctx, t, pc)

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	if err := waitGathered(ctx, pc); err != nil {
		return err
	}
	sdp, err := marshalSDP(pc.LocalDescription())
	if err != nil {
		return err
	}
	if err := t.Post(ctx, "offer", sdp); err != nil {
		return err
	}

	since := time.Now().Add(-time.Minute)
	seen := map[string]bool{}
	answered := false
	for !answered {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sigs, err := t.Poll(ctx, since)
		if err != nil {
			return err
		}
		for _, s := range sigs {
			if s.FromClientID == selfID || seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			if ts, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil && ts.After(since) {
				since = ts
			}
			switch s.Kind {
			case "answer":
				desc, err := unmarshalSDP(s.Payload)
				if err != nil {
					return err
				}
				if err := pc.SetRemoteDescription(desc); err != nil {
					return err
				}
				answered = true
			case "ice":
				var cand webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(s.Payload), &cand); err == nil {
					_ = pc.AddICECandidate(cand)
				}
			}
		}
		if !answered {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(p2pPollEvery):
			}
		}
	}

	select {
	case <-opened:
	case <-ctx.Done():
		return ctx.Err()
	}

	hraw, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if err := dc.Send(hraw); err != nil {
		return fmt.Errorf("send header: %w", err)
	}
	for off := 0; off < len(data); off += p2pChunkBytes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := off + p2pChunkBytes
		if end > len(data) {
			end = len(data)
		}
		if err := dc.Send(data[off:end]); err != nil {
			return fmt.Errorf("send chunk: %w", err)
		}
	}
	// Brief drain so the SCTP stack flushes before close.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	return nil
}

type p2pReceived struct {
	Header P2PHeader
	Data   []byte
}

// p2pReceive answers the latest offer and collects one delivery.
func p2pReceive(ctx context.Context, t SignalTransport, selfID string) (p2pReceived, error) {
	var out p2pReceived
	pc, err := newP2PPeer()
	if err != nil {
		return out, err
	}
	defer func() { _ = pc.Close() }()

	got := make(chan error, 1)
	var mu sync.Mutex
	var header *P2PHeader
	var data []byte
	var total int64

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			mu.Lock()
			defer mu.Unlock()
			if header == nil {
				if msg.IsString || len(msg.Data) > p2pMaxHeaderB {
					select {
					case got <- fmt.Errorf("invalid p2p header"):
					default:
					}
					return
				}
				var h P2PHeader
				if err := json.Unmarshal(msg.Data, &h); err != nil || h.Size < 0 || h.Size > p2pMaxTotalB {
					select {
					case got <- fmt.Errorf("invalid p2p header"):
					default:
					}
					return
				}
				header = &h
				data = make([]byte, 0, min64(h.Size, p2pMaxTotalB))
				if h.Size == 0 {
					select {
					case got <- nil:
					default:
					}
				}
				return
			}
			if msg.IsString {
				select {
				case got <- fmt.Errorf("unexpected text frame"):
				default:
				}
				return
			}
			data = append(data, msg.Data...)
			total += int64(len(msg.Data))
			if total > p2pMaxTotalB {
				select {
				case got <- fmt.Errorf("p2p payload too large"):
				default:
				}
				return
			}
			if header != nil && total >= header.Size {
				select {
				case got <- nil:
				default:
				}
			}
		})
	})
	postICE(ctx, t, pc)

	since := time.Now().Add(-time.Minute)
	seen := map[string]bool{}
	for {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		select {
		case err := <-got:
			if err != nil {
				return out, err
			}
			mu.Lock()
			defer mu.Unlock()
			if header == nil {
				return out, fmt.Errorf("p2p closed without header")
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != header.SHA256 {
				return out, fmt.Errorf("p2p sha256 mismatch")
			}
			out.Header = *header
			out.Data = data
			return out, nil
		default:
		}
		sigs, err := t.Poll(ctx, since)
		if err != nil {
			return out, err
		}
		for _, s := range sigs {
			if s.FromClientID == selfID || seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			if ts, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil && ts.After(since) {
				since = ts
			}
			switch s.Kind {
			case "offer":
				desc, err := unmarshalSDP(s.Payload)
				if err != nil {
					continue
				}
				if err := pc.SetRemoteDescription(desc); err != nil {
					continue
				}
				answer, err := pc.CreateAnswer(nil)
				if err != nil {
					continue
				}
				if err := pc.SetLocalDescription(answer); err != nil {
					continue
				}
				if err := waitGathered(ctx, pc); err != nil {
					return out, err
				}
				asdp, err := marshalSDP(pc.LocalDescription())
				if err != nil {
					return out, err
				}
				if err := t.Post(ctx, "answer", asdp); err != nil {
					return out, err
				}
			case "ice":
				var cand webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(s.Payload), &cand); err == nil {
					_ = pc.AddICECandidate(cand)
				}
			}
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(p2pPollEvery):
		}
		drainICE(ctx, t, pc, selfID, &since, seen)
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
