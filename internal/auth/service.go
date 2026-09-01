package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/go-crm/services/pkg/config"
)

var (
	// ErrInvalidCredentials is returned for a bad email/password combination.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrEmailTaken is returned when registering (or linking) an already-used email.
	ErrEmailTaken = errors.New("email already registered")
	// ErrUnknownProvider is returned for an unconfigured SSO provider.
	ErrUnknownProvider = errors.New("unknown or unconfigured provider")
)

// Service holds the auth business logic.
type Service struct {
	store *store
	// pool is kept alongside the store because refresh-token issuance is shared
	// with sibling modules through the package-level IssueSession.
	pool      *pgxpool.Pool
	cfg       config.Config
	providers map[string]*oauthProvider
}

func newService(pool *pgxpool.Pool, cfg config.Config) *Service {
	return &Service{
		store:     &store{pool: pool},
		pool:      pool,
		cfg:       cfg,
		providers: buildProviders(cfg),
	}
}

// Register creates a password-backed user and starts a session.
func (s *Service) Register(ctx context.Context, email, password, name string) (Session, error) {
	switch _, err := s.store.userByEmail(ctx, email); {
	case err == nil:
		return Session{}, ErrEmailTaken
	case !errors.Is(err, ErrUserNotFound):
		return Session{}, err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return Session{}, err
	}

	var namePtr *string
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		namePtr = &trimmed
	}

	u, err := s.store.createUserWithOrg(ctx, newUser{
		Email:        email,
		Name:         namePtr,
		OrgName:      defaultOrgName(email),
		PasswordHash: &hash,
		AuthProvider: "password",
	})
	if err != nil {
		return Session{}, err
	}
	return IssueSession(ctx, s.pool, s.cfg, u)
}

// defaultOrgName names the personal workspace a new signup gets. Registration
// asks only for an email, so the local part is the best label available; the
// owner can rename it once organization settings exist.
func defaultOrgName(email string) string {
	local, _, found := strings.Cut(email, "@")
	if !found || local == "" {
		return "My workspace"
	}
	return local + "'s workspace"
}

// Login verifies an email/password pair and starts a session.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	u, err := s.store.userByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	// SSO-only accounts have no password set.
	if u.PasswordHash == nil {
		return Session{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(password, *u.PasswordHash)
	if err != nil || !ok {
		return Session{}, ErrInvalidCredentials
	}
	return IssueSession(ctx, s.pool, s.cfg, u)
}

// AuthCodeURL returns the provider authorization URL for the given CSRF state.
func (s *Service) AuthCodeURL(provider, state string) (string, error) {
	p, ok := s.providers[provider]
	if !ok {
		return "", ErrUnknownProvider
	}
	return p.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline), nil
}

// CompleteSSO exchanges an authorization code, resolves (or provisions) the
// user, and returns an access token.
func (s *Service) CompleteSSO(ctx context.Context, provider, code string) (Session, error) {
	p, ok := s.providers[provider]
	if !ok {
		return Session{}, ErrUnknownProvider
	}

	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return Session{}, fmt.Errorf("code exchange: %w", err)
	}
	id, err := p.identity(ctx, tok)
	if err != nil {
		return Session{}, err
	}
	if id.Email == "" {
		return Session{}, errors.New("provider returned no email")
	}
	id.Email = strings.TrimSpace(strings.ToLower(id.Email))

	// 1. Known SSO identity → log in.
	u, err := s.store.userByProvider(ctx, provider, id.ProviderUserID)
	if err == nil {
		return IssueSession(ctx, s.pool, s.cfg, u)
	}
	if !errors.Is(err, ErrUserNotFound) {
		return Session{}, err
	}

	// 2. Email already registered under the same provider → link provider ID if missing, or refuse auto-link across different methods.
	if existing, e := s.store.userByEmail(ctx, id.Email); e == nil {
		if existing.AuthProvider == provider {
			if existing.ProviderUserID == nil || *existing.ProviderUserID == "" {
				_ = s.store.updateUserProviderID(ctx, existing.ID, id.ProviderUserID)
			}
			return IssueSession(ctx, s.pool, s.cfg, existing)
		}
		return Session{}, ErrEmailTaken
	} else if !errors.Is(e, ErrUserNotFound) {
		return Session{}, e
	}

	// 3. First time → provision a new SSO user with their own workspace.
	var namePtr *string
	if id.Name != "" {
		namePtr = &id.Name
	}
	u, err = s.store.createUserWithOrg(ctx, newUser{
		Email:          id.Email,
		Name:           namePtr,
		OrgName:        defaultOrgName(id.Email),
		AuthProvider:   provider,
		ProviderUserID: &id.ProviderUserID,
	})
	if err != nil {
		return Session{}, err
	}
	return IssueSession(ctx, s.pool, s.cfg, u)
}
