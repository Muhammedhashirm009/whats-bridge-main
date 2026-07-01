package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
	"whatsbridge/internal/db"
)

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

// seedUsers creates the users and sessions tables and seeds the default admin user.
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

	_, err = db.LocalDB.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		token VARCHAR(64) PRIMARY KEY,
		username VARCHAR(100) NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		log.Printf("Auth: failed to create sessions table: %v", err)
		return
	}

	// Read admin credentials from environment variables
	adminUser := os.Getenv("ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}

	adminPass := os.Getenv("ADMIN_PASS")
	if adminPass == "" {
		// Generate a random 16-character password
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			log.Printf("Auth: failed to generate random password: %v", err)
			return
		}
		adminPass = hex.EncodeToString(b)
		log.Printf("⚠️ Generated admin password: %s — set ADMIN_PASS env var to change", adminPass)
	}

	// Check if default user exists
	var count int
	err = db.LocalDB.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", adminUser).Scan(&count)
	if err != nil {
		log.Printf("Auth: failed to check default user: %v", err)
		return
	}

	if count == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("Auth: failed to hash password: %v", err)
			return
		}
		_, err = db.LocalDB.Exec("INSERT INTO users (username, password_hash) VALUES (?, ?)", adminUser, string(hash))
		if err != nil {
			log.Printf("Auth: failed to seed default user: %v", err)
			return
		}
		log.Printf("Auth: default user '%s' created successfully.", adminUser)
	}
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Printf("Auth: failed to generate secure token: %v", err)
		return ""
	}
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

	database := db.GetDB()
	if database == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Database not ready"})
		return
	}

	var storedHash string
	err := database.QueryRow("SELECT password_hash FROM users WHERE username = ?", req.Username).Scan(&storedHash)
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
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Failed to create session"})
		return
	}

	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	
	// Clean up expired sessions periodically on login
	_, _ = database.Exec("DELETE FROM sessions WHERE expires_at < NOW()")

	_, err = database.Exec(
		"INSERT INTO sessions (token, username, expires_at) VALUES (?, ?, ?)",
		token, req.Username, expiresAt,
	)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Failed to store session"})
		return
	}

	// Determine cookie security based on environment
	isProduction := os.Getenv("APP_ENV") == "production"
	sameSite := http.SameSiteLaxMode
	if isProduction {
		sameSite = http.SameSiteStrictMode
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "wb_session",
		Value:    token,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   isProduction,
		SameSite: sameSite,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// AuthLogoutHandler handles POST /api/auth/logout
func AuthLogoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("wb_session")
	if err == nil {
		database := db.GetDB()
		if database != nil {
			_, _ = database.Exec("DELETE FROM sessions WHERE token = ?", cookie.Value)
		}
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

	database := db.GetDB()
	if database == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"authenticated": false})
		return
	}

	var username string
	var expiresAt time.Time
	err = database.QueryRow(
		"SELECT username, expires_at FROM sessions WHERE token = ?",
		cookie.Value,
	).Scan(&username, &expiresAt)

	if err != nil || time.Now().After(expiresAt) {
		if err == nil {
			// Clean up expired session
			_, _ = database.Exec("DELETE FROM sessions WHERE token = ?", cookie.Value)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"authenticated": false})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"username":      username,
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

		database := db.GetDB()
		if database == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		var username string
		var expiresAt time.Time
		err = database.QueryRow(
			"SELECT username, expires_at FROM sessions WHERE token = ?",
			cookie.Value,
		).Scan(&username, &expiresAt)

		if err != nil || time.Now().After(expiresAt) {
			if err == nil {
				_, _ = database.Exec("DELETE FROM sessions WHERE token = ?", cookie.Value)
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// Refresh session expiry on activity
		newExpiry := time.Now().Add(30 * 24 * time.Hour)
		_, _ = database.Exec(
			"UPDATE sessions SET expires_at = ? WHERE token = ?",
			newExpiry, cookie.Value,
		)

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

		database := db.GetDB()
		if database == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"error":"Database not ready"}`)
			return
		}

		var username string
		var expiresAt time.Time
		err = database.QueryRow(
			"SELECT username, expires_at FROM sessions WHERE token = ?",
			cookie.Value,
		).Scan(&username, &expiresAt)

		if err != nil || time.Now().After(expiresAt) {
			if err == nil {
				_, _ = database.Exec("DELETE FROM sessions WHERE token = ?", cookie.Value)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":"Session expired"}`)
			return
		}

		next(w, r)
	}
}

// IsAuthenticated checks if the request has a valid session cookie.
func IsAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie("wb_session")
	if err != nil {
		return false
	}

	database := db.GetDB()
	if database == nil {
		return false
	}

	var expiresAt time.Time
	err = database.QueryRow(
		"SELECT expires_at FROM sessions WHERE token = ?",
		cookie.Value,
	).Scan(&expiresAt)

	if err != nil || time.Now().After(expiresAt) {
		return false
	}
	return true
}
