package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"whatsbridge/internal/auth"
	"whatsbridge/internal/bot"
	"whatsbridge/internal/db"
	"time"
)

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	client := bot.GetClient()
	if client == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"loggedIn":  false,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected": client.IsConnected(),
		"loggedIn":  client.IsLoggedIn(),
	})
}

func SendHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 50MB for multipart uploads
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	w.Header().Set("Content-Type", "application/json")

	client := bot.GetClient()
	if client == nil || !client.IsConnected() || !client.IsLoggedIn() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Bot is not connected to WhatsApp",
		})
		return
	}

	err := r.ParseMultipartForm(50 << 20)
	var to, message string
	var fileBytes []byte
	var fileName string

	if err == nil {
		to = r.FormValue("to")
		message = r.FormValue("message")

		file, header, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			fileBytes, err = io.ReadAll(file)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Failed to read uploaded file"})
				return
			}
			fileName = header.Filename
		}
	} else {
		var req struct {
			To      string `json:"to"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Invalid request format",
			})
			return
		}
		to = req.To
		message = req.Message
	}

	if to == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Phone number is required",
		})
		return
	}

	log.Printf("[SendHandler] to=%q message=%q file=%q", to, message, fileName)

	var sendErr error
	if len(fileBytes) > 0 {
		// Use secure temp file to prevent path traversal
		tmpFile, err := os.CreateTemp("", "wb-upload-*")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Internal error saving file"})
			return
		}
		tmpPath := tmpFile.Name()
		defer os.Remove(tmpPath)

		if _, err := tmpFile.Write(fileBytes); err != nil {
			tmpFile.Close()
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Internal error saving file"})
			return
		}
		tmpFile.Close()

		sendErr = bot.SendMediaMessage(to, tmpPath, message)
	} else {
		if message == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Message is required"})
			return
		}
		sendErr = bot.SendTextMessage(to, message)
	}

	if sendErr != nil {
		log.Printf("[SendHandler] FAILED to=%q err=%v", to, sendErr)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Failed to send message: %v", sendErr),
		})
		return
	}

	log.Printf("[SendHandler] SUCCESS to=%q", to)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	m, err := db.GetMetrics()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(m)
}

func ScheduleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	w.Header().Set("Content-Type", "application/json")

	var req struct {
		To           string `json:"to"`
		Message      string `json:"message"`
		ScheduledFor string `json:"scheduled_for"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid JSON"})
		return
	}

	t, err := time.Parse(time.RFC3339, req.ScheduledFor)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid time format"})
		return
	}

	err = db.AddScheduledMessage(req.To, req.Message, t.UTC().Format(time.RFC3339))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Database error"})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func BulkSendHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Messages []struct {
			To      string `json:"to"`
			Message string `json:"message"`
		} `json:"messages"`
		IntervalMs int `json:"interval_ms"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid format"})
		return
	}

	// Cap bulk send to prevent abuse
	if len(req.Messages) > 1000 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Maximum 1000 messages per bulk send"})
		return
	}

	go func() {
		for _, m := range req.Messages {
			client := bot.GetClient()
			if client == nil || !client.IsConnected() || !client.IsLoggedIn() {
				break
			}
			bot.SendTextMessage(m.To, m.Message)

			if req.IntervalMs > 0 {
				time.Sleep(time.Duration(req.IntervalMs) * time.Millisecond)
			}
		}
	}()

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": fmt.Sprintf("Started dispatching %d messages", len(req.Messages))})
}

func QRHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	bot.QRMutex.Lock()
	defer bot.QRMutex.Unlock()

	if bot.CurrentQR == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "No QR code available or already logged in"})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"code": bot.CurrentQR})
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	w.Header().Set("Content-Type", "application/json")

	client := bot.GetClient()
	if client == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Bot not initialized"})
		return
	}

	err := bot.Logout()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func ConnectHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	w.Header().Set("Content-Type", "application/json")

	client := bot.GetClient()
	if client == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Bot not initialized"})
		return
	}

	err := client.Connect()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func DisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	w.Header().Set("Content-Type", "application/json")

	client := bot.GetClient()
	if client == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Bot not initialized"})
		return
	}

	client.Disconnect()
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// ─── API Key Middleware ──────────────────────────────────────

// RequireAPIKey middleware checks for a valid Bearer token.
// If no API keys are configured, all requests pass through (open mode).
func RequireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If no API keys exist, allow all (backward compatible open mode)
		if !db.HasAnyAPIKeys() {
			next(w, r)
			return
		}

		// If the request has a valid dashboard session, allow it
		if auth.IsAuthenticated(r) {
			next(w, r)
			return
		}

		// Check Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "API key required. Set Authorization: Bearer <your-api-key>",
			})
			return
		}

		// Extract Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Invalid authorization format. Use: Bearer <your-api-key>",
			})
			return
		}

		token := parts[1]
		if !db.ValidateAPIKey(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Invalid or inactive API key",
			})
			return
		}

		next(w, r)
	}
}

// ─── API Key Management Handlers ─────────────────────────────

func APIKeysListHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	keys, err := db.ListAPIKeys()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if keys == nil {
		keys = []db.APIKey{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "keys": keys})
}

func APIKeysCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Name is required"})
		return
	}

	rawKey, err := db.CreateAPIKey(req.Name)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"key":     rawKey,
		"message": "Save this key — it won't be shown again!",
	})
}

func APIKeysDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	w.Header().Set("Content-Type", "application/json")

	var req struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Try query param
		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "ID is required"})
			return
		}
		var parseErr error
		req.ID, parseErr = strconv.Atoi(idStr)
		if parseErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid ID format"})
			return
		}
	}

	if err := db.DeleteAPIKey(req.ID); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
