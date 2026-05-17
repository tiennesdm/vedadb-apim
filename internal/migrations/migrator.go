// Package migrations provides the migration runner for VedaDB schema management.
// It reads SQL files, executes them via the VedaDB wire protocol, tracks applied
// migrations in the schema_migrations table, and supports up/down migrations.
package migrations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Migration represents a single database migration.
type Migration struct {
	Name    string
	Version string // extracted from filename, e.g., "001" from "001_schema.sql"
	Up      string
	Down    string
}

// Migrator runs database migrations against a VedaDB instance.
type Migrator struct {
	addr       string
	conn       net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	timeout    time.Duration
	mu         sync.Mutex
	migrations []Migration
}

// VedaDBRequest is the JSON request format for the VedaDB wire protocol.
type VedaDBRequest struct {
	Type   string        `json:"type"`
	Query  string        `json:"query"`
	Params []interface{} `json:"params,omitempty"`
}

// VedaDBResponse is the JSON response format from the VedaDB wire protocol.
type VedaDBResponse struct {
	Status   string            `json:"status"`
	Rows     []json.RawMessage `json:"rows,omitempty"`
	RowCount int               `json:"row_count,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// NewMigrator creates a new migration runner for the given VedaDB address.
func NewMigrator(addr string) *Migrator {
	return &Migrator{
		addr:    addr,
		timeout: 30 * time.Second,
	}
}

// WithTimeout sets the connection timeout.
func (m *Migrator) WithTimeout(d time.Duration) *Migrator {
	m.timeout = d
	return m
}

// Connect establishes a TCP connection to VedaDB.
func (m *Migrator) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dialer := &net.Dialer{Timeout: m.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return fmt.Errorf("connect to VedaDB at %s: %w", m.addr, err)
	}
	m.conn = conn
	m.reader = bufio.NewReader(conn)
	m.writer = bufio.NewWriter(conn)

	// Ensure schema_migrations table exists
	if err := m.ensureMigrationsTable(); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}
	return nil
}

// Close closes the connection.
func (m *Migrator) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		return m.conn.Close()
	}
	return nil
}

// LoadMigrations loads all .sql files from the given directory, sorted by filename.
func (m *Migrator) LoadMigrations(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", dir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".sql") {
			files = append(files, name)
		}
	}

	sort.Strings(files)

	for _, f := range files {
		path := filepath.Join(dir, f)
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}

		version := extractVersion(f)
		up, down := splitUpDown(string(content))

		m.migrations = append(m.migrations, Migration{
			Name:    f,
			Version: version,
			Up:      up,
			Down:    down,
		})
	}
	return nil
}

// Up runs all pending migrations.
func (m *Migrator) Up(ctx context.Context) (int, error) {
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return 0, fmt.Errorf("get applied migrations: %w", err)
	}

	appliedSet := make(map[string]bool)
	for _, a := range applied {
		appliedSet[a] = true
	}

	var count int
	for _, mig := range m.migrations {
		if appliedSet[mig.Name] {
			continue // already applied
		}

		if err := m.runMigration(ctx, mig.Name, mig.Up); err != nil {
			return count, fmt.Errorf("apply migration %s: %w", mig.Name, err)
		}
		count++
	}
	return count, nil
}

// UpTo runs migrations up to and including the specified version.
func (m *Migrator) UpTo(ctx context.Context, version string) (int, error) {
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return 0, fmt.Errorf("get applied migrations: %w", err)
	}

	appliedSet := make(map[string]bool)
	for _, a := range applied {
		appliedSet[a] = true
	}

	var count int
	for _, mig := range m.migrations {
		if appliedSet[mig.Name] {
			continue
		}
		if mig.Version > version {
			break
		}
		if err := m.runMigration(ctx, mig.Name, mig.Up); err != nil {
			return count, fmt.Errorf("apply migration %s: %w", mig.Name, err)
		}
		count++
	}
	return count, nil
}

// Down rolls back the last N migrations.
func (m *Migrator) Down(ctx context.Context, n int) (int, error) {
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return 0, fmt.Errorf("get applied migrations: %w", err)
	}

	if len(applied) == 0 {
		return 0, nil
	}

	// Build reverse lookup from migration name to migration
	migMap := make(map[string]Migration)
	for _, mig := range m.migrations {
		migMap[mig.Name] = mig
	}

	var count int
	// Roll back from most recently applied
	for i := len(applied) - 1; i >= 0 && n > 0; i-- {
		name := applied[i]
		mig, ok := migMap[name]
		if !ok {
			return count, fmt.Errorf("migration %s not found for rollback", name)
		}
		if mig.Down == "" {
			return count, fmt.Errorf("migration %s has no down script", name)
		}
		if err := m.runMigrationDown(ctx, mig.Name, mig.Down); err != nil {
			return count, fmt.Errorf("rollback migration %s: %w", mig.Name, err)
		}
		count++
		n--
	}
	return count, nil
}

// Status returns the current migration status.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return nil, fmt.Errorf("get applied migrations: %w", err)
	}

	appliedSet := make(map[string]bool)
	for _, a := range applied {
		appliedSet[a] = true
	}

	var statuses []MigrationStatus
	for _, mig := range m.migrations {
		statuses = append(statuses, MigrationStatus{
			Name:    mig.Name,
			Version: mig.Version,
			Applied: appliedSet[mig.Name],
		})
	}
	return statuses, nil
}

// MigrationStatus represents the status of a single migration.
type MigrationStatus struct {
	Name    string
	Version string
	Applied bool
}

// GetAppliedMigrations returns the list of applied migration names.
func (m *Migrator) GetAppliedMigrations(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req := VedaDBRequest{
		Type:  "query",
		Query: "SELECT name FROM schema_migrations ORDER BY id ASC",
	}

	resp, err := m.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		// If the table doesn't exist, return empty
		if strings.Contains(resp.Error, "no such table") || strings.Contains(resp.Error, "not found") {
			return []string{}, nil
		}
		return nil, fmt.Errorf("query applied migrations: %s", resp.Error)
	}

	var names []string
	for _, row := range resp.Rows {
		var r struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(row, &r); err != nil {
			return nil, fmt.Errorf("unmarshal migration row: %w", err)
		}
		names = append(names, r.Name)
	}
	return names, nil
}

// Migrations returns loaded migrations.
func (m *Migrator) Migrations() []Migration {
	return m.migrations
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (m *Migrator) ensureMigrationsTable() error {
	req := VedaDBRequest{
		Type: "query",
		Query: `CREATE TABLE IF NOT EXISTS schema_migrations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255) UNIQUE NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	resp, err := m.sendRequest(context.Background(), req)
	if err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("create schema_migrations table: %s", resp.Error)
	}
	return nil
}

