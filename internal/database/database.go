package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	dsql "database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/mcmx/nitejaguar/ent"
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
	SaveWorkflow(workflowId string, jsonDef string) error

	// GetWorkflow retrieves a workflow definition from the database
	GetWorkflow(workflowId string) (*ent.Workflow, error)

	// GetWorkflows retrieves all workflow definitions from the database
	GetWorkflows(all, isEnabled bool) ([]*ent.Workflow, error)
	SetWorkflowEnabled(workflowID string, enabled bool) error

	// Client database operations
	RegisterClient(name string, tags []string) (*ent.RemoteClient, string, error)
	HeartbeatClient(id string) error
	PollClient(id string) error
	GetClients() ([]*ent.RemoteClient, error)
	AuthenticateClient(id, token string) (string, bool)
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
func (s *service) SaveWorkflow(workflowId string, jsonDef string) error {
	w, err := s.client.Workflow.Query().Where(workflow.ID(workflowId)).Only(context.Background())
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("failed to check existing workflow: %w", err)
	}
	if w != nil {
		log.Printf("Workflow %s already exists, updating...", workflowId)
		_, err := s.client.Workflow.UpdateOneID(workflowId).
			SetJSONDefinition(jsonDef).
			Save(context.Background())
		if err != nil {
			return fmt.Errorf("failed to save workflow: %w", err)
		}
		return nil
	}
	_, err = s.client.Workflow.Create().
		SetJSONDefinition(jsonDef).
		SetID(workflowId).
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

func (s *service) RegisterClient(name string, tags []string) (*ent.RemoteClient, string, error) {
	tid, _ := typeid.WithPrefix("client")
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	h := hashToken(token)

	c, err := s.client.RemoteClient.Create().
		SetID(tid.String()).
		SetName(name).
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
		return c.ID, subtle.ConstantTimeCompare([]byte(c.TokenHash), []byte(h)) == 1
	}
	cs, err := s.client.RemoteClient.Query().All(context.Background())
	if err != nil {
		return "", false
	}
	for _, c := range cs {
		if subtle.ConstantTimeCompare([]byte(c.TokenHash), []byte(h)) == 1 {
			return c.ID, true
		}
	}
	return "", false
}
