package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"whatsbridge/internal/db"
	"strings"
	"sync"
	"time"

	"github.com/h2non/filetype"
	_ "github.com/lib/pq"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var GlobalClient *whatsmeow.Client
var CurrentQR string
var QRMutex sync.Mutex
var container *sqlstore.Container

func EventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		fmt.Printf("Received a message from %s: %s\n", v.Info.Sender.User, v.Message.GetConversation())
	}
}

func InitWhatsApp() {
	waPostgresDSN := os.Getenv("WA_POSTGRES_DSN")
	if waPostgresDSN == "" {
		log.Fatal("WA_POSTGRES_DSN environment variable is required")
	}

	dbLog := waLog.Stdout("Database", "WARN", true)
	var err error

	// Retry database initialization until it succeeds
	for {
		container, err = sqlstore.New(context.Background(), "postgres", waPostgresDSN, dbLog)
		if err == nil {
			break
		}
		log.Printf("Failed to open WhatsApp store DB, retrying in 5s: %v", err)
		time.Sleep(5 * time.Second)
	}

	log.Println("WhatsApp session store (PostgreSQL) initialized successfully.")
	StartClient()
}

func StartClient() {
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		log.Printf("Failed to get device store: %v", err)
		return
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)
	client.AddEventHandler(EventHandler)

	if client.Store.ID == nil {
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			log.Printf("WhatsApp connect failed (no internet?), will retry in background: %v", err)
			GlobalClient = client
			go retryConnect(client)
			return
		}
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					QRMutex.Lock()
					CurrentQR = evt.Code
					QRMutex.Unlock()
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
					fmt.Println("\nPlease scan the QR code on the web dashboard or here to log in.")
				} else {
					fmt.Println("Login event:", evt.Event)
					if evt.Event == "success" {
						QRMutex.Lock()
						CurrentQR = ""
						QRMutex.Unlock()
					}
				}
			}
		}()
	} else {
		err = client.Connect()
		if err != nil {
			log.Printf("WhatsApp connect failed (no internet?), will retry in background: %v", err)
			GlobalClient = client
			go retryConnect(client)
			return
		}
		fmt.Println("Successfully connected to WhatsApp!")
	}

	GlobalClient = client
}

// retryConnect keeps trying to connect to WhatsApp servers until successful.
// This handles the case where the app starts before internet is available.
func retryConnect(client *whatsmeow.Client) {
	for {
		time.Sleep(15 * time.Second)

		if client.IsConnected() {
			log.Println("WhatsApp client is now connected.")
			return
		}

		log.Println("Retrying WhatsApp connection...")
		err := client.Connect()
		if err != nil {
			log.Printf("Retry failed: %v", err)
			continue
		}

		log.Println("WhatsApp connection established on retry!")
		return
	}
}

func RestartBot() {
	if GlobalClient != nil {
		GlobalClient.Disconnect()
	}
	StartClient()
}

func Logout() error {
	if GlobalClient == nil {
		return fmt.Errorf("client not initialized")
	}

	err := GlobalClient.Logout(context.Background())
	if err != nil {
		return err
	}

	QRMutex.Lock()
	CurrentQR = ""
	QRMutex.Unlock()

	RestartBot()
	return nil
}

func SendTextMessage(to string, message string) error {
	if !IsInternetAvailable() {
		return fmt.Errorf("no internet connection")
	}
	if GlobalClient == nil {
		return fmt.Errorf("client not connected")
	}

	phone := strings.ReplaceAll(strings.ReplaceAll(to, "+", ""), " ", "")
	jid := types.NewJID(phone, types.DefaultUserServer)
	_, err := GlobalClient.SendMessage(context.Background(), jid, &waProto.Message{
		Conversation: proto.String(message),
	})
	db.LogMessageUsage(err == nil)
	return err
}

func SendMediaMessage(to string, filePath string, caption string) error {
	if !IsInternetAvailable() {
		return fmt.Errorf("no internet connection")
	}
	if GlobalClient == nil {
		return fmt.Errorf("client not connected")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	kind, _ := filetype.Match(data)
	var mediaType whatsmeow.MediaType
	if kind.MIME.Type == "image" {
		mediaType = whatsmeow.MediaImage
	} else if kind.MIME.Type == "video" {
		mediaType = whatsmeow.MediaVideo
	} else if kind.MIME.Type == "audio" {
		mediaType = whatsmeow.MediaAudio
	} else {
		mediaType = whatsmeow.MediaDocument
	}

	uploadResp, err := GlobalClient.Upload(context.Background(), data, mediaType)
	if err != nil {
		return fmt.Errorf("failed to upload media: %v", err)
	}

	var msg *waProto.Message
	switch mediaType {
	case whatsmeow.MediaImage:
		msg = &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(kind.MIME.Value),
				URL:           proto.String(uploadResp.URL),
				DirectPath:    proto.String(uploadResp.DirectPath),
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
	case whatsmeow.MediaVideo:
		msg = &waProto.Message{
			VideoMessage: &waProto.VideoMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(kind.MIME.Value),
				URL:           proto.String(uploadResp.URL),
				DirectPath:    proto.String(uploadResp.DirectPath),
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
	case whatsmeow.MediaDocument:
		msg = &waProto.Message{
			DocumentMessage: &waProto.DocumentMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(kind.MIME.Value),
				FileName:      proto.String(filepath.Base(filePath)),
				URL:           proto.String(uploadResp.URL),
				DirectPath:    proto.String(uploadResp.DirectPath),
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
	case whatsmeow.MediaAudio:
		msg = &waProto.Message{
			AudioMessage: &waProto.AudioMessage{
				Mimetype:      proto.String(kind.MIME.Value),
				URL:           proto.String(uploadResp.URL),
				DirectPath:    proto.String(uploadResp.DirectPath),
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
	}

	phone := strings.ReplaceAll(strings.ReplaceAll(to, "+", ""), " ", "")
	jid := types.NewJID(phone, types.DefaultUserServer)
	_, err = GlobalClient.SendMessage(context.Background(), jid, msg)
	db.LogMessageUsage(err == nil)
	return err
}
