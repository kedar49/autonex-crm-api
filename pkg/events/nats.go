package events

import (
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

// Connect opens a NATS connection. Use JetStream() on the returned conn for durable streams.
func Connect(url string) (*nats.Conn, error) {
	return nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	)
}

// JetStream returns a JetStream context for durable publish/subscribe.
func JetStream(nc *nats.Conn) (nats.JetStreamContext, error) {
	return nc.JetStream()
}

type Event struct {
	Subject   string                 `json:"subject"`
	OrgID     string                 `json:"org_id"`
	ActorID   string                 `json:"actor_id,omitempty"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
}

func Publish(js nats.JetStreamContext, subject string, orgID, actorID string, payload map[string]interface{}) error {
	evt := Event{
		Subject:   subject,
		OrgID:     orgID,
		ActorID:   actorID,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}

	_, err = js.Publish(subject, data)
	return err
}
