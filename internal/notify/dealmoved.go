// Package notify turns domain events into email to the people who need to know.
//
// It sits beside the modules rather than inside them: a deal does not know what
// an SMTP relay is, and the recipient list is an organization question rather
// than a pipeline one.
package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Autonex009/autonex-crm-api/pkg/mailer"
)

// sendTimeout bounds the background delivery. The HTTP request that triggered it
// has already been answered, so nothing is waiting on this.
const sendTimeout = 30 * time.Second

// DealMove is what the board knows at the moment a card is dropped.
type DealMove struct {
	DealID     string
	Title      string
	FromStage  string
	ToStage    string
	AccountID  string
	Amount     float64
	ActorName  string
	StageLabel func(string) string
}

// Notifier holds the collaborators the notifications need. A nil *Notifier is
// usable and does nothing, so a caller never has to guard the call site.
type Notifier struct {
	pool      *pgxpool.Pool
	store     *Store
	mail      mailer.Sender
	webAppURL string
	pusher    *Pusher
}

func New(pool *pgxpool.Pool, mail mailer.Sender, webAppURL, expoAccessToken string) *Notifier {
	store := NewStore(pool)
	return &Notifier{
		pool:      pool,
		store:     store,
		mail:      mail,
		webAppURL: webAppURL,
		pusher:    NewPusher(store, expoAccessToken),
	}
}

// pushToDevices sends one recorded notification on to the user's phones.
//
// The badge is read back rather than counted in memory because the user may have
// cleared notifications on another device between the insert and now, and a
// badge that disagrees with the list is worse than no badge.
//
// Errors are swallowed inside Pusher.Push: the notification is already durable
// and the app can list it, so a failure to nudge must not fail the deal move
// that caused it.
func (n *Notifier) pushToDevices(ctx context.Context, item NotificationItem) {
	badge, err := n.store.UnreadCount(ctx, item.OrgID, item.UserID)
	if err != nil {
		log.Printf("notify: could not read unread count for %s: %v", item.UserID, err)
		badge = 0
	}
	n.pusher.Push(ctx, item, badge)
}

func (n *Notifier) Store() *Store {
	if n == nil {
		return nil
	}
	return n.store
}

// DealMoved emails the mover's colleagues that a card changed column.
//
// Delivery happens on a detached goroutine: a drag-and-drop should feel
// instant, and an SMTP round trip is far slower than the database write it
// follows. The trade is that a failure is logged rather than surfaced — the
// right balance for a notification, which is not the point of the request.
func (n *Notifier) DealMoved(ctx context.Context, orgID, actorID string, mv DealMove) {
	// The mailer being unconfigured used to short-circuit this whole method,
	// which meant the in-app bell stayed empty on a deployment with no SMTP.
	// The two deliveries are now independent: the notification row is written
	// either way, and only the email needs a relay.
	if n == nil || n.pool == nil {
		return
	}
	if mv.FromStage == mv.ToStage {
		return // a reorder inside a column is not news
	}

	// The request context is cancelled the moment the handler returns, so the
	// values are kept (for tracing) while the cancellation is dropped.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)

	go func() {
		defer cancel()

		company, currency, actor := n.details(sendCtx, orgID, mv.AccountID, actorID)
		n.recordDealMoved(sendCtx, orgID, actorID, mv, company)

		if n.mail == nil {
			return
		}
		to, err := n.orgRecipients(sendCtx, orgID)
		if err != nil {
			log.Printf("notify: could not resolve recipients for deal %s: %v", mv.DealID, err)
			return
		}
		if len(to) == 0 {
			return
		}
		if err := n.mail.Send(sendCtx, mailer.Message{
			To:      to,
			Subject: dealMovedSubject(mv),
			Body:    n.dealMovedBody(mv, company, currency, actor),
		}); err != nil {
			log.Printf("notify: deal %s move email failed: %v", mv.DealID, err)
		}
	}()
}

