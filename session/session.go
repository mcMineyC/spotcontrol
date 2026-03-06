package session

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	"github.com/mcMineyC/spotcontrol/ap"
	"github.com/mcMineyC/spotcontrol/apresolve"
	"github.com/mcMineyC/spotcontrol/dealer"
	"github.com/mcMineyC/spotcontrol/login5"
	"github.com/mcMineyC/spotcontrol/mercury"
	devicespb "github.com/mcMineyC/spotcontrol/proto/spotify/connectstate/devices"
	credentialspb "github.com/mcMineyC/spotcontrol/proto/spotify/login5/v3/credentials"
	"github.com/mcMineyC/spotcontrol/spclient"
	"golang.org/x/oauth2"
	spotifyoauth2 "golang.org/x/oauth2/spotify"
)

// Session orchestrates all the components needed to interact with Spotify's
// backend: the access point (AP) TCP connection, Login5 token management,
// spclient HTTP API, dealer WebSocket, and Mercury pub/sub messaging.
//
// Create a Session with NewSessionFromOptions, which performs the full
// connection and authentication flow.
type Session struct {
	log spotcontrol.Logger

	deviceType devicespb.DeviceType
	deviceId   string
	deviceName string

	clientToken string

	client *http.Client

	resolver *apresolve.ApResolver
	login5   *login5.Login5

	ap     *ap.Accesspoint
	hg     *mercury.Client
	sp     *spclient.Spclient
	dealer *dealer.Dealer

	// oauthToken is the OAuth2 token obtained from the interactive PKCE flow.
	// It carries the scopes needed by the Spotify Web API (api.spotify.com),
	// which the Login5 token does not provide. Protected by oauthLock.
	oauthToken *oauth2.Token
	oauthConf  *oauth2.Config
	oauthLock  sync.RWMutex
}

