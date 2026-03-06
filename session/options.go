package session

import (
	"net/http"

	spotcontrol "github.com/badfortrains/spotcontrol"
	"github.com/badfortrains/spotcontrol/apresolve"
	devicespb "github.com/badfortrains/spotcontrol/proto/spotify/connectstate/devices"
)

// Options configures a new Session. At minimum, DeviceType, DeviceId, and
// Credentials must be provided.
type Options struct {
	// Log is the base logger to use. If nil, a NullLogger is used.
	Log spotcontrol.Logger

	// DeviceType is the Spotify device type shown to other clients (e.g.
	// COMPUTER, SPEAKER). Required.
	DeviceType devicespb.DeviceType

	// DeviceId is the hex-encoded 20-byte Spotify device identifier. Required.
	DeviceId string

	// DeviceName is the human-readable name shown in Spotify Connect device
	// lists. If empty, "SpotControl" is used.
	DeviceName string

	// Credentials is the authentication method to use. Must be one of:
	//   - StoredCredentials
	//   - SpotifyTokenCredentials
	//   - BlobCredentials
	//   - InteractiveCredentials
	Credentials any

	// ClientToken is an existing Spotify client token. If empty, a new one is
	// retrieved automatically from https://clienttoken.spotify.com/v1/clienttoken.
	ClientToken string

	// Resolver is an existing ApResolver instance. If nil, a new one is created
	// using the default apresolve endpoint.
	Resolver *apresolve.ApResolver

	// Client is the HTTP client used for all HTTP requests (login5, spclient,
	// apresolve, client token, etc.). If nil, a default client with a 30-second
	// timeout is created.
	Client *http.Client

	// AppState is the persisted application state from a previous session. If
	// provided and it contains an OAuth2 token, that token will be restored so
	// that Web API requests (api.spotify.com) work without requiring a new
	// interactive login. May be nil.
	AppState *spotcontrol.AppState
}

// StoredCredentials authenticates using a username and stored credential bytes
// obtained from a previous successful authentication (APWelcome's
// reusable_auth_credentials).
type StoredCredentials struct {
	Username string
	Data     []byte
}

// SpotifyTokenCredentials authenticates using a username and an OAuth/Spotify
// access token (e.g. from an interactive OAuth2 PKCE flow).
type SpotifyTokenCredentials struct {
	Username string
	Token    string
}

// BlobCredentials authenticates using an encrypted discovery blob (base64-encoded)
// obtained via Spotify Connect zeroconf discovery.
type BlobCredentials struct {
	Username string
	Blob     []byte
}

// InteractiveCredentials triggers an OAuth2 PKCE interactive login flow. A
// local HTTP server is started on CallbackPort (or a random port if 0) to
// receive the authorization code callback. The user must open the printed URL
// in a browser to complete authentication.
type InteractiveCredentials struct {
	// CallbackPort is the local port for the OAuth2 callback server. Use 0 for
	// a random available port.
	CallbackPort int
}
