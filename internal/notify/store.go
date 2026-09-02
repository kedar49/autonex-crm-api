package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationItem struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	UserID    string    `json:"userId"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	ActionURL string    `json:"actionUrl,omitempty"`
	Priority  string    `json:"priority"`
	IsRead    bool      `json:"isRead"`
	CreatedAt time.Time `json:"createdAt"`
}

type PushSubscription struct {
	ID        string    `json:"id,omitempty"`
	UserID    string    `json:"userId,omitempty"`
	OrgID     string    `json:"orgId,omitempty"`
	Endpoint  string    `json:"endpoint"`
	P256dh    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	UserAgent string    `json:"userAgent,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type NotificationsResponse struct {
	Items       []NotificationItem `json:"items"`
	UnreadCount int                `json:"unreadCount"`
}

// SSEHub manages real-time Server-Sent Events subscriber channels
type SSEHub struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan NotificationItem]bool // key: userID
}

func NewSSEHub() *SSEHub {
	return &SSEHub{
		subscribers: make(map[string]map[chan NotificationItem]bool),
	}
}

func (h *SSEHub) Subscribe(userID string) chan NotificationItem {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan NotificationItem, 10)
	if _, ok := h.subscribers[userID]; !ok {
		h.subscribers[userID] = make(map[chan NotificationItem]bool)
	}
	h.subscribers[userID][ch] = true
	return ch
}

func (h *SSEHub) Unsubscribe(userID string, ch chan NotificationItem) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if userSubs, ok := h.subscribers[userID]; ok {
		delete(userSubs, ch)
		close(ch)
		if len(userSubs) == 0 {
			delete(h.subscribers, userID)
		}
	}
}

func (h *SSEHub) Broadcast(item NotificationItem) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if userSubs, ok := h.subscribers[item.UserID]; ok {
		for ch := range userSubs {
			select {
			case ch <- item:
			default:
				// Non-blocking fallback if buffer is full
			}
		}
	}
}

// Store handles notification data access
type Store struct {
	pool *pgxpool.Pool
	hub  *SSEHub
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool: pool,
		hub:  NewSSEHub(),
	}
}

func (s *Store) CreateNotification(ctx context.Context, item NotificationItem) (NotificationItem, error) {
	if item.Priority == "" {
		item.Priority = "info"
	}
	query := `
		INSERT INTO notifications (org_id, user_id, type, title, body, action_url, priority)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, is_read
	`
	err := s.pool.QueryRow(ctx, query, item.OrgID, item.UserID, item.Type, item.Title, item.Body, item.ActionURL, item.Priority).
		Scan(&item.ID, &item.CreatedAt, &item.IsRead)
	if err != nil {
		return item, fmt.Errorf("create notification: %w", err)
	}

	// Broadcast instantly to active SSE listeners
	s.hub.Broadcast(item)

	return item, nil
}

func (s *Store) ListNotifications(ctx context.Context, orgID, userID string, limit, offset int) (NotificationsResponse, error) {
	if limit <= 0 {
		limit = 20
	}

	var resp NotificationsResponse
	resp.Items = make([]NotificationItem, 0)

	// Count unread
	countQuery := `SELECT COUNT(*) FROM notifications WHERE org_id = $1 AND user_id = $2 AND is_read = false`
	err := s.pool.QueryRow(ctx, countQuery, orgID, userID).Scan(&resp.UnreadCount)
	if err != nil {
		return resp, fmt.Errorf("count unread: %w", err)
	}

	// Fetch items
	itemsQuery := `
		SELECT id, org_id, user_id, type, title, body, COALESCE(action_url, ''), priority, is_read, created_at
		FROM notifications
		WHERE org_id = $1 AND user_id = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`
	rows, err := s.pool.Query(ctx, itemsQuery, orgID, userID, limit, offset)
	if err != nil {
		return resp, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var n NotificationItem
		if err := rows.Scan(&n.ID, &n.OrgID, &n.UserID, &n.Type, &n.Title, &n.Body, &n.ActionURL, &n.Priority, &n.IsRead, &n.CreatedAt); err != nil {
			return resp, err
		}
		resp.Items = append(resp.Items, n)
	}

	return resp, rows.Err()
}

func (s *Store) MarkAsRead(ctx context.Context, orgID, userID, notificationID string) error {
	query := `UPDATE notifications SET is_read = true WHERE id = $1 AND org_id = $2 AND user_id = $3`
	_, err := s.pool.Exec(ctx, query, notificationID, orgID, userID)
	return err
}

func (s *Store) MarkAllAsRead(ctx context.Context, orgID, userID string) error {
	query := `UPDATE notifications SET is_read = true WHERE org_id = $1 AND user_id = $2`
	_, err := s.pool.Exec(ctx, query, orgID, userID)
	return err
}

func (s *Store) SavePushSubscription(ctx context.Context, sub PushSubscription) error {
	query := `
		INSERT INTO push_subscriptions (user_id, org_id, endpoint, p256dh, auth, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (endpoint) DO UPDATE SET
			p256dh = EXCLUDED.p256dh,
			auth = EXCLUDED.auth,
			user_agent = EXCLUDED.user_agent,
			updated_at = NOW()
	`
	_, err := s.pool.Exec(ctx, query, sub.UserID, sub.OrgID, sub.Endpoint, sub.P256dh, sub.Auth, sub.UserAgent)
	return err
}

func (s *Store) DeletePushSubscription(ctx context.Context, userID, endpoint string) error {
	query := `DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`
	_, err := s.pool.Exec(ctx, query, userID, endpoint)
	return err
}

// ServeSSE handles HTTP Server-Sent Events stream
func (s *Store) ServeSSE(w http.ResponseWriter, r *http.Request, userID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := s.hub.Subscribe(userID)
	defer s.hub.Unsubscribe(userID, ch)

	// Send connection open heartbeat
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case item, ok := <-ch:
			if !ok {
				return
			}
			bytes, err := json.Marshal(item)
			if err != nil {
				log.Printf("sse marshal err: %v", err)
				continue
			}
			fmt.Fprintf(w, "event: notification\ndata: %s\n\n", string(bytes))
			flusher.Flush()
		}
	}
}
