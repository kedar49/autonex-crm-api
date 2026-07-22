package main

import (
	"log"

	"github.com/go-crm/services/pkg/config"
	"github.com/go-crm/services/pkg/events"
)

// Worker consumes NATS JetStream subjects for async domain events.
func main() {
	cfg := config.Load()

	nc, err := events.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer nc.Close()

	log.Printf("worker connected to %s", cfg.NATSURL)

	// TODO: subscribe to durable JetStream consumers, e.g.
	//   events.Subscribe(nc, "crm.deals.won", dealsHandler)

	select {} // block forever
}
