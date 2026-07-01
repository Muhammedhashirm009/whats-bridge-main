package bot

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"whatsbridge/internal/db"
)

type BridgeAction struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

type SendMessagePayload struct {
	Phone   string `json:"phone"`
	Message string `json:"message"`
}

type SendDocumentPayload struct {
	Phone       string `json:"phone"`
	DocumentURL string `json:"document_url"`
	Filename    string `json:"filename"`
	Caption     string `json:"caption"`
}

// isURLSafe validates that a URL does not point to private/internal networks (SSRF protection).
func isURLSafe(urlStr string) bool {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	// Only allow http and https schemes
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return false
	}

	// Block localhost variants
	if strings.EqualFold(hostname, "localhost") {
		return false
	}

	ip := net.ParseIP(hostname)
	if ip == nil {
		// Hostname — try resolving
		ips, err := net.LookupIP(hostname)
		if err != nil || len(ips) == 0 {
			return false
		}
		ip = ips[0]
	}

	// Block private and reserved IP ranges
	privateRanges := []struct {
		network string
	}{
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"127.0.0.0/8"},
		{"169.254.0.0/16"},
		{"::1/128"},
		{"fc00::/7"},
		{"fe80::/10"},
	}

	for _, r := range privateRanges {
		_, cidr, err := net.ParseCIDR(r.network)
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return false
		}
	}

	return true
}

// HandleBridgeWebSocket is an HTTP handler for the WSS bridge.
// Mount this on the main HTTP mux at a path like "/ws/bridge".
func HandleBridgeWebSocket(w http.ResponseWriter, r *http.Request) {
	// Authenticate via API key before accepting WebSocket
	key := ""

	// Check Authorization: Bearer <key> header first
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			key = parts[1]
		}
	}

	// If not present, check ?token=<key> query parameter
	if key == "" {
		key = r.URL.Query().Get("token")
	}

	// Validate the API key
	if key == "" || !db.ValidateAPIKey(key) {
		http.Error(w, `{"error":"Unauthorized — valid API key required"}`, http.StatusUnauthorized)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// TODO: Restrict OriginPatterns to specific domains in production
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("WebSocket accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "the sky is falling")

	log.Println("Bridge: New WebSocket client connected")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		var action BridgeAction
		err = wsjson.Read(ctx, c, &action)
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		log.Printf("Bridge received action: %s", action.Action)

		switch action.Action {
		case "SEND_MESSAGE":
			var p SendMessagePayload
			if err := json.Unmarshal(action.Payload, &p); err != nil {
				log.Printf("Invalid payload for SEND_MESSAGE: %v", err)
				continue
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("Bridge: recovered panic in SEND_MESSAGE: %v", r)
					}
				}()
				handleSendMessage(p)
			}()

		case "SEND_DOCUMENT":
			var p SendDocumentPayload
			if err := json.Unmarshal(action.Payload, &p); err != nil {
				log.Printf("Invalid payload for SEND_DOCUMENT: %v", err)
				continue
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("Bridge: recovered panic in SEND_DOCUMENT: %v", r)
					}
				}()
				handleSendDocument(p)
			}()

		default:
			log.Printf("Unknown bridge action: %s", action.Action)
		}
	}
}

func handleSendMessage(p SendMessagePayload) {
	client := GetClient()
	if client == nil || !client.IsConnected() {
		log.Println("Bridge Error: WhatsApp client not connected")
		return
	}

	log.Printf("Bridge: Sending message to %s", p.Phone)
	err := SendTextMessage(p.Phone, p.Message)
	if err != nil {
		log.Printf("Bridge Error: Failed to send message: %v", err)
	}
}

func handleSendDocument(p SendDocumentPayload) {
	client := GetClient()
	if client == nil || !client.IsConnected() {
		log.Println("Bridge Error: WhatsApp client not connected")
		return
	}

	// SSRF protection: validate the document URL
	if !isURLSafe(p.DocumentURL) {
		log.Printf("Bridge Error: Blocked unsafe URL: %s", p.DocumentURL)
		return
	}

	log.Printf("Bridge: Sending document to %s from %s", p.Phone, p.DocumentURL)

	// Download file with timeout
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Get(p.DocumentURL)
	if err != nil {
		log.Printf("Bridge Error: Failed to download document: %v", err)
		return
	}
	defer resp.Body.Close()

	// Limit download size to 100MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		log.Printf("Bridge Error: Failed to read document body: %v", err)
		return
	}

	// Save temporarily using secure temp file (prevents path traversal)
	tmpFile, err := os.CreateTemp(os.TempDir(), "wb-doc-*")
	if err != nil {
		log.Printf("Bridge Error: Failed to create temp file: %v", err)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		log.Printf("Bridge Error: Failed to write temp file: %v", err)
		return
	}
	tmpFile.Close()

	err = SendMediaMessage(p.Phone, tmpPath, p.Caption)
	if err != nil {
		log.Printf("Bridge Error: Failed to send media: %v", err)
	}
}
