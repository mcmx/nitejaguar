package server

import (
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/internal/database"
)

// clientInfo is a registered remote client (REST polling transport).
type clientInfo struct {
	ID            string    `json:"client_id"`
	Name          string    `json:"name"`
	TenantID      string    `json:"tenant_id"`
	Tags          []string  `json:"tags"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	LastPoll      time.Time `json:"last_poll"`
}

// clientRegistry is a database-backed client registry.
type clientRegistry struct {
	db database.Service
}

func newClientRegistry(db database.Service) *clientRegistry {
	return &clientRegistry{db: db}
}

func (r *clientRegistry) register(name string, tags []string, tenantID string) (*clientInfo, string) {
	if r.db == nil {
		return nil, ""
	}
	c, token, err := r.db.RegisterClient(name, tags, tenantID)
	if err != nil {
		return nil, ""
	}
	return &clientInfo{
		ID:            c.ID,
		Name:          c.Name,
		TenantID:      c.TenantID,
		Tags:          append([]string(nil), c.Tags...),
		RegisteredAt:  c.RegisteredAt,
		LastHeartbeat: c.LastHeartbeat,
		LastPoll:      c.LastPoll,
	}, token
}

func (r *clientRegistry) getClient(id string) (*clientInfo, bool) {
	if r.db == nil {
		return nil, false
	}
	cs, err := r.db.GetClients()
	if err != nil {
		return nil, false
	}
	for _, c := range cs {
		if c.ID == id {
			return &clientInfo{
				ID:            c.ID,
				Name:          c.Name,
				TenantID:      c.TenantID,
				Tags:          append([]string(nil), c.Tags...),
				RegisteredAt:  c.RegisteredAt,
				LastHeartbeat: c.LastHeartbeat,
				LastPoll:      c.LastPoll,
			}, true
		}
	}
	return nil, false
}

func (r *clientRegistry) heartbeat(id string) (*clientInfo, bool) {
	if r.db == nil {
		return nil, false
	}
	err := r.db.HeartbeatClient(id)
	if err != nil {
		return nil, false
	}
	return r.getClient(id)
}

func (r *clientRegistry) poll(id string) (*clientInfo, bool) {
	if r.db == nil {
		return nil, false
	}
	_ = r.db.PollClient(id)
	return r.getClient(id)
}

func (r *clientRegistry) list() []*clientInfo {
	if r.db == nil {
		return nil
	}
	cs, err := r.db.GetClients()
	if err != nil {
		return nil
	}
	list := make([]*clientInfo, 0, len(cs))
	for _, c := range cs {
		list = append(list, &clientInfo{
			ID:            c.ID,
			Name:          c.Name,
			TenantID:      c.TenantID,
			Tags:          append([]string(nil), c.Tags...),
			RegisteredAt:  c.RegisteredAt,
			LastHeartbeat: c.LastHeartbeat,
			LastPoll:      c.LastPoll,
		})
	}
	return list
}

func (r *clientRegistry) authenticate(id, token string) bool {
	if r.db == nil {
		return false
	}
	_, ok := r.db.AuthenticateClient(id, token)
	return ok
}

func (r *clientRegistry) identity(token string) (string, bool) {
	if r.db == nil {
		return "", false
	}
	return r.db.AuthenticateClient("", token)
}

func bearerToken(authorization, compatible string) string {
	if len(authorization) > 7 && strings.EqualFold(authorization[:7], "Bearer ") {
		return strings.TrimSpace(authorization[7:])
	}
	return strings.TrimSpace(compatible)
}
