package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	dsql "database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/ent/auditlog"
	"github.com/mcmx/nitejaguar/ent/enrollmenttoken"
	"github.com/mcmx/nitejaguar/ent/nodeassignment"
	"github.com/mcmx/nitejaguar/ent/remoteclient"
	"github.com/mcmx/nitejaguar/ent/workflow"
	"go.jetify.com/typeid"

	_ "github.com/joho/godotenv/autoload"
	_ "github.com/mattn/go-sqlite3"
)

// Service represents a service that interacts with a database.
type Service interface {
	// Health returns a map of health status information.
	// The keys and values in the map are service-specific.
	Health() *HealthResponse

	// Close terminates the database connection.
	// It returns an error if the connection cannot be closed.
	Close() error

	// SaveWorkflow saves a workflow definition to the database
	SaveWorkflow(workflowId string, jsonDef string, tenantID string) error

	// GetWorkflow retrieves a workflow definition from the database
	GetWorkflow(workflowId string) (*ent.Workflow, error)

	// GetWorkflows retrieves all workflow definitions from the database
	GetWorkflows(all, isEnabled bool) ([]*ent.Workflow, error)
	SetWorkflowEnabled(workflowID string, enabled bool) error

	// Client database operations
	RegisterClient(name string, tags []string, tenantID string) (*ent.RemoteClient, string, error)
	HeartbeatClient(id string) error
	PollClient(id string) error
	GetClients() ([]*ent.RemoteClient, error)
	AuthenticateClient(id, token string) (string, bool)
	RevokeClient(id string) error

	// Tenant enrollment & client lifecycle (roadmap slice 1).
	CreateEnrollmentToken(tenantID, label string, expiresAt *time.Time, maxUses int) (*ent.EnrollmentToken, string, error)
	ListEnrollmentTokens() ([]*ent.EnrollmentToken, error)
	RevokeEnrollmentToken(id string) error
	ConsumeEnrollmentToken(token string) (*ent.EnrollmentToken, error)
	EnsureDefaultEnrollmentToken() (plaintext string, created bool, err error)

	// Audit trail for enrollment use and lifecycle ops.
	LogAudit(action, tenantID, actor, target, detail string) error
	ListAuditLogs(limit int) ([]*ent.AuditLog, error)

	// Distributed dispatch (roadmap slice 2): pending node assignments per
	// execution. Enqueue is idempotent per (workflow, execution, node).
	EnqueueNodeAssignment(tenantID, workflowID, executionID, nodeID, parentActionID string, payload any) (*ent.NodeAssignment, error)
	ListPendingAssignments() ([]*ent.NodeAssignment, error)
	CompleteNodeAssignment(workflowID, executionID, nodeID string) error

	// Credentials model (roadmap slice 3): nodes hold a credential_ref,
	// never the secret. Secrets are encrypted at rest and delivered
	// just-in-time to the executing client.
	CreateCredential(tenantID, name, credType, scope, ownerID, secretPlaintext, description string) (*ent.Credential, error)
	ListCredentials(tenantID string) ([]*ent.Credential, error)
	GetCredential(id string) (*ent.Credential, error)
	DeleteCredential(id string) error
	ResolveCredential(tenantID, ref, userID string, groupIDs []string) (*ent.Credential, error)
	DecryptCredentialSecret(row *ent.Credential) (string, error)

	// Client-to-client transfer (roadmap slice 6): relay sessions +
	// WebRTC signaling. Control plane goes through the server; the relay
	// is the guaranteed path with P2P first.
	InitTransferSession(tenantID, workflowID, executionID, nodeID, senderClientID, receiverClientID string, receiverTags []string, fileName, destinationFile, permissions string, size int64, sha256hex string) (*ent.TransferSession, error)
	GetTransferSession(id string) (*ent.TransferSession, error)
	AppendTransferChunk(transferID string, seq int, data []byte) (int, error)
	ListTransferChunks(transferID string, fromSeq int) ([]*ent.TransferChunk, error)
	ListPendingTransfers(clientID string, tags []string, tenantID string) ([]*ent.TransferSession, error)
	CompleteTransferSession(transferID, sha256hex string, viaP2P bool) (*ent.TransferSession, error)
	PostTransferSignal(transferID, fromClientID, kind, payload string) (*ent.TransferSignal, error)
	ListTransferSignals(transferID string, since time.Time) ([]*ent.TransferSignal, error)
	SetClientDialInfo(id, dialInfo string) error

	// RBAC + web auth (roadmap slice 4): users, roles, login sessions.
	CreateUser(tenantID, username, password, role string, groups []string) (*ent.AppUser, error)
	ListUsers(tenantID string) ([]*ent.AppUser, error)
	GetUser(id string) (*ent.AppUser, error)
	CountUsers() (int, error)
	RevokeUser(id string) error
	VerifyUser(tenantID, username, password string) (*ent.AppUser, error)
	CreateSession(userID string, ttl time.Duration) (*ent.AuthSession, string, error)
	AuthenticateSession(token string) (*ent.AppUser, *ent.AuthSession, error)
	RevokeSession(token string) error
	EnsureDefaultAdmin() (username, plaintext string, created bool, err error)
}

