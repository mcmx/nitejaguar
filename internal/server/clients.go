package server

import (
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
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

// clientRegistry is a mutex-protected in-memory client registry.
type clientRegistry struct {
	mu      sync.RWMutex
	clients map[string]*clientInfo
}

func newClientRegistry() *clientRegistry {
	return &clientRegistry{clients: make(map[string]*clientInfo)}
}

func (r *clientRegistry) register(name string, tags []string) *clientInfo {
	tid, _ := typeid.WithPrefix("client")
	c := &clientInfo{
		ID:            tid.String(),
		Name:          name,
		Tags:          append([]string{}, tags...),
		LastHeartbeat: time.Now(),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[c.ID] = c
	return c
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
	return c, true
}
