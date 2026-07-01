package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"whatsbridge/internal/db"
)

// In-memory session store
var (
	sessions   = make(map[string]session)
	sessionsMu sync.RWMutex
)

type session struct {
	Username  string
	ExpiresAt time.Time
}

// InitUsers creates the users table and seeds the default admin user.
func InitUsers() {
	if db.LocalDB == nil {
		log.Println("Auth: DB not ready, will retry user seeding in background")
		go func() {
			for {
				time.Sleep(5 * time.Second)
				if db.LocalDB != nil {
					seedUsers()
					return
				}
			}
		}()
		return
	}
	seedUsers()
}

func seedUsers() {
	_, err := db.LocalDB.Exec(`CREATE TABLE IF NOT EXISTS users (
		id INT AUTO_INCREMENT PRIMARY KEY,
		username VARCHAR(100) UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		log.Printf("Auth: failed to create users table: %v", err)
		return
	}

	// Check if default user exists
	var count int
	err = db.LocalDB.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", "hashir").Scan(&count)
	if err != nil {
		log.Printf("Auth: failed to check default user: %v", err)
		return
	}

	if count == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte("Ashir9990*"), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("Auth: failed to hash password: %v", err)
			return
		}
		_, err = db.LocalDB.Exec("INSERT INTO users (username, password_hash) VALUES (?, ?)", "hashir", string(hash))
		if err != nil {
			log.Printf("Auth: failed to seed default user: %v", err)
			return
		}
		log.Println("Auth: default user 'hashir' created successfully.")
	}
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// LoginHandler handles POST /api/auth/login
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid request"})
		return
	}

	if db.LocalDB == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Database not ready"})
		return
	}

	var storedHash string
	err := db.LocalDB.QueryRow("SELECT password_hash FROM users WHERE username = ?", req.Username).Scan(&storedHash)
	if err == sql.ErrNoRows {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid credentials"})
		return
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Server error"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(req.Password)); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid credentials"})
		return
	}

	// Create session
	token := generateToken()
	sessionsMu.Lock()
	sessions[token] = session{
		Username:  req.Username,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour), // 30 days
	}
	sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "wb_session",
		Value:    token,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// AuthLogoutHandler handles POST /api/auth/logout
func AuthLogoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("wb_session")
	if err == nil {
		sessionsMu.Lock()
		delete(sessions, cookie.Value)
		sessionsMu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:   "wb_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// CheckAuthHandler returns current auth status
func CheckAuthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cookie, err := r.Cookie("wb_session")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"authenticated": false})
		return
	}

	sessionsMu.RLock()
	sess, ok := sessions[cookie.Value]
	sessionsMu.RUnlock()

	if !ok || time.Now().After(sess.ExpiresAt) {
		json.NewEncoder(w).Encode(map[string]interface{}{"authenticated": false})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"username":      sess.Username,
	})
}

// RequireAuth middleware protects page routes
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("wb_session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		sessionsMu.RLock()
		sess, ok := sessions[cookie.Value]
		sessionsMu.RUnlock()

		if !ok || time.Now().After(sess.ExpiresAt) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// Refresh session expiry on activity
		sessionsMu.Lock()
		sess.ExpiresAt = time.Now().Add(30 * 24 * time.Hour)
		sessions[cookie.Value] = sess
		sessionsMu.Unlock()

		next(w, r)
	}
}

// RequireAuthAPI middleware for API routes (returns 401 JSON instead of redirect)
func RequireAuthAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("wb_session")
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":"Not authenticated"}`)
			return
		}

		sessionsMu.RLock()
		sess, ok := sessions[cookie.Value]
		sessionsMu.RUnlock()

		if !ok || time.Now().After(sess.ExpiresAt) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":"Session expired"}`)
			return
		}

		next(w, r)
	}
}
