package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var LocalDB *sql.DB

// dbMu protects LocalDB from concurrent access.
var dbMu sync.RWMutex

// GetDB returns the current database connection in a thread-safe manner.
func GetDB() *sql.DB {
	dbMu.RLock()
	defer dbMu.RUnlock()
	return LocalDB
}

// SetDB sets the global database connection in a thread-safe manner.
func SetDB(d *sql.DB) {
	dbMu.Lock()
	defer dbMu.Unlock()
	LocalDB = d
}

func InitDB(dsn string) {
	var err error
	d, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Printf("Failed to open MySQL: %v — will retry", err)
		go retryDB(dsn)
		return
	}
	SetDB(d)

	// Test connection with retry
	if err = d.Ping(); err != nil {
		log.Printf("Failed to connect to MySQL: %v — will retry in background", err)
		go retryDB(dsn)
		return
	}

	createTables()
	log.Println("MySQL database initialized successfully.")
}

func retryDB(dsn string) {
	for {
		time.Sleep(5 * time.Second)
		d, err := sql.Open("mysql", dsn)
		if err != nil {
			log.Printf("MySQL retry: open failed: %v", err)
			continue
		}
		if err = d.Ping(); err != nil {
			log.Printf("MySQL retry: ping failed: %v", err)
			continue
		}
		SetDB(d)
		createTables()
		log.Println("MySQL database initialized successfully (on retry).")
		return
	}
}

func createTables() {
	d := GetDB()
	if d == nil {
		log.Printf("createTables: database not initialized, skipping")
		return
	}

	queries := []string{
		`CREATE TABLE IF NOT EXISTS usage_logs (
			id INT AUTO_INCREMENT PRIMARY KEY,
			date VARCHAR(10) UNIQUE,
			messages_sent INT DEFAULT 0,
			messages_failed INT DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS scheduled_messages (
			id INT AUTO_INCREMENT PRIMARY KEY,
			recipient VARCHAR(30) NOT NULL,
			message TEXT NOT NULL,
			scheduled_for DATETIME NOT NULL,
			status VARCHAR(20) DEFAULT 'pending'
		);`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			key_hash VARCHAR(64) NOT NULL UNIQUE,
			key_prefix VARCHAR(8) NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_used_at DATETIME NULL,
			is_active TINYINT(1) DEFAULT 1
		);`,
	}

	for _, query := range queries {
		_, err := d.Exec(query)
		if err != nil {
			fmt.Printf("Error creating table: %v\n", err)
		}
	}
}

func LogMessageUsage(success bool) {
	d := GetDB()
	if d == nil {
		return
	}

	today := time.Now().Format("2006-01-02")

	// Ensure row exists for today
	_, _ = d.Exec(`INSERT IGNORE INTO usage_logs (date) VALUES (?)`, today)

	if success {
		_, _ = d.Exec(`UPDATE usage_logs SET messages_sent = messages_sent + 1 WHERE date = ?`, today)
	} else {
		_, _ = d.Exec(`UPDATE usage_logs SET messages_failed = messages_failed + 1 WHERE date = ?`, today)
	}
}

type Metrics struct {
	TotalSent      int `json:"total_sent"`
	TotalFailed    int `json:"total_failed"`
	ScheduledCount int `json:"scheduled_count"`
}

func GetMetrics() (Metrics, error) {
	var m Metrics
	d := GetDB()
	if d == nil {
		return m, fmt.Errorf("database not initialized")
	}

	err := d.QueryRow(`SELECT IFNULL(SUM(messages_sent),0), IFNULL(SUM(messages_failed),0) FROM usage_logs`).Scan(&m.TotalSent, &m.TotalFailed)
	if err != nil {
		return m, fmt.Errorf("failed to query usage metrics: %w", err)
	}

	err = d.QueryRow(`SELECT COUNT(*) FROM scheduled_messages WHERE status = 'pending'`).Scan(&m.ScheduledCount)
	if err != nil {
		return m, fmt.Errorf("failed to query scheduled count: %w", err)
	}

	return m, nil
}

func AddScheduledMessage(recipient, message, scheduledFor string) error {
	d := GetDB()
	if d == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := d.Exec(`INSERT INTO scheduled_messages (recipient, message, scheduled_for) VALUES (?, ?, ?)`,
		recipient, message, scheduledFor)
	return err
}

type ScheduledMessage struct {
	ID        int
	Recipient string
	Message   string
}

func GetPendingMessages(now string) ([]ScheduledMessage, error) {
	d := GetDB()
	if d == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := d.Query(`SELECT id, recipient, message FROM scheduled_messages WHERE status = 'pending' AND scheduled_for <= ?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ScheduledMessage
	for rows.Next() {
		var m ScheduledMessage
		if err := rows.Scan(&m.ID, &m.Recipient, &m.Message); err == nil {
			msgs = append(msgs, m)
		}
	}
	if err := rows.Err(); err != nil {
		return msgs, fmt.Errorf("error iterating pending messages: %w", err)
	}
	return msgs, nil
}