type service struct {
	db     *dsql.DB
	client *ent.Client
}

type HealthResponse struct {
	Status            string `json:"status"`
	Message           string `json:"message"`
	Error             string `json:"error,omitempty"`
	OpenConnections   int    `json:"open_connections"`
	InUse             int    `json:"in_use"`
	Idle              int    `json:"idle"`
	WaitCount         int64  `json:"wait_count"`
	WaitDuration      string `json:"wait_duration"`
	MaxIdleClosed     int64  `json:"max_idle_closed"`
	MaxLifetimeClosed int64  `json:"max_lifetime_closed"`
}

var (
	dburl      string
	dbInstance *service
)

func New() (Service, error) {
	// Reuse Connection
	dburl = os.Getenv("DB_URL")
	if dbInstance != nil {
		return dbInstance, nil
	}
	if dburl == "" {
		fmt.Println("DB_URL is empty, you could set it to: file:ent.db?mode=memory&cache=shared&_fk=1, to start in memory only")
		fmt.Println("or file:ent.db?cache=shared&_fk=1, to create a file called ent.db")
	}

	drv, err := sql.Open("sqlite3", dburl)
	if err != nil {
		// This will not be a connection error, but a DSN parse error or
		// another initialization error.
		return nil, fmt.Errorf("failed opening database connection: %w", err)
	}

	db := drv.DB()
	client := ent.NewClient(ent.Driver(drv))

	dbInstance = &service{
		client: client,
		db:     db,
	}
	if err := client.Schema.Create(context.Background()); err != nil {
		return nil, fmt.Errorf("failed creating schema resources: %w", err)
	}
	if plaintext, created, err := dbInstance.EnsureDefaultEnrollmentToken(); err != nil {
		log.Printf("failed bootstrapping default enrollment token: %v", err)
	} else if created {
		// The plaintext is only available here; print to stdout so the
		// operator can enroll the first client. It is never stored.
		fmt.Printf("BOOTSTRAP enrollment token (tenant=default, one-time): %s\n", plaintext)
		log.Printf("BOOTSTRAP enrollment token created for tenant=default (one-time, see stdout)")
	}
	dbInstance.ensureDefaultAdminLogged()
	return dbInstance, nil
}

// Health checks the health of the database connection by pinging the database.
// It returns a map with keys indicating various health statistics.
func (s *service) Health() *HealthResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stats := &HealthResponse{}

	// Ping the database
	err := s.db.PingContext(ctx)
	if err != nil {
		stats.Status = "down"
		stats.Error = fmt.Sprintf("db down: %v", err)
		return stats
	}

	// Database is up, add more statistics
	stats.Status = "up"
	stats.Message = "It's healthy"

	// Get database stats (like open connections, in use, idle, etc.)
	dbStats := s.db.Stats()
	stats.OpenConnections = dbStats.OpenConnections
	stats.InUse = dbStats.InUse
	stats.Idle = dbStats.Idle
	stats.WaitCount = dbStats.WaitCount
	stats.WaitDuration = dbStats.WaitDuration.String()
	stats.MaxIdleClosed = dbStats.MaxIdleClosed
	stats.MaxLifetimeClosed = dbStats.MaxLifetimeClosed

	// Evaluate stats to provide a health message
	if dbStats.OpenConnections > 40 { // Assuming 50 is the max for this example
		stats.Message = "The database is experiencing heavy load."
	}

	if dbStats.WaitCount > 1000 {
		stats.Message = "The database has a high number of wait events, indicating potential bottlenecks."
	}

	if dbStats.MaxIdleClosed > int64(dbStats.OpenConnections)/2 {
		stats.Message = "Many idle connections are being closed, consider revising the connection pool settings."
	}

	if dbStats.MaxLifetimeClosed > int64(dbStats.OpenConnections)/2 {
		stats.Message = "Many connections are being closed due to max lifetime, consider increasing max lifetime or revising the connection usage pattern."
	}

	return stats
}