// NewSessionFromOptions creates a new Session by performing the full
// connection and authentication flow:
//
//  1. Retrieve a client token (or use the one provided in Options).
//  2. Resolve AP, spclient, and dealer endpoints via apresolve.
//  3. Connect and authenticate with the access point (AP) using the provided
//     credentials.
//  4. Authenticate with Login5 using the stored credentials from the AP.
//  5. Initialize the spclient HTTP wrapper.
//  6. Initialize the dealer WebSocket client.
//  7. Initialize the Mercury pub/sub client.
//
// The caller is responsible for calling Close when the session is no longer
// needed.
func NewSessionFromOptions(ctx context.Context, opts *Options) (*Session, error) {
	log := opts.Log
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	// Validate device type.
	if opts.DeviceType == devicespb.DeviceType_UNKNOWN {
		return nil, fmt.Errorf("missing device type")
	}

	// Validate device ID (must be a 20-byte hex string = 40 hex chars).
	if deviceIdBytes, err := hex.DecodeString(opts.DeviceId); err != nil {
		return nil, fmt.Errorf("invalid device id (not valid hex): %w", err)
	} else if len(deviceIdBytes) != 20 {
		return nil, fmt.Errorf("invalid device id length: expected 40 hex chars (20 bytes), got %d hex chars", len(opts.DeviceId))
	}

	s := &Session{
		log:        log,
		deviceType: opts.DeviceType,
		deviceId:   opts.DeviceId,
		deviceName: opts.DeviceName,
		client:     opts.Client,
	}

	// Restore persisted OAuth2 token if available.
	if opts.AppState != nil && opts.AppState.HasOAuthToken() {
		s.oauthToken = &oauth2.Token{
			AccessToken:  opts.AppState.OAuthAccessToken,
			RefreshToken: opts.AppState.OAuthRefreshToken,
			TokenType:    opts.AppState.OAuthTokenType,
			Expiry:       opts.AppState.OAuthExpiry,
		}
		log.Debugf("restored persisted OAuth2 token (expires %s)", s.oauthToken.Expiry.Format(time.RFC3339))
	}

	if s.deviceName == "" {
		s.deviceName = "SpotControl"
	}

	if s.client == nil {
		s.client = &http.Client{Timeout: 30 * time.Second}
	}

	// ---- Step 1: Client Token ----
	if len(opts.ClientToken) == 0 {
		var err error
		s.clientToken, err = retrieveClientToken(s.client, s.deviceId)
		if err != nil {
			return nil, fmt.Errorf("failed obtaining client token: %w", err)
		}
		log.Debugf("obtained new client token: %s...%s", s.clientToken[:8], s.clientToken[len(s.clientToken)-4:])
	} else {
		s.clientToken = opts.ClientToken
	}

	// ---- Step 2: AP Resolver ----
	if opts.Resolver != nil {
		s.resolver = opts.Resolver
	} else {
		s.resolver = apresolve.NewApResolver(log, s.client)
	}

	// ---- Step 3: Login5 client (created early, used after AP auth) ----
	s.login5 = login5.NewLogin5(log, s.client, s.deviceId, s.clientToken)

	// ---- Step 4: Access Point connection and authentication ----
	apAddr, err := s.resolver.GetAccesspoint(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting accesspoint from resolver: %w", err)
	}
	s.ap = ap.NewAccesspoint(log, apAddr, s.deviceId)

	switch creds := opts.Credentials.(type) {
	case StoredCredentials:
		if err := s.ap.ConnectStored(ctx, creds.Username, creds.Data); err != nil {
			return nil, fmt.Errorf("failed authenticating AP with stored credentials: %w", err)
		}
	case SpotifyTokenCredentials:
		if err := s.ap.ConnectSpotifyToken(ctx, creds.Username, creds.Token); err != nil {
			return nil, fmt.Errorf("failed authenticating AP with spotify token: %w", err)
		}
	case BlobCredentials:
		if err := s.ap.ConnectBlob(ctx, creds.Username, creds.Blob); err != nil {
			return nil, fmt.Errorf("failed authenticating AP with blob: %w", err)
		}
	case InteractiveCredentials:
		if err := s.connectInteractive(ctx, log, creds); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown credentials type: %T", opts.Credentials)
	}

	// ---- Step 5: Login5 authentication (uses stored creds from AP) ----
	if err := s.login5.Login(ctx, &credentialspb.StoredCredential{
		Username: s.ap.Username(),
		Data:     s.ap.StoredCredentials(),
	}); err != nil {
		return nil, fmt.Errorf("failed authenticating with login5: %w", err)
	}

	// ---- Step 6: Initialize spclient ----
	spAddr, err := s.resolver.GetSpclient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting spclient from resolver: %w", err)
	}
	s.sp, err = spclient.NewSpclient(ctx, log, s.client, spAddr, s.login5.AccessToken(), s.deviceId, s.clientToken)
	if err != nil {
		return nil, fmt.Errorf("failed initializing spclient: %w", err)
	}

	// If we have an OAuth2 token (from interactive login or restored state),
	// configure the spclient to use it for Web API requests.
	if s.oauthToken != nil {
		s.sp.SetWebApiTokenFunc(s.WebApiToken())
		log.Debugf("configured spclient with OAuth2 Web API token")
	}

	// ---- Step 7: Initialize dealer ----
	dealerAddr, err := s.resolver.GetDealer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting dealer from resolver: %w", err)
	}
	s.dealer = dealer.NewDealer(log, s.client, dealerAddr, s.login5.AccessToken())

	// ---- Step 8: Initialize Mercury/Hermes ----
	s.hg = mercury.NewClient(log, s.ap)

	log.Infof("session established for user %s", spotcontrol.ObfuscateUsername(s.ap.Username()))
	return s, nil
}