// recordDealMoved writes the in-app notification every member of the org sees
// in their bell, skipping the person who moved the card: they were there.
func (n *Notifier) recordDealMoved(ctx context.Context, orgID, actorID string, mv DealMove, company string) {
	if n.store == nil {
		return
	}

	members, err := n.orgMemberIDs(ctx, orgID)
	if err != nil {
		log.Printf("notify: could not resolve members for deal %s: %v", mv.DealID, err)
		return
	}

	stage := mv.ToStage
	if mv.StageLabel != nil {
		stage = mv.StageLabel(mv.ToStage)
	}

	body := fmt.Sprintf("%s moved to %s", strings.TrimSpace(mv.Title), stage)
	if company != "" {
		body = fmt.Sprintf("%s · %s moved to %s", company, strings.TrimSpace(mv.Title), stage)
	}

	// A won deal is worth interrupting someone for; the rest are FYI.
	priority := "info"
	if mv.ToStage == "won" {
		priority = "success"
	}

	for _, userID := range members {
		if userID == actorID {
			continue
		}
		item, err := n.store.CreateNotification(ctx, NotificationItem{
			OrgID:     orgID,
			UserID:    userID,
			Type:      "deal_moved",
			Title:     "Deal moved",
			Body:      body,
			ActionURL: "/deals",
			Priority:  priority,
		})
		if err != nil {
			log.Printf("notify: could not record deal %s notification: %v", mv.DealID, err)
			return
		}
		n.pushToDevices(ctx, item)
	}
}

// orgMemberIDs lists the profile ids of everyone in the organization — the
// audience for an org-wide notification.
func (n *Notifier) orgMemberIDs(ctx context.Context, orgID string) ([]string, error) {
	rows, err := n.pool.Query(ctx,
		`SELECT u.id::text
		   FROM users u
		   JOIN profiles p ON p.id = u.id
		  WHERE u.org_id = $1::uuid`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, 8)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// orgRecipients lists everyone in the organization, the person who moved the
// card included.
//
// The join onto profiles is what "every profile in the organization" means here:
// profiles carry no email of their own, so an address is reachable only through
// the login account it belongs to. A profile with no account cannot be emailed,
// and is skipped rather than silently counted.
func (n *Notifier) orgRecipients(ctx context.Context, orgID string) ([]string, error) {
	rows, err := n.pool.Query(ctx,
		`SELECT u.email
		   FROM users u
		   JOIN profiles p ON p.id = u.id
		  WHERE u.org_id = $1
		    AND u.email <> ''
		  ORDER BY u.email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		out = append(out, email)
	}
	return out, rows.Err()
}

func label(mv DealMove, stage string) string {
	if mv.StageLabel != nil {
		return mv.StageLabel(stage)
	}
	return stage
}

func dealMovedSubject(mv DealMove) string {
	return fmt.Sprintf("%s moved to %s", mv.Title, label(mv, mv.ToStage))
}

// details resolves the labels the email shows but the board response does not
// carry. A failure here is not fatal: a notification missing a company name is
// worth far more than one that never arrives.
//
// The actor is resolved here too. Now that the mail goes to the whole
// organization — the mover included — "Someone moved a deal" is not good enough:
// the first thing a reader needs is who did it.
func (n *Notifier) details(ctx context.Context, orgID, accountID, actorID string) (company, currency, actor string) {
	currency = "USD"
	err := n.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT name FROM accounts WHERE id = $2::uuid), ''),
		        COALESCE((SELECT currency FROM organizations WHERE id = $1), 'USD'),
		        COALESCE((SELECT COALESCE(NULLIF(btrim(u.name), ''), u.email)
		                    FROM users u WHERE u.id = $3::uuid), '')`,
		orgID, nilIfEmpty(accountID), nilIfEmpty(actorID)).Scan(&company, &currency, &actor)
	if err != nil {
		log.Printf("notify: could not resolve deal labels: %v", err)
	}
	return company, currency, actor
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (n *Notifier) dealMovedBody(mv DealMove, company, currency, actor string) string {
	var b strings.Builder

	// The resolved name wins; DealMove.ActorName is the caller's override, and
	// "Someone" is the last resort rather than the usual case.
	who := actor
	if who == "" {
		who = mv.ActorName
	}
	if who == "" {
		who = "Someone"
	}
	fmt.Fprintf(&b, "%s moved a deal on the board.\n\n", who)
	fmt.Fprintf(&b, "  Deal:    %s\n", mv.Title)
	if company != "" {
		fmt.Fprintf(&b, "  Company: %s\n", company)
	}
	if mv.Amount > 0 {
		fmt.Fprintf(&b, "  Value:   %s %.2f\n", currency, mv.Amount)
	}
	fmt.Fprintf(&b, "  Stage:   %s → %s\n", label(mv, mv.FromStage), label(mv, mv.ToStage))

	if n.webAppURL != "" {
		fmt.Fprintf(&b, "\nOpen the board: %s/app/deals\n", strings.TrimRight(n.webAppURL, "/"))
	}
	return b.String()
}