func UpdateScheduledMessageStatus(id int, status string) error {
	d := GetDB()
	if d == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := d.Exec(`UPDATE scheduled_messages SET status = ? WHERE id = ?`, status, id)
	return err
}

// ─── API Key Management ─────────────────────────────────────

type APIKey struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	KeyPrefix string    `json:"key_prefix"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  *string   `json:"last_used_at"`
	IsActive  bool      `json:"is_active"`
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// CreateAPIKey generates a new API key, stores its hash, and returns the raw key.
func CreateAPIKey(name string) (string, error) {
	d := GetDB()
	if d == nil {
		return "", fmt.Errorf("database not initialized")
	}

	// Generate 32-byte random key
	b := make([]byte, 32)
	n, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	if n != 32 {
		return "", fmt.Errorf("failed to generate random key: expected 32 bytes, got %d", n)
	}
	rawKey := "wb_" + hex.EncodeToString(b) // wb_ prefix for easy identification
	keyHash := hashKey(rawKey)
	keyPrefix := rawKey[:11] // "wb_" + first 8 hex chars

	_, err = d.Exec(
		`INSERT INTO api_keys (name, key_hash, key_prefix) VALUES (?, ?, ?)`,
		name, keyHash, keyPrefix,
	)
	if err != nil {
		return "", err
	}

	log.Printf("API key '%s' created (prefix: %s...)", name, keyPrefix)
	return rawKey, nil
}

// ListAPIKeys returns all API keys (without the actual key, just metadata).
func ListAPIKeys() ([]APIKey, error) {
	d := GetDB()
	if d == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := d.Query(`SELECT id, name, key_prefix, created_at, last_used_at, is_active FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var createdStr string
		var lastUsedStr sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &createdStr, &lastUsedStr, &k.IsActive); err != nil {
			continue
		}
		k.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdStr)
		if lastUsedStr.Valid {
			k.LastUsed = &lastUsedStr.String
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return keys, fmt.Errorf("error iterating API keys: %w", err)
	}
	return keys, nil
}

// DeleteAPIKey removes an API key by ID.
func DeleteAPIKey(id int) error {
	d := GetDB()
	if d == nil {
		return fmt.Errorf("database not initialized")
	}
	result, err := d.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("API key not found")
	}
	log.Printf("API key #%d deleted", id)
	return nil
}

// ValidateAPIKey checks if a raw API key is valid and active.
func ValidateAPIKey(rawKey string) bool {
	d := GetDB()
	if d == nil || rawKey == "" {
		return false
	}

	keyHash := hashKey(rawKey)
	var isActive bool
	err := d.QueryRow(`SELECT is_active FROM api_keys WHERE key_hash = ?`, keyHash).Scan(&isActive)
	if err != nil {
		return false
	}

	if isActive {
		// Update last_used_at
		go func() {
			dd := GetDB()
			if dd != nil {
				dd.Exec(`UPDATE api_keys SET last_used_at = NOW() WHERE key_hash = ?`, keyHash)
			}
		}()
	}

	return isActive
}

// HasAnyAPIKeys checks if there are any API keys configured.
func HasAnyAPIKeys() bool {
	d := GetDB()
	if d == nil {
		return false
	}
	var count int
	err := d.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE is_active = 1`).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}
