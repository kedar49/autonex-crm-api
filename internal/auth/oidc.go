package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"

	"github.com/go-crm/services/pkg/config"
)

// ssoIdentity is the normalized identity extracted from a provider's userinfo.
type ssoIdentity struct {
	ProviderUserID string
	Email          string
}

// providerReg holds the static endpoints for a known provider. Client
// credentials are supplied separately from config.
type providerReg struct {
	authURL     string
	tokenURL    string
	userInfoURL string
	scopes      []string
}

var registry = map[string]providerReg{
	"google": {
		authURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		tokenURL:    "https://oauth2.googleapis.com/token",
		userInfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
		scopes:      []string{"openid", "email", "profile"},
	},
	"github": {
		authURL:     "https://github.com/login/oauth/authorize",
		tokenURL:    "https://github.com/login/oauth/access_token",
		userInfoURL: "https://api.github.com/user",
		scopes:      []string{"read:user", "user:email"},
	},
}

// oauthProvider is a fully-configured, ready-to-use provider.
type oauthProvider struct {
	name        string
	oauth       *oauth2.Config
	userInfoURL string
}

// buildProviders merges the static registry with configured credentials and
// returns only the providers that are actually enabled.
func buildProviders(cfg config.Config) map[string]*oauthProvider {
	out := make(map[string]*oauthProvider)
	for name, creds := range cfg.OAuthCreds {
		reg, ok := registry[name]
		if !ok || creds.ClientID == "" {
			continue
		}
		out[name] = &oauthProvider{
			name: name,
			oauth: &oauth2.Config{
				ClientID:     creds.ClientID,
				ClientSecret: creds.ClientSecret,
				Endpoint:     oauth2.Endpoint{AuthURL: reg.authURL, TokenURL: reg.tokenURL},
				RedirectURL:  cfg.OIDCRedirectBase + "/" + name + "/callback",
				Scopes:       reg.scopes,
			},
			userInfoURL: reg.userInfoURL,
		}
	}
	return out
}

// identity exchanges the OAuth token for the user's normalized identity.
func (p *oauthProvider) identity(ctx context.Context, tok *oauth2.Token) (ssoIdentity, error) {
	client := p.oauth.Client(ctx, tok)

	body, err := getJSON(ctx, client, p.userInfoURL)
	if err != nil {
		return ssoIdentity{}, err
	}

	switch p.name {
	case "google":
		var u struct {
			Sub   string `json:"sub"`
			Email string `json:"email"`
		}
		if err := json.Unmarshal(body, &u); err != nil {
			return ssoIdentity{}, err
		}
		return ssoIdentity{ProviderUserID: u.Sub, Email: u.Email}, nil

	case "github":
		var u struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
		}
		if err := json.Unmarshal(body, &u); err != nil {
			return ssoIdentity{}, err
		}
		email := u.Email
		if email == "" {
			// GitHub omits the email here when it is private; fetch it explicitly.
			if email, err = githubPrimaryEmail(ctx, client); err != nil {
				return ssoIdentity{}, err
			}
		}
		return ssoIdentity{ProviderUserID: fmt.Sprintf("%d", u.ID), Email: email}, nil

	default:
		return ssoIdentity{}, fmt.Errorf("unsupported provider %q", p.name)
	}
}

func githubPrimaryEmail(ctx context.Context, client *http.Client) (string, error) {
	body, err := getJSON(ctx, client, "https://api.github.com/user/emails")
	if err != nil {
		return "", err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("no verified primary email on github account")
}

func getJSON(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo request to %s: status %d", url, resp.StatusCode)
	}
	return body, nil
}
