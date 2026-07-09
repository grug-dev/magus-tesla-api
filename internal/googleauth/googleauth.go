// Package googleauth is a thin anti-corruption adapter over Google's OAuth 2.0
// login. It builds the consent URL, exchanges the authorization code, and reads
// the signed-in user's identity from Google's userinfo endpoint. It returns its
// own Identity type and does not import any domain module — the gateway maps the
// Identity onto account.OAuthIdentity (mirrors the internal/tesla adapter pattern).
package googleauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const userInfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"

// Identity is the verified Google identity of a signed-in user.
type Identity struct {
	Sub   string // Google's stable user id (the OAuth "subject")
	Email string
	Name  string
}

// Client wraps the OAuth config for a single Google app.
type Client struct {
	cfg *oauth2.Config
}

// NewClient builds a Google login client for the given app credentials and the
// exact redirect URI registered with Google.
func NewClient(clientID, clientSecret, redirectURL string) *Client {
	return &Client{cfg: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}}
}

// AuthCodeURL returns the Google consent URL carrying the CSRF state.
func (c *Client) AuthCodeURL(state string) string {
	return c.cfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange trades the authorization code for tokens and returns the user's
// identity from Google's userinfo endpoint.
func (c *Client) Exchange(ctx context.Context, code string) (Identity, error) {
	tok, err := c.cfg.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("exchanging code: %w", err)
	}

	resp, err := c.cfg.Client(ctx, tok).Get(userInfoURL)
	if err != nil {
		return Identity{}, fmt.Errorf("fetching userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("userinfo returned status %d", resp.StatusCode)
	}

	var u struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return Identity{}, fmt.Errorf("decoding userinfo: %w", err)
	}
	return Identity{Sub: u.Sub, Email: u.Email, Name: u.Name}, nil
}