func (m *Migrator) runMigration(ctx context.Context, name, sql string) error {
	if strings.TrimSpace(sql) == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Execute each statement separately (split by semicolons outside of quotes)
	statements := splitStatements(sql)
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		req := VedaDBRequest{
			Type:  "query",
			Query: stmt,
		}

		resp, err := m.sendRequest(ctx, req)
		if err != nil {
			return fmt.Errorf("execute statement: %w", err)
		}
		if resp.Status != "ok" {
			// Ignore "table already exists" and "duplicate" errors for migrations
			errMsg := strings.ToLower(resp.Error)
			if strings.Contains(errMsg, "already exists") || strings.Contains(errMsg, "duplicate") {
				continue
			}
			return fmt.Errorf("statement failed: %s", resp.Error)
		}
	}

	// Record migration as applied
	req := VedaDBRequest{
		Type:   "query",
		Query:  "INSERT OR IGNORE INTO schema_migrations (name) VALUES (?)",
		Params: []interface{}{name},
	}
	resp, err := m.sendRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if resp.Status != "ok" {
		return fmt.Errorf("record migration %s: %s", name, resp.Error)
	}
	return nil
}

func (m *Migrator) runMigrationDown(ctx context.Context, name, sql string) error {
	if strings.TrimSpace(sql) == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	statements := splitStatements(sql)
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		req := VedaDBRequest{
			Type:  "query",
			Query: stmt,
		}

		resp, err := m.sendRequest(ctx, req)
		if err != nil {
			return fmt.Errorf("execute down statement: %w", err)
		}
		if resp.Status != "ok" {
			return fmt.Errorf("down statement failed: %s", resp.Error)
		}
	}

	// Remove from applied
	req := VedaDBRequest{
		Type:   "query",
		Query:  "DELETE FROM schema_migrations WHERE name = ?",
		Params: []interface{}{name},
	}
	resp, err := m.sendRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("remove migration record: %w", err)
	}
	if resp.Status != "ok" {
		return fmt.Errorf("remove migration %s: %s", name, resp.Error)
	}
	return nil
}

func (m *Migrator) sendRequest(ctx context.Context, req VedaDBRequest) (*VedaDBResponse, error) {
	// Set deadline from context
	deadline, ok := ctx.Deadline()
	if ok {
		m.conn.SetDeadline(deadline)
	} else {
		m.conn.SetDeadline(time.Now().Add(m.timeout))
	}
	defer m.conn.SetDeadline(time.Time{})

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	data = append(data, '\n')
	if _, err := m.writer.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	if err := m.writer.Flush(); err != nil {
		return nil, fmt.Errorf("flush request: %w", err)
	}

	line, err := m.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp VedaDBResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// SQL parsing helpers
// ---------------------------------------------------------------------------

func extractVersion(filename string) string {
	// Extract version prefix like "001" from "001_schema.sql"
	parts := strings.SplitN(filename, "_", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return filename
}

func splitUpDown(content string) (string, string) {
	// Split on "-- +migrate Down" or "-- @down" or similar markers
	downMarkers := []string{
		"-- +migrate Down",
		"-- +migrate down",
		"-- @down",
		"-- DOWN",
		"-- migrate:down",
	}

	lower := strings.ToLower(content)
	for _, marker := range downMarkers {
		idx := strings.Index(lower, strings.ToLower(marker))
		if idx >= 0 {
			return strings.TrimSpace(content[:idx]), strings.TrimSpace(content[idx+len(marker):])
		}
	}
	return strings.TrimSpace(content), ""
}

func splitStatements(sql string) []string {
	var statements []string
	var buf bytes.Buffer
	inString := false
	stringChar := rune(0)
	escaped := false

	for _, ch := range sql {
		if escaped {
			buf.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			buf.WriteRune(ch)
			escaped = true
			continue
		}
		if inString {
			buf.WriteRune(ch)
			if ch == stringChar {
				inString = false
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			inString = true
			stringChar = ch
			buf.WriteRune(ch)
			continue
		}
		if ch == ';' {
			buf.WriteRune(ch)
			statements = append(statements, buf.String())
			buf.Reset()
			continue
		}
		buf.WriteRune(ch)
	}

	remaining := buf.String()
	if strings.TrimSpace(remaining) != "" {
		statements = append(statements, remaining)
	}
	return statements
}