// connectInteractive performs the OAuth2 PKCE interactive login flow. It starts
// a local HTTP server, prints the authorization URL, and waits for the user to
// complete authentication in their browser.
func (s *Session) connectInteractive(ctx context.Context, log spotcontrol.Logger, creds InteractiveCredentials) error {
	serverCtx, serverCancel := context.WithCancel(ctx)

	callbackPort, codeCh, err := NewOAuth2Server(serverCtx, log, creds.CallbackPort)
	if err != nil {
		serverCancel()
		return fmt.Errorf("failed initializing oauth2 server: %w", err)
	}

	oauthConf := &oauth2.Config{
		ClientID:    spotcontrol.ClientIdHex,
		RedirectURL: fmt.Sprintf("http://127.0.0.1:%d/login", callbackPort),
		Scopes: []string{
			"app-remote-control",
			"playlist-modify",
			"playlist-modify-private",
			"playlist-modify-public",
			"playlist-read",
			"playlist-read-collaborative",
			"playlist-read-private",
			"streaming",
			"ugc-image-upload",
			"user-follow-modify",
			"user-follow-read",
			"user-library-modify",
			"user-library-read",
			"user-modify",
			"user-modify-playback-state",
			"user-modify-private",
			"user-personalized",
			"user-read-birthdate",
			"user-read-currently-playing",
			"user-read-email",
			"user-read-play-history",
			"user-read-playback-position",
			"user-read-playback-state",
			"user-read-private",
			"user-read-recently-played",
			"user-top-read",
		},
		Endpoint: spotifyoauth2.Endpoint,
	}

	verifier := oauth2.GenerateVerifier()
	authURL := oauthConf.AuthCodeURL("", oauth2.S256ChallengeOption(verifier))
	log.Infof("to complete authentication, visit the following URL in your browser:\n%s", authURL)
	fmt.Printf("\nOpen this URL in your browser to log in:\n%s\n\n", authURL)

	var code string
	select {
	case c, ok := <-codeCh:
		if !ok || c == "" {
			serverCancel()
			return fmt.Errorf("oauth2 callback channel closed without receiving a code")
		}
		code = c
	case <-ctx.Done():
		serverCancel()
		return fmt.Errorf("context cancelled while waiting for oauth2 callback: %w", ctx.Err())
	}

	serverCancel()

	token, err := oauthConf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("failed exchanging oauth2 code: %w", err)
	}

	// Store the full OAuth2 token and config so we can refresh it later and
	// use it for Web API requests (api.spotify.com). The Login5 token only
	// works for spclient endpoints.
	s.oauthLock.Lock()
	s.oauthToken = token
	s.oauthConf = oauthConf
	s.oauthLock.Unlock()
	log.Debugf("stored OAuth2 token (expires %s, has refresh=%v)", token.Expiry.Format(time.RFC3339), token.RefreshToken != "")

	// The Spotify OAuth2 token response includes the username in the extra
	// data. If it's not there, we'll pass an empty string and let the AP
	// figure it out from the token.
	username := ""
	if u, ok := token.Extra("username").(string); ok {
		username = u
	}

	if err := s.ap.ConnectSpotifyToken(ctx, username, token.AccessToken); err != nil {
		return fmt.Errorf("failed authenticating AP with interactive token: %w", err)
	}

	return nil
}

// Close shuts down the session, closing all connections and stopping all
// background goroutines. After Close returns, the session should not be used.
func (s *Session) Close() {
	if s.hg != nil {
		s.hg.Close()
	}
	if s.dealer != nil {
		s.dealer.Close()
	}
	if s.ap != nil {
		s.ap.Close()
	}
	s.log.Debugf("session closed")
}

// Accesspoint returns the underlying AP connection.
func (s *Session) Accesspoint() *ap.Accesspoint {
	return s.ap
}

// Spclient returns the underlying spclient HTTP wrapper.
func (s *Session) Spclient() *spclient.Spclient {
	return s.sp
}

// Dealer returns the underlying dealer WebSocket client.
func (s *Session) Dealer() *dealer.Dealer {
	return s.dealer
}

// Mercury returns the underlying Mercury pub/sub client.
func (s *Session) Mercury() *mercury.Client {
	return s.hg
}

