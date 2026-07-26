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
	store     *store
	cfg       config.Config
	providers map[string]*oauthProvider
}

func newService(pool *pgxpool.Pool, cfg config.Config) *Service {
	return &Service{
		store:     &store{pool: pool},
		cfg:       cfg,
		providers: buildProviders(cfg),
	}
}

// Register creates a password-backed user and returns an access token.
func (s *Service) Register(ctx context.Context, email, password string) (string, User, error) {
	switch _, err := s.store.userByEmail(ctx, email); {
	case err == nil:
		return "", User{}, ErrEmailTaken
	case !errors.Is(err, ErrUserNotFound):
		return "", User{}, err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return "", User{}, err
	}
	u, err := s.store.createUserWithOrg(ctx, newUser{
		Email:        email,
		OrgName:      defaultOrgName(email),
		PasswordHash: &hash,
		AuthProvider: "password",
	})
	if err != nil {
		return "", User{}, err
	}
	tok, err := issueAccessToken(s.cfg, u)
	return tok, u, err
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

// Login verifies an email/password pair and returns an access token.
func (s *Service) Login(ctx context.Context, email, password string) (string, User, error) {
	u, err := s.store.userByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	// SSO-only accounts have no password set.
	if u.PasswordHash == nil {
		return "", User{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(password, *u.PasswordHash)
	if err != nil || !ok {
		return "", User{}, ErrInvalidCredentials
	}
	tok, err := issueAccessToken(s.cfg, u)
	return tok, u, err
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
func (s *Service) CompleteSSO(ctx context.Context, provider, code string) (string, error) {
	p, ok := s.providers[provider]
	if !ok {
		return "", ErrUnknownProvider
	}

	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("code exchange: %w", err)
	}
	id, err := p.identity(ctx, tok)
	if err != nil {
		return "", err
	}
	if id.Email == "" {
		return "", errors.New("provider returned no email")
	}

	// 1. Known SSO identity → log in.
	u, err := s.store.userByProvider(ctx, provider, id.ProviderUserID)
	if err == nil {
		return issueAccessToken(s.cfg, u)
	}
	if !errors.Is(err, ErrUserNotFound) {
		return "", err
	}

	// 2. Email already registered under a different method → refuse to
	//    auto-link (would let SSO take over a password account).
	if existing, e := s.store.userByEmail(ctx, id.Email); e == nil {
		if existing.AuthProvider == provider {
			return issueAccessToken(s.cfg, existing)
		}
		return "", ErrEmailTaken
	} else if !errors.Is(e, ErrUserNotFound) {
		return "", e
	}

	// 3. First time → provision a new SSO user with their own workspace.
	u, err = s.store.createUserWithOrg(ctx, newUser{
		Email:          id.Email,
		OrgName:        defaultOrgName(id.Email),
		AuthProvider:   provider,
		ProviderUserID: &id.ProviderUserID,
	})
	if err != nil {
		return "", err
	}
	return issueAccessToken(s.cfg, u)
}
