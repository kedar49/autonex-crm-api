package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/go-crm/services/internal/integrations/google"
	"github.com/go-crm/services/internal/integrations/slack"
	"github.com/go-crm/services/pkg/config"
	"github.com/go-crm/services/pkg/events"
	"github.com/nats-io/nats.go"
)

func main() {
	cfg := config.Load()

	nc, err := events.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatalf("nats connection failed: %v", err)
	}
	defer nc.Close()

	js, err := events.JetStream(nc)
	if err != nil {
		log.Printf("jetstream init warning: %v (falling back to standard nats pub/sub)", err)
	}

	log.Printf("worker daemon running, connected to %s", cfg.NATSURL)

	slackClient := slack.NewClient()
	googleClient := google.NewClient()

	// Subscribe to Deal Won events
	subscribe(nc, js, "crm.deals.won", func(evt events.Event) {
		log.Printf("[WORKER] Deal Won event received for Org %s", evt.OrgID)
		if webhook, ok := evt.Payload["slack_webhook_url"].(string); ok && webhook != "" {
			dealTitle, _ := evt.Payload["deal_title"].(string)
			amount, _ := evt.Payload["amount"].(float64)
			_ = slackClient.SendWebhook(context.Background(), webhook, slack.Notification{
				Text: "🎉 **Deal Won!** " + dealTitle + " (Value: $" + formatFloat(amount) + ")",
			})
		}
	})

	// Subscribe to Quote Approval events
	subscribe(nc, js, "crm.quotes.approval_requested", func(evt events.Event) {
		log.Printf("[WORKER] Quote Approval Requested for Org %s", evt.OrgID)
		if webhook, ok := evt.Payload["slack_webhook_url"].(string); ok && webhook != "" {
			quoteNum, _ := evt.Payload["quote_number"].(string)
			_ = slackClient.SendWebhook(context.Background(), webhook, slack.Notification{
				Text: "⚠️ **Quote Approval Required** for Quote #" + quoteNum,
			})
		}
	})

	// Subscribe to Calendar Follow-up Sync
	subscribe(nc, js, "crm.followups.scheduled", func(evt events.Event) {
		log.Printf("[WORKER] Calendar Followup Sync for Org %s", evt.OrgID)
		if token, ok := evt.Payload["google_access_token"].(string); ok && token != "" {
			title, _ := evt.Payload["title"].(string)
			_, _ = googleClient.CreateEvent(context.Background(), token, google.CalendarEventInput{
				Summary: title,
			})
		}
	})

	select {} // block forever
}

func subscribe(nc *nats.Conn, js nats.JetStreamContext, subject string, handler func(evt events.Event)) {
	if js != nil {
		_, err := js.Subscribe(subject, func(m *nats.Msg) {
			var evt events.Event
			if err := json.Unmarshal(m.Data, &evt); err == nil {
				handler(evt)
			}
			_ = m.Ack()
		})
		if err == nil {
			return
		}
	}

	// Fallback to core NATS sub
	_, _ = nc.Subscribe(subject, func(m *nats.Msg) {
		var evt events.Event
		if err := json.Unmarshal(m.Data, &evt); err == nil {
			handler(evt)
		}
	})
}

func formatFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
