package bot

import (
	"context"
	"fmt"
	"log"
	"whatsbridge/internal/db"
	"strings"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func StartSchedulerLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in scheduler loop: %v", r)
		}
	}()
	for {
		time.Sleep(10 * time.Second)

		c := GetClient()
		if c == nil || !c.IsConnected() || !c.IsLoggedIn() {
			continue
		}

		// Check that the database is available before querying
		if db.GetDB() == nil {
			continue
		}

		now := time.Now().UTC().Format(time.RFC3339)
		msgsToSend, err := db.GetPendingMessages(now)
		if err != nil {
			fmt.Printf("Scheduler error: %v\n", err)
			continue
		}

		for _, pending := range msgsToSend {
			db.UpdateScheduledMessageStatus(pending.ID, "processing")

			phone := strings.TrimPrefix(pending.Recipient, "+")
			jid := types.NewJID(phone, types.DefaultUserServer)
			msg := &waProto.Message{Conversation: proto.String(pending.Message)}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := c.SendMessage(ctx, jid, msg)
			cancel()

			if err != nil {
				db.UpdateScheduledMessageStatus(pending.ID, "failed")
				db.LogMessageUsage(false)
			} else {
				db.UpdateScheduledMessageStatus(pending.ID, "sent")
				db.LogMessageUsage(true)
			}
		}
	}
}
