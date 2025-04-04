package database

import (
	"context"
	dsql "database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/mcmx/nitejaguar/ent"

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
	SaveWorkflow(workflowId string, jsonDef []byte) error

	// GetWorkflow retrieves a workflow definition from the database
	GetWorkflow(workflowId string) ([]byte, error)

	// GetWorkflows retrieves all workflow definitions from the database
	GetWorkflows() ([][]byte, error)
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
	dburl      = os.Getenv("DB_URL")
	dbInstance *service
)

func New() Service {
	// Reuse Connection
	if dbInstance != nil {
		return dbInstance
	}

	drv, err := sql.Open("sqlite3", dburl)
	if err != nil {
		// This will not be a connection error, but a DSN parse error or
		// another initialization error.
		log.Fatal(err)
	}

	db := drv.DB()
	client := ent.NewClient(ent.Driver(drv))

	dbInstance = &service{
		client: client,
		db:     db,
	}
	if err := client.Schema.Create(context.Background()); err != nil {
		log.Fatalf("failed creating schema resources: %v", err)
	}
	return dbInstance
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
		log.Fatalf("db down: %v", err) // Log the error and terminate the program
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
func (s *service) SaveWorkflow(workflowId string, jsonDef []byte) error {
	stmt := `INSERT OR REPLACE INTO workflows (id, json_definition, updated_at)
	VALUES (?, ?, CURRENT_TIMESTAMP)`

	_, err := s.client.ExecContext(context.Background(), stmt, workflowId, jsonDef)
	if err != nil {
		return fmt.Errorf("failed to save workflow: %w", err)
	}

	return nil
}

// GetWorkflow retrieves a workflow definition from the database
func (s *service) GetWorkflow(workflowId string) ([]byte, error) {
	w, _ := s.client.Workflow.Get(context.Background(), workflowId)

	return []byte(w.JSONDefinition), nil
	// stmt := `SELECT json_definition FROM workflows WHERE id = ?`
	// var jsonDef []byte

	// row := s.db.QueryRow(stmt, workflowId)
	// err := row.Scan(&jsonDef)
	// if err == sql.ErrNoRows {
	// 	return nil, fmt.Errorf("workflow not found: %s", workflowId)
	// } else if err != nil {
	// 	return nil, fmt.Errorf("failed to retrieve workflow: %w", err)
	// }

	// return jsonDef, nil
}

// GetWorkflows retrieves all workflow definitions from the database
func (s *service) GetWorkflows() ([][]byte, error) {

	ws, _ := s.client.Workflow.Query().All(context.Background())
	var workflows [][]byte
	for _, w := range ws {
		workflows = append(workflows, []byte(w.JSONDefinition))
	}

	return workflows, nil
	// stmt := `SELECT json_definition FROM workflows`
	// var workflows [][]byte
	// rows, err := s.db.Query(stmt)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to retrieve workflows: %w", err)
	// }
	// defer rows.Close()
	// for rows.Next() {
	// 	var data []byte
	// 	err := rows.Scan(&data)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("failed to scan workflow ID: %w", err)
	// 	}
	// 	workflows = append(workflows, data)
	// }
	// return workflows, nil
}
