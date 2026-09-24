package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"
)

// memTransport is an in-memory SignalTransport linking exactly two peers.
type memTransport struct {
	mu      *sync.Mutex
	self    string
	queues  map[string]*[]Signal
	counter *int
	now     *time.Time
}

func newMemPair() (*memTransport, *memTransport) {
	shared := make(map[string]*[]Signal)
	qa, qb := []Signal{}, []Signal{}
	shared["a"] = &qa
	shared["b"] = &qb
	now := time.Now()
	counter := 0
	mu := &sync.Mutex{}
	return &memTransport{self: "a", queues: shared, now: &now, counter: &counter, mu: mu},
		&memTransport{self: "b", queues: shared, now: &now, counter: &counter, mu: mu}
}

func (m *memTransport) Post(_ context.Context, kind, payload string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.counter++
	*m.now = m.now.Add(time.Millisecond)
	other := "b"
	if m.self == "b" {
		other = "a"
	}
	*m.queues[other] = append(*m.queues[other], Signal{
		ID: fmt.Sprintf("sig-%s-%d", m.self, *m.counter), FromClientID: m.self,
		Kind: kind, Payload: payload, CreatedAt: m.now.UTC().Format(time.RFC3339Nano),
	})
	return nil
}

func (m *memTransport) Poll(_ context.Context, since time.Time) ([]Signal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Signal
	for _, s := range *m.queues[m.self] {
		ts, err := time.Parse(time.RFC3339Nano, s.CreatedAt)
		if err != nil || ts.After(since) {
			out = append(out, s)
		}
	}
	return out, nil
}

func TestP2PLoopback(t *testing.T) {
	senderT, receiverT := newMemPair()
	payload := make([]byte, 100*1024)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	sum := sha256.Sum256(payload)
	header := P2PHeader{FileName: "big.bin", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:]), Permissions: "0644"}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sendErr := make(chan error, 1)
	go func() { sendErr <- p2pSend(ctx, senderT, "a", header, payload) }()
	got, err := p2pReceive(ctx, receiverT, "b")
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("send: %v", err)
	}
	if string(got.Data) != string(payload) {
		t.Fatalf("payload mismatch: %d vs %d bytes", len(got.Data), len(payload))
	}
	if got.Header != header {
		t.Fatalf("header mismatch: %+v", got.Header)
	}
}

func TestP2PSendTimesOutWithoutReceiver(t *testing.T) {
	senderT, _ := newMemPair()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	header := P2PHeader{FileName: "x", Size: 3, SHA256: "abc"}
	if err := p2pSend(ctx, senderT, "a", header, []byte("xyz")); err == nil {
		t.Fatal("expected timeout error without a receiver")
	}
}

func TestP2PRejectsTamperedBytes(t *testing.T) {
	// Header sha must match delivered bytes; covered by p2pReceive check.
	// This test pins the corrupt-payload path via a raw tamper simulation:
	// receive must fail when bytes don't match the header hash.
	senderT, receiverT := newMemPair()
	header := P2PHeader{FileName: "x", Size: 4, SHA256: hex.EncodeToString(make([]byte, 32))}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = p2pSend(ctx, senderT, "a", header, []byte("evil")) }()
	if _, err := p2pReceive(ctx, receiverT, "b"); err == nil {
		t.Fatal("expected sha mismatch error")
	}
}
