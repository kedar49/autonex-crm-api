package events

import "github.com/nats-io/nats.go"

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
