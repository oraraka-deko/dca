package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// LogRecord represents a log entry stored in the database.
type LogRecord struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	MetaJSON  string    `json:"meta_json,omitempty"`
}

// TaskRecord represents background task history stored in the database.
type TaskRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Command     string    `json:"command"`
	Result      string    `json:"result"`
	Error       string    `json:"error"`
	Progress    float64   `json:"progress"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	MetaJSON    string    `json:"meta_json,omitempty"`
}

// LogFilter contains query parameters for searching logs.
type LogFilter struct {
	Level     string     `json:"level"`
	Component string     `json:"component"`
	Query     string     `json:"query"`
	Limit     int        `json:"limit"`
	Offset    int        `json:"offset"`
	StartTime *time.Time `json:"start_time,omitempty"`
	EndTime   *time.Time `json:"end_time,omitempty"`
}

// TaskFilter contains query parameters for searching task history.
type TaskFilter struct {
	Status string `json:"status"`
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// Store defines the interface for database operations.
type Store interface {
	InitSchema(ctx context.Context) error
	Close() error
	GetDriverName() string

	SaveConfig(ctx context.Context, key string, val string) error
	GetConfig(ctx context.Context, key string) (string, error)
	GetAllConfigs(ctx context.Context) (map[string]string, error)

	InsertLog(ctx context.Context, level, component, message, metaJSON string) error
	QueryLogs(ctx context.Context, filter LogFilter) ([]LogRecord, error)

	SaveTask(ctx context.Context, task TaskRecord) error
	GetTask(ctx context.Context, id string) (TaskRecord, bool, error)
	QueryTasks(ctx context.Context, filter TaskFilter) ([]TaskRecord, error)
}

// SQLStore implements Store for both SQLite and PostgreSQL.
type SQLStore struct {
	mu         sync.RWMutex
	db         *sql.DB
	driverName string // "sqlite" or "postgres"
}

// NewStore initializes a new Store instance based on dbType ("sqlite" or "postgres").
func NewStore(dbType string, connStr string) (Store, error) {
	dbType = strings.ToLower(strings.TrimSpace(dbType))
	var driver string

	switch dbType {
	case "sqlite", "sqlite3", "":
		driver = "sqlite"
		if connStr == "" {
			connStr = "mymcp.db"
		}
	case "postgres", "postgresql":
		driver = "postgres"
		if connStr == "" {
			return nil, fmt.Errorf("connection string required for postgres database")
		}
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, fmt.Errorf("failed opening %s database: %w", driver, err)
	}

	if driver == "sqlite" {
		db.SetMaxOpenConns(1) // SQLite write safety
	} else {
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(5 * time.Minute)
	}

	store := &SQLStore{
		db:         db,
		driverName: driver,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.InitSchema(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed initializing database schema: %w", err)
	}

	return store, nil
}

func (s *SQLStore) GetDriverName() string {
	return s.driverName
}

func (s *SQLStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// InitSchema creates tables and indexes for SQLite or PostgreSQL.
func (s *SQLStore) InitSchema(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var schema string
	if s.driverName == "postgres" {
		schema = `
		CREATE TABLE IF NOT EXISTS server_configs (
			config_key VARCHAR(255) PRIMARY KEY,
			config_val TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS server_logs (
			id SERIAL PRIMARY KEY,
			timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			level VARCHAR(50) NOT NULL,
			component VARCHAR(100) NOT NULL,
			message TEXT NOT NULL,
			meta_json TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON server_logs(timestamp);
		CREATE INDEX IF NOT EXISTS idx_logs_level ON server_logs(level);

		CREATE TABLE IF NOT EXISTS task_history (
			id VARCHAR(128) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL,
			command TEXT,
			result TEXT,
			error TEXT,
			progress DOUBLE PRECISION DEFAULT 0,
			created_at TIMESTAMP NOT NULL,
			started_at TIMESTAMP,
			completed_at TIMESTAMP,
			meta_json TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_tasks_status ON task_history(status);
		`
	} else { // sqlite
		schema = `
		CREATE TABLE IF NOT EXISTS server_configs (
			config_key TEXT PRIMARY KEY,
			config_val TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS server_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			level TEXT NOT NULL,
			component TEXT NOT NULL,
			message TEXT NOT NULL,
			meta_json TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON server_logs(timestamp);
		CREATE INDEX IF NOT EXISTS idx_logs_level ON server_logs(level);

		CREATE TABLE IF NOT EXISTS task_history (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			command TEXT,
			result TEXT,
			error TEXT,
			progress REAL DEFAULT 0,
			created_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			meta_json TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_tasks_status ON task_history(status);
		`
	}

	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *SQLStore) SaveConfig(ctx context.Context, key string, val string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var query string
	now := time.Now().UTC()
	if s.driverName == "postgres" {
		query = `
		INSERT INTO server_configs (config_key, config_val, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (config_key) DO UPDATE SET
			config_val = EXCLUDED.config_val,
			updated_at = EXCLUDED.updated_at
		`
	} else {
		query = `
		INSERT INTO server_configs (config_key, config_val, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(config_key) DO UPDATE SET
			config_val = excluded.config_val,
			updated_at = excluded.updated_at
		`
	}
	_, err := s.db.ExecContext(ctx, query, key, val, now)
	return err
}

func (s *SQLStore) GetConfig(ctx context.Context, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var query string
	if s.driverName == "postgres" {
		query = `SELECT config_val FROM server_configs WHERE config_key = $1`
	} else {
		query = `SELECT config_val FROM server_configs WHERE config_key = ?`
	}

	var val string
	err := s.db.QueryRowContext(ctx, query, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("config key %s not found", key)
	}
	return val, err
}

func (s *SQLStore) GetAllConfigs(ctx context.Context) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `SELECT config_key, config_val FROM server_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		res[k] = v
	}
	return res, nil
}

func (s *SQLStore) InsertLog(ctx context.Context, level, component, message, metaJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	var query string
	if s.driverName == "postgres" {
		query = `INSERT INTO server_logs (timestamp, level, component, message, meta_json) VALUES ($1, $2, $3, $4, $5)`
	} else {
		query = `INSERT INTO server_logs (timestamp, level, component, message, meta_json) VALUES (?, ?, ?, ?, ?)`
	}

	_, err := s.db.ExecContext(ctx, query, now, level, component, message, metaJSON)
	return err
}

func (s *SQLStore) QueryLogs(ctx context.Context, filter LogFilter) ([]LogRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sb strings.Builder
	var args []interface{}
	argIdx := 1

	sb.WriteString(`SELECT id, timestamp, level, component, message, COALESCE(meta_json, '') FROM server_logs WHERE 1=1`)

	addCond := func(cond string, val interface{}) {
		if s.driverName == "postgres" {
			sb.WriteString(fmt.Sprintf(" AND %s $%d", cond, argIdx))
		} else {
			sb.WriteString(fmt.Sprintf(" AND %s ?", cond))
		}
		args = append(args, val)
		argIdx++
	}

	if filter.Level != "" {
		addCond("level =", strings.ToUpper(filter.Level))
	}
	if filter.Component != "" {
		addCond("component =", filter.Component)
	}
	if filter.Query != "" {
		q := "%" + filter.Query + "%"
		if s.driverName == "postgres" {
			sb.WriteString(fmt.Sprintf(" AND (message ILIKE $%d OR component ILIKE $%d)", argIdx, argIdx))
			args = append(args, q)
			argIdx++
		} else {
			sb.WriteString(" AND (message LIKE ? OR component LIKE ?)")
			args = append(args, q, q)
			argIdx += 2
		}
	}
	if filter.StartTime != nil {
		addCond("timestamp >=", *filter.StartTime)
	}
	if filter.EndTime != nil {
		addCond("timestamp <=", *filter.EndTime)
	}

	sb.WriteString(" ORDER BY timestamp DESC")

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	if s.driverName == "postgres" {
		sb.WriteString(fmt.Sprintf(" LIMIT $%d", argIdx))
		args = append(args, limit)
		argIdx++
		if filter.Offset > 0 {
			sb.WriteString(fmt.Sprintf(" OFFSET $%d", argIdx))
			args = append(args, filter.Offset)
		}
	} else {
		sb.WriteString(" LIMIT ?")
		args = append(args, limit)
		if filter.Offset > 0 {
			sb.WriteString(" OFFSET ?")
			args = append(args, filter.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []LogRecord
	for rows.Next() {
		var rec LogRecord
		var ts interface{}
		if err := rows.Scan(&rec.ID, &ts, &rec.Level, &rec.Component, &rec.Message, &rec.MetaJSON); err != nil {
			return nil, err
		}
		rec.Timestamp = parseTime(ts)
		logs = append(logs, rec)
	}
	return logs, nil
}

func (s *SQLStore) SaveTask(ctx context.Context, task TaskRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var query string
	if s.driverName == "postgres" {
		query = `
		INSERT INTO task_history (id, name, status, command, result, error, progress, created_at, started_at, completed_at, meta_json)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			status = EXCLUDED.status,
			command = EXCLUDED.command,
			result = EXCLUDED.result,
			error = EXCLUDED.error,
			progress = EXCLUDED.progress,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at,
			meta_json = EXCLUDED.meta_json
		`
	} else {
		query = `
		INSERT INTO task_history (id, name, status, command, result, error, progress, created_at, started_at, completed_at, meta_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			status = excluded.status,
			command = excluded.command,
			result = excluded.result,
			error = excluded.error,
			progress = excluded.progress,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			meta_json = excluded.meta_json
		`
	}

	_, err := s.db.ExecContext(ctx, query,
		task.ID, task.Name, task.Status, task.Command, task.Result, task.Error,
		task.Progress, task.CreatedAt, task.StartedAt, task.CompletedAt, task.MetaJSON,
	)
	return err
}

func (s *SQLStore) GetTask(ctx context.Context, id string) (TaskRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var query string
	if s.driverName == "postgres" {
		query = `SELECT id, name, status, COALESCE(command, ''), COALESCE(result, ''), COALESCE(error, ''), progress, created_at, started_at, completed_at, COALESCE(meta_json, '') FROM task_history WHERE id = $1`
	} else {
		query = `SELECT id, name, status, COALESCE(command, ''), COALESCE(result, ''), COALESCE(error, ''), progress, created_at, started_at, completed_at, COALESCE(meta_json, '') FROM task_history WHERE id = ?`
	}

	var rec TaskRecord
	var cAt, sAt, compAt interface{}
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&rec.ID, &rec.Name, &rec.Status, &rec.Command, &rec.Result, &rec.Error,
		&rec.Progress, &cAt, &sAt, &compAt, &rec.MetaJSON,
	)
	if err == sql.ErrNoRows {
		return TaskRecord{}, false, nil
	}
	if err != nil {
		return TaskRecord{}, false, err
	}
	rec.CreatedAt = parseTime(cAt)
	rec.StartedAt = parseTime(sAt)
	rec.CompletedAt = parseTime(compAt)
	return rec, true, nil
}

func (s *SQLStore) QueryTasks(ctx context.Context, filter TaskFilter) ([]TaskRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sb strings.Builder
	var args []interface{}
	argIdx := 1

	sb.WriteString(`SELECT id, name, status, COALESCE(command, ''), COALESCE(result, ''), COALESCE(error, ''), progress, created_at, started_at, completed_at, COALESCE(meta_json, '') FROM task_history WHERE 1=1`)

	if filter.Status != "" {
		if s.driverName == "postgres" {
			sb.WriteString(fmt.Sprintf(" AND status = $%d", argIdx))
		} else {
			sb.WriteString(" AND status = ?")
		}
		args = append(args, filter.Status)
		argIdx++
	}

	if filter.Query != "" {
		q := "%" + filter.Query + "%"
		if s.driverName == "postgres" {
			sb.WriteString(fmt.Sprintf(" AND (name ILIKE $%d OR command ILIKE $%d)", argIdx, argIdx))
			args = append(args, q)
			argIdx++
		} else {
			sb.WriteString(" AND (name LIKE ? OR command LIKE ?)")
			args = append(args, q, q)
			argIdx += 2
		}
	}

	sb.WriteString(" ORDER BY created_at DESC")

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if s.driverName == "postgres" {
		sb.WriteString(fmt.Sprintf(" LIMIT $%d", argIdx))
		args = append(args, limit)
		argIdx++
		if filter.Offset > 0 {
			sb.WriteString(fmt.Sprintf(" OFFSET $%d", argIdx))
			args = append(args, filter.Offset)
		}
	} else {
		sb.WriteString(" LIMIT ?")
		args = append(args, limit)
		if filter.Offset > 0 {
			sb.WriteString(" OFFSET ?")
			args = append(args, filter.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskRecord
	for rows.Next() {
		var rec TaskRecord
		var cAt, sAt, compAt interface{}
		if err := rows.Scan(
			&rec.ID, &rec.Name, &rec.Status, &rec.Command, &rec.Result, &rec.Error,
			&rec.Progress, &cAt, &sAt, &compAt, &rec.MetaJSON,
		); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(cAt)
		rec.StartedAt = parseTime(sAt)
		rec.CompletedAt = parseTime(compAt)
		tasks = append(tasks, rec)
	}
	return tasks, nil
}

func parseTime(val interface{}) time.Time {
	if val == nil {
		return time.Time{}
	}
	switch v := val.(type) {
	case time.Time:
		return v
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err == nil {
			return t
		}
		t, err = time.Parse("2006-01-02 15:04:05", v)
		if err == nil {
			return t
		}
		t, err = time.Parse("2006-01-02T15:04:05Z", v)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}

// MemoryJSONHelper returns formatted JSON string for metadata maps.
func MetadataToJSON(v interface{}) string {
	if v == nil {
		return "{}"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
