package spotcontrol

import (
	"context"
	"time"
)

// GetAddressFunc is a function that returns a different address for a type of endpoint
// each time it is called, rotating through available addresses.
type GetAddressFunc func(ctx context.Context) string

// GetLogin5TokenFunc is a function that returns an access token from Login5.
// If force is true, a new token will be obtained even if the current one hasn't expired.
type GetLogin5TokenFunc func(ctx context.Context, force bool) (string, error)

// AppState holds persisted state across sessions.
type AppState struct {
	// DeviceId is the unique device identifier for this controller instance.
	DeviceId string `json:"device_id"`
	// Username is the authenticated Spotify username.
	Username string `json:"username,omitempty"`
	// StoredCredentials is the reusable auth credential data from APWelcome.
	StoredCredentials []byte `json:"stored_credentials,omitempty"`

	// OAuth2 token fields for Web API access persistence.
	// Login5 tokens only work for spclient endpoints; the public Web API
	// (api.spotify.com) requires a standard OAuth2 access token obtained via
	// the Authorization Code / PKCE flow. These fields allow the token to be
	// saved and restored across sessions so the user doesn't need to
	// re-authenticate interactively every time.

	// OAuthAccessToken is the OAuth2 access token for the Spotify Web API.
	OAuthAccessToken string `json:"oauth_access_token,omitempty"`
	// OAuthRefreshToken is the OAuth2 refresh token used to obtain new access
	// tokens when the current one expires.
	OAuthRefreshToken string `json:"oauth_refresh_token,omitempty"`
	// OAuthTokenType is the token type (typically "Bearer").
	OAuthTokenType string `json:"oauth_token_type,omitempty"`
	// OAuthExpiry is the time at which the access token expires.
	OAuthExpiry time.Time `json:"oauth_expiry,omitempty"`
}

// HasOAuthToken returns true if the AppState contains a persisted OAuth2 token
// (at minimum an access token or a refresh token).
func (s *AppState) HasOAuthToken() bool {
	return s.OAuthAccessToken != "" || s.OAuthRefreshToken != ""
}