// Close closes the database connection.
// It logs a message indicating the disconnection from the specific database.
// If the connection is successfully closed, it returns nil.
// If an error occurs while closing the connection, it returns the error.
func (s *service) Close() error {
	log.Printf("Disconnected from database: %s", dburl)
	return s.db.Close()
}

// SaveWorkflow saves a workflow definition to the database
func (s *service) SaveWorkflow(workflowId string, jsonDef string, tenantID string) error {
	if tenantID == "" {
		tenantID = "default"
	}
	revID, _ := typeid.WithPrefix("revision")
	w, err := s.client.Workflow.Query().Where(workflow.ID(workflowId)).Only(context.Background())
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("failed to check existing workflow: %w", err)
	}
	if w != nil {
		log.Printf("Workflow %s already exists, updating...", workflowId)
		_, err := s.client.Workflow.UpdateOneID(workflowId).
			SetJSONDefinition(jsonDef).
			SetTenantID(tenantID).
			SetRevision(revID.String()).
			Save(context.Background())
		if err != nil {
			return fmt.Errorf("failed to save workflow: %w", err)
		}
		return nil
	}
	_, err = s.client.Workflow.Create().
		SetJSONDefinition(jsonDef).
		SetTenantID(tenantID).
		SetID(workflowId).
		SetRevision(revID.String()).
		Save(context.Background())

	if err != nil {
		return fmt.Errorf("failed to save workflow: %w", err)
	}

	return nil
}

// GetWorkflow retrieves a workflow definition from the database
func (s *service) GetWorkflow(workflowId string) (*ent.Workflow, error) {
	w, err := s.client.Workflow.Get(context.Background(), workflowId)

	if err != nil {
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}

	return w, nil
}

// GetWorkflows retrieves all workflow definitions from the database
func (s *service) GetWorkflows(all, isEnabled bool) ([]*ent.Workflow, error) {
	if all {
		ws, err := s.client.Workflow.Query().All(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to get workflows: %w", err)
		}
		return ws, nil
	}
	ws, err := s.client.Workflow.Query().Where(workflow.Enabled(isEnabled)).All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get workflows: %w", err)
	}
	return ws, nil
}

func (s *service) SetWorkflowEnabled(workflowID string, enabled bool) error {
	return s.client.Workflow.UpdateOneID(workflowID).SetEnabled(enabled).Exec(context.Background())
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *service) RegisterClient(name string, tags []string, tenantID string) (*ent.RemoteClient, string, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	tid, _ := typeid.WithPrefix("client")
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	h := hashToken(token)

	c, err := s.client.RemoteClient.Create().
		SetID(tid.String()).
		SetName(name).
		SetTenantID(tenantID).
		SetTags(tags).
		SetTokenHash(h).
		Save(context.Background())
	if err != nil {
		return nil, "", fmt.Errorf("failed to register client: %w", err)
	}
	return c, token, nil
}

func (s *service) HeartbeatClient(id string) error {
	err := s.client.RemoteClient.UpdateOneID(id).
		SetLastHeartbeat(time.Now()).
		Exec(context.Background())
	if err != nil {
		return fmt.Errorf("failed to update client heartbeat: %w", err)
	}
	return nil
}

func (s *service) PollClient(id string) error {
	err := s.client.RemoteClient.UpdateOneID(id).
		SetLastPoll(time.Now()).
		Exec(context.Background())
	if err != nil {
		return fmt.Errorf("failed to update client poll: %w", err)
	}
	return nil
}

func (s *service) GetClients() ([]*ent.RemoteClient, error) {
	cs, err := s.client.RemoteClient.Query().All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get clients: %w", err)
	}
	return cs, nil
}