// Login5Client returns the underlying Login5 authentication client.
func (s *Session) Login5Client() *login5.Login5 {
	return s.login5
}

// Resolver returns the underlying AP resolver.
func (s *Session) Resolver() *apresolve.ApResolver {
	return s.resolver
}

// Username returns the canonical username for the authenticated session.
func (s *Session) Username() string {
	return s.ap.Username()
}

// StoredCredentials returns the reusable authentication credentials from the
// AP handshake. These can be persisted to disk and passed as StoredCredentials
// in Options to avoid re-entering the password.
func (s *Session) StoredCredentials() []byte {
	return s.ap.StoredCredentials()
}

// DeviceId returns the device ID for this session.
func (s *Session) DeviceId() string {
	return s.deviceId
}

// DeviceName returns the device name for this session.
func (s *Session) DeviceName() string {
	return s.deviceName
}

// DeviceType returns the device type for this session.
func (s *Session) DeviceType() devicespb.DeviceType {
	return s.deviceType
}

// ClientToken returns the client token for this session.
func (s *Session) ClientToken() string {
	return s.clientToken
}

// AccessToken returns a fresh Login5 access token. If force is true, a new
// token is obtained even if the current one hasn't expired.
func (s *Session) AccessToken(ctx context.Context, force bool) (string, error) {
	return s.login5.AccessToken()(ctx, force)
}

// WebApiToken returns a GetLogin5TokenFunc (same signature) that provides
// OAuth2 access tokens suitable for the Spotify Web API (api.spotify.com).
//
// Unlike the Login5 token (which only works for spclient endpoints), the
// OAuth2 token carries the scopes requested during the interactive PKCE flow
// (user-read-playback-state, user-modify-playback-state, etc.).
//
// The returned function automatically refreshes the token when it has expired,
// using the stored refresh token and OAuth2 config.
func (s *Session) WebApiToken() spotcontrol.GetLogin5TokenFunc {
	return func(ctx context.Context, force bool) (string, error) {
		s.oauthLock.RLock()
		tok := s.oauthToken
		conf := s.oauthConf
		s.oauthLock.RUnlock()

		if tok == nil {
			return "", fmt.Errorf("no OAuth2 token available (interactive login required)")
		}

		// If not forced and the token is still valid, return it.
		if !force && tok.Valid() {
			return tok.AccessToken, nil
		}

		// Token expired or forced refresh — use the refresh token.
		if tok.RefreshToken == "" {
			// No refresh token; return the (possibly expired) access token and
			// let the caller deal with any 401 error.
			s.log.Warnf("OAuth2 token expired and no refresh token available")
			return tok.AccessToken, nil
		}

		if conf == nil {
			// We have a token from persisted state but no oauth config (the
			// interactive flow was not run this session). Build one now with
			// the same client ID and endpoint — we don't need RedirectURL or
			// Scopes for a refresh.
			conf = &oauth2.Config{
				ClientID: spotcontrol.ClientIdHex,
				Endpoint: spotifyoauth2.Endpoint,
			}
		}

		s.log.Debugf("refreshing OAuth2 Web API token")
		src := conf.TokenSource(ctx, tok)
		newTok, err := src.Token()
		if err != nil {
			return "", fmt.Errorf("failed refreshing OAuth2 token: %w", err)
		}

		s.oauthLock.Lock()
		s.oauthToken = newTok
		s.oauthLock.Unlock()

		s.log.Debugf("refreshed OAuth2 token (new expiry %s)", newTok.Expiry.Format(time.RFC3339))
		return newTok.AccessToken, nil
	}
}

// OAuthToken returns the current OAuth2 token, or nil if none is available.
// This can be used to persist the token across sessions via AppState.
func (s *Session) OAuthToken() *oauth2.Token {
	s.oauthLock.RLock()
	defer s.oauthLock.RUnlock()
	return s.oauthToken
}
