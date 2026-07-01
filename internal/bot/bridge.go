package bot

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
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

// HandleBridgeWebSocket is an HTTP handler for the WSS bridge.
// Mount this on the main HTTP mux at a path like "/ws/bridge".
func HandleBridgeWebSocket(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		OriginPatterns:     []string{"*"},
	})
	if err != nil {
		log.Printf("WebSocket accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "the sky is falling")

	log.Println("Bridge: New WebSocket client connected")
	ctx := context.Background()

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
			go handleSendMessage(p)

		case "SEND_DOCUMENT":
			var p SendDocumentPayload
			if err := json.Unmarshal(action.Payload, &p); err != nil {
				log.Printf("Invalid payload for SEND_DOCUMENT: %v", err)
				continue
			}
			go handleSendDocument(p)

		default:
			log.Printf("Unknown bridge action: %s", action.Action)
		}
	}
}

func handleSendMessage(p SendMessagePayload) {
	if GlobalClient == nil || !GlobalClient.IsConnected() {
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
	if GlobalClient == nil || !GlobalClient.IsConnected() {
		log.Println("Bridge Error: WhatsApp client not connected")
		return
	}

	log.Printf("Bridge: Sending document to %s from %s", p.Phone, p.DocumentURL)

	// Download file
	resp, err := http.Get(p.DocumentURL)
	if err != nil {
		log.Printf("Bridge Error: Failed to download document: %v", err)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Bridge Error: Failed to read document body: %v", err)
		return
	}

	// Save temporarily
	tmpFile := filepath.Join(os.TempDir(), p.Filename)
	err = os.WriteFile(tmpFile, data, 0644)
	if err != nil {
		log.Printf("Bridge Error: Failed to save temp file: %v", err)
		return
	}
	defer os.Remove(tmpFile)

	err = SendMediaMessage(p.Phone, tmpFile, p.Caption)
	if err != nil {
		log.Printf("Bridge Error: Failed to send media: %v", err)
	}
}