func (s *service) AuthenticateClient(id, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	h := hashToken(token)
	if id != "" {
		c, err := s.client.RemoteClient.Get(context.Background(), id)
		if err != nil {
			return "", false
		}
		if c.Revoked {
			return "", false
		}
		return c.ID, subtle.ConstantTimeCompare([]byte(c.TokenHash), []byte(h)) == 1
	}
	cs, err := s.client.RemoteClient.Query().All(context.Background())
	if err != nil {
		return "", false
	}
	for _, c := range cs {
		if c.Revoked {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(c.TokenHash), []byte(h)) == 1 {
			return c.ID, true
		}
	}
	return "", false
}

func (s *service) RevokeClient(id string) error {
	c, err := s.client.RemoteClient.Get(context.Background(), id)
	if err != nil {
		return fmt.Errorf("client not found: %w", err)
	}
	if c.Revoked {
		return nil
	}
	if err := s.client.RemoteClient.UpdateOneID(id).
		SetRevoked(true).
		SetRevokedAt(time.Now()).
		Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to revoke client: %w", err)
	}
	return nil
}

// CreateEnrollmentToken mints a tenant-scoped join token. The plaintext is
// returned once and never stored; only its sha256 hash is persisted.
func (s *service) CreateEnrollmentToken(tenantID, label string, expiresAt *time.Time, maxUses int) (*ent.EnrollmentToken, string, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	if maxUses < 0 {
		return nil, "", fmt.Errorf("max_uses cannot be negative")
	}
	tid, _ := typeid.WithPrefix("enroll")
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", fmt.Errorf("failed to generate token: %w", err)
	}
	plaintext := hex.EncodeToString(buf)
	create := s.client.EnrollmentToken.Create().
		SetID(tid.String()).
		SetTenantID(tenantID).
		SetLabel(label).
		SetTokenHash(hashToken(plaintext)).
		SetMaxUses(maxUses)
	if expiresAt != nil {
		create.SetExpiresAt(*expiresAt)
	}
	tok, err := create.Save(context.Background())
	if err != nil {
		return nil, "", fmt.Errorf("failed to create enrollment token: %w", err)
	}
	return tok, plaintext, nil
}

func (s *service) ListEnrollmentTokens() ([]*ent.EnrollmentToken, error) {
	toks, err := s.client.EnrollmentToken.Query().
		Order(ent.Desc(enrollmenttoken.FieldCreatedAt)).
		All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list enrollment tokens: %w", err)
	}
	return toks, nil
}

func (s *service) RevokeEnrollmentToken(id string) error {
	tok, err := s.client.EnrollmentToken.Get(context.Background(), id)
	if err != nil {
		return fmt.Errorf("enrollment token not found: %w", err)
	}
	if tok.Revoked {
		return nil
	}
	if err := s.client.EnrollmentToken.UpdateOneID(id).
		SetRevoked(true).
		Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to revoke enrollment token: %w", err)
	}
	return nil
}

// ConsumeEnrollmentToken validates a join token and records one use.
// It returns the token row (tenant comes from here, never from client input).
func (s *service) ConsumeEnrollmentToken(token string) (*ent.EnrollmentToken, error) {
	if token == "" {
		return nil, fmt.Errorf("enrollment token is required")
	}
	h := hashToken(token)
	tok, err := s.client.EnrollmentToken.Query().
		Where(enrollmenttoken.TokenHash(h)).
		Only(context.Background())
	if err != nil {
		return nil, fmt.Errorf("invalid enrollment token")
	}
	if tok.Revoked {
		return nil, fmt.Errorf("enrollment token revoked")
	}
	if tok.ExpiresAt != nil && time.Now().After(*tok.ExpiresAt) {
		return nil, fmt.Errorf("enrollment token expired")
	}
	if tok.MaxUses > 0 && tok.UseCount >= tok.MaxUses {
		return nil, fmt.Errorf("enrollment token exhausted")
	}
	updated, err := s.client.EnrollmentToken.UpdateOneID(tok.ID).
		SetUseCount(tok.UseCount + 1).
		Save(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to consume enrollment token: %w", err)
	}
	return updated, nil
}

