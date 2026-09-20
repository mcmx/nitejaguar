package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"go.jetify.com/typeid"
)

// clientInfo is a registered remote client (REST polling transport).
// Kept in-memory only; persisted executions are deferred to a later issue.
type clientInfo struct {
	ID            string    `json:"client_id"`
	Name          string    `json:"name"`
	Tags          []string  `json:"tags"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	LastPoll      time.Time `json:"last_poll"`
	tokenHash     string
}

// clientRegistry is a mutex-protected in-memory client registry.
type clientRegistry struct {
	mu      sync.RWMutex
	clients map[string]*clientInfo
}

func newClientRegistry() *clientRegistry {
	return &clientRegistry{clients: make(map[string]*clientInfo)}
}

func (r *clientRegistry) register(name string, tags []string) (*clientInfo, string) {
	tid, _ := typeid.WithPrefix("client")
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	c := &clientInfo{
		ID:            tid.String(),
		Name:          name,
		Tags:          append([]string{}, tags...),
		RegisteredAt:  time.Now(),
		LastHeartbeat: time.Now(),
		tokenHash:     hashToken(token),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[c.ID] = c
	return c, token
}

func (r *clientRegistry) heartbeat(id string) (*clientInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.clients[id]
	if !ok {
		return nil, false
	}
	c.LastHeartbeat = time.Now()
	return c, true
}

func (r *clientRegistry) get(id string) (*clientInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[id]
	if !ok {
		return nil, false
	}
	copy := *c
	copy.Tags = append([]string(nil), c.Tags...)
	return &copy, true
}

func (r *clientRegistry) poll(id string) (*clientInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.clients[id]
	if !ok {
		return nil, false
	}
	c.LastPoll = time.Now()
	copy := *c
	copy.Tags = append([]string(nil), c.Tags...)
	return &copy, true
}

func (r *clientRegistry) list() []*clientInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clients := make([]*clientInfo, 0, len(r.clients))
	for _, c := range r.clients {
		copy := *c
		copy.Tags = append([]string(nil), c.Tags...)
		clients = append(clients, &copy)
	}
	return clients
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (r *clientRegistry) authenticate(id, token string) bool {
	_, ok := r.identityForClient(id, token)
	return ok
}

func (r *clientRegistry) identity(token string) (string, bool) {
	return r.identityForClient("", token)
}

func (r *clientRegistry) identityForClient(id, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	h := hashToken(token)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id != "" {
		c, ok := r.clients[id]
		if !ok {
			return "", false
		}
		return c.ID, subtle.ConstantTimeCompare([]byte(c.tokenHash), []byte(h)) == 1
	}
	for _, c := range r.clients {
		if subtle.ConstantTimeCompare([]byte(c.tokenHash), []byte(h)) == 1 {
			return c.ID, true
		}
	}
	return "", false
}

func bearerToken(authorization, compatible string) string {
	if len(authorization) > 7 && strings.EqualFold(authorization[:7], "Bearer ") {
		return strings.TrimSpace(authorization[7:])
	}
	return strings.TrimSpace(compatible)
}
