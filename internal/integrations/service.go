package integrations

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/go-crm/services/internal/integrations/google"
	"github.com/go-crm/services/pkg/config"
)

// Service owns the connected accounts and the actions performed through them.
type Service struct {
	store    *store
	cfg      config.Config
	calendar *google.Client
}

func NewService(pool *pgxpool.Pool, cfg config.Config) *Service {
	return &Service{store: &store{pool: pool}, cfg: cfg, calendar: google.NewClient()}
}

func (s *Service) List(ctx context.Context, userID string) ([]Connection, error) {
	return s.store.list(ctx, userID)
}

func (s *Service) Disconnect(ctx context.Context, userID, provider string) error {
	return s.store.delete(ctx, userID, provider)
}

// CompleteGoogle exchanges the consent code and stores the resulting tokens.
func (s *Service) CompleteGoogle(ctx context.Context, userID, code string) error {
	oc, err := googleConfig(s.cfg)
	if err != nil {
		return err
	}
	tok, err := oc.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("code exchange: %w", err)
	}
	if tok.RefreshToken == "" {
		// Without one the connection dies in an hour with no way to renew. Better
		// to refuse now than to look connected and fail later.
		return errors.New("google returned no refresh token")
	}

	accountID, _ := googleAccount(ctx, oc.Client(ctx, tok))
	return s.store.save(ctx, userID, "google", tok, accountID)
}

// Meeting is what a booked call becomes.
type Meeting struct {
	GoogleEventID string    `json:"googleEventId"`
	MeetLink      string    `json:"meetLink"`
	Title         string    `json:"title"`
	StartAt       time.Time `json:"startAt"`
	EndAt         time.Time `json:"endAt"`
}

// BookCall creates a Google Calendar event with a Meet link on the caller's own
// calendar and records it against the lead or deal.
//
// Attendees are deliberately not set: the meeting lands on the organiser's
// calendar and the link is theirs to share, so booking a call never sends
// anything to the customer by itself.
func (s *Service) BookCall(
	ctx context.Context, userID, title string, startAt time.Time, duration time.Duration,
	leadID, dealID string,
) (Meeting, error) {
	tok, err := s.store.token(ctx, userID, "google")
	if err != nil {
		return Meeting{}, err
	}
	oc, err := googleConfig(s.cfg)
	if err != nil {
		return Meeting{}, err
	}

	// TokenSource refreshes an expired access token behind the scenes; comparing
	// afterwards tells us whether the stored copy needs updating.
	src := oc.TokenSource(ctx, tok)
	fresh, err := src.Token()
	if err != nil {
		return Meeting{}, fmt.Errorf("refresh google token: %w", err)
	}
	if fresh.AccessToken != tok.AccessToken {
		if err := s.store.refreshed(ctx, userID, "google", fresh); err != nil {
			// Not fatal: the call can still be booked with the token in hand.
			log.Printf("integrations: could not persist refreshed google token: %v", err)
		}
	}

	endAt := startAt.Add(duration)
	res, err := s.calendar.CreateEvent(ctx, oauth2.NewClient(ctx, src), google.CalendarEventInput{
		Summary:  title,
		StartAt:  startAt,
		EndAt:    endAt,
		TimeZone: s.cfg.Timezone,
	})
	if err != nil {
		return Meeting{}, err
	}

	m := Meeting{
		GoogleEventID: res.ID,
		MeetLink:      res.MeetLink(),
		Title:         title,
		StartAt:       startAt,
		EndAt:         endAt,
	}
	if err := s.store.saveCalendarEvent(ctx, m, leadID, dealID); err != nil {
		// The meeting exists in Google; failing the request now would tell the
		// user it did not. Record the problem and return the meeting.
		log.Printf("integrations: could not record calendar event %s: %v", res.ID, err)
	}
	return m, nil
}