// EnsureDefaultEnrollmentToken bootstraps the `default` tenant with a
// one-time join token when no usable token exists. It returns the plaintext
// only when a token was newly created.
func (s *service) EnsureDefaultEnrollmentToken() (string, bool, error) {
	toks, err := s.client.EnrollmentToken.Query().
		Where(enrollmenttoken.TenantID("default")).
		All(context.Background())
	if err != nil {
		return "", false, fmt.Errorf("failed to check enrollment tokens: %w", err)
	}
	now := time.Now()
	for _, tok := range toks {
		if tok.Revoked {
			continue
		}
		if tok.ExpiresAt != nil && now.After(*tok.ExpiresAt) {
			continue
		}
		if tok.MaxUses > 0 && tok.UseCount >= tok.MaxUses {
			continue
		}
		return "", false, nil
	}
	tok, plaintext, err := s.CreateEnrollmentToken("default", "bootstrap", nil, 1)
	if err != nil {
		return "", false, err
	}
	_ = s.LogAudit("enrollment.create", "default", "system", tok.ID, "bootstrap one-time token")
	return plaintext, true, nil
}

func (s *service) LogAudit(action, tenantID, actor, target, detail string) error {
	if tenantID == "" {
		tenantID = "default"
	}
	aid, _ := typeid.WithPrefix("audit")
	if err := s.client.AuditLog.Create().
		SetID(aid.String()).
		SetAction(action).
		SetTenantID(tenantID).
		SetActor(actor).
		SetTarget(target).
		SetDetail(detail).
		Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to write audit log: %w", err)
	}
	return nil
}

func (s *service) ListAuditLogs(limit int) ([]*ent.AuditLog, error) {
	q := s.client.AuditLog.Query().Order(ent.Desc(auditlog.FieldCreatedAt))
	if limit > 0 {
		q.Limit(limit)
	}
	logs, err := q.All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list audit logs: %w", err)
	}
	return logs, nil
}

// EnqueueNodeAssignment persists a pending node execution. It is idempotent
// per (workflow, execution, node): an existing pending or done row is
// returned unchanged so re-ingested results cannot double-enqueue.
func (s *service) EnqueueNodeAssignment(tenantID, workflowID, executionID, nodeID, parentActionID string, payload any) (*ent.NodeAssignment, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	if workflowID == "" || executionID == "" || nodeID == "" {
		return nil, fmt.Errorf("workflow_id, execution_id and node_id are required")
	}
	ctx := context.Background()
	existing, err := s.client.NodeAssignment.Query().
		Where(
			nodeassignment.WorkflowID(workflowID),
			nodeassignment.ExecutionID(executionID),
			nodeassignment.NodeID(nodeID),
		).
		Only(ctx)
	if err == nil {
		return existing, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check existing assignment: %w", err)
	}
	payloadJSON := ""
	if payload != nil {
		if raw, merr := json.Marshal(payload); merr == nil {
			payloadJSON = string(raw)
		}
	}
	aid, _ := typeid.WithPrefix("assign")
	row, err := s.client.NodeAssignment.Create().
		SetID(aid.String()).
		SetTenantID(tenantID).
		SetWorkflowID(workflowID).
		SetExecutionID(executionID).
		SetNodeID(nodeID).
		SetPayloadJSON(payloadJSON).
		SetParentActionID(parentActionID).
		SetStatus("pending").
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue node assignment: %w", err)
	}
	return row, nil
}

func (s *service) ListPendingAssignments() ([]*ent.NodeAssignment, error) {
	rows, err := s.client.NodeAssignment.Query().
		Where(nodeassignment.Status("pending")).
		Order(ent.Asc(nodeassignment.FieldCreatedAt)).
		All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list pending assignments: %w", err)
	}
	return rows, nil
}

func (s *service) CompleteNodeAssignment(workflowID, executionID, nodeID string) error {
	ctx := context.Background()
	row, err := s.client.NodeAssignment.Query().
		Where(
			nodeassignment.WorkflowID(workflowID),
			nodeassignment.ExecutionID(executionID),
			nodeassignment.NodeID(nodeID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to check assignment: %w", err)
	}
	if row.Status == "done" {
		return nil
	}
	if err := s.client.NodeAssignment.UpdateOneID(row.ID).SetStatus("done").Exec(ctx); err != nil {
		return fmt.Errorf("failed to complete assignment: %w", err)
	}
	return nil
}

// Ensure remoteclient import is used even when only enrollment code paths run.
var _ = remoteclient.FieldRevoked
