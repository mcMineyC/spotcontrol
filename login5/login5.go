package login5

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	spotcontrol "github.com/badfortrains/spotcontrol"
	pb "github.com/badfortrains/spotcontrol/proto/spotify/login5/v3"
	credentialspb "github.com/badfortrains/spotcontrol/proto/spotify/login5/v3/credentials"
	"google.golang.org/protobuf/proto"
)

// LoginError is returned when the Login5 endpoint returns an error code
// instead of a successful authentication response.
type LoginError struct {
	Code pb.LoginError
}

func (e *LoginError) Error() string {
	return fmt.Sprintf("failed authenticating with login5: %v", e.Code)
}

// Login5 manages authentication against the Spotify Login5 endpoint
// (https://login5.spotify.com/v3/login). It handles challenge solving
// (hashcash), stores the resulting access token, and provides automatic
// token renewal via the AccessToken function.
//
// Login5 is safe for concurrent use.
type Login5 struct {
	log     spotcontrol.Logger
	baseUrl *url.URL
	client  *http.Client

	deviceId    string
	clientToken string

	loginOk     *pb.LoginOk
	loginOkExp  time.Time
	loginOkLock sync.RWMutex
}

// NewLogin5 creates a new Login5 client. The deviceId and clientToken are
// required for constructing LoginRequest messages. If client is nil a default
// HTTP client with a 30-second timeout is used.
func NewLogin5(log spotcontrol.Logger, client *http.Client, deviceId, clientToken string) *Login5 {
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	baseUrl, err := url.Parse("https://login5.spotify.com/")
	if err != nil {
		panic("invalid login5 base URL")
	}

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &Login5{
		log:         log,
		baseUrl:     baseUrl,
		client:      client,
		deviceId:    deviceId,
		clientToken: clientToken,
	}
}

// request sends a single LoginRequest to the Login5 endpoint and returns the
// parsed LoginResponse.
func (c *Login5) request(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	body, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling LoginRequest: %w", err)
	}

	httpReq := &http.Request{
		Method: "POST",
		URL:    c.baseUrl.JoinPath("/v3/login"),
		Header: http.Header{
			"Accept":       []string{"application/x-protobuf"},
			"Content-Type": []string{"application/x-protobuf"},
			"User-Agent":   []string{spotcontrol.UserAgent()},
			"Client-Token": []string{c.clientToken},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}

	resp, err := c.client.Do(httpReq.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed requesting login5: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading login5 response: %w", err)
	}

	var protoResp pb.LoginResponse
	if err := proto.Unmarshal(respBody, &protoResp); err != nil {
		return nil, fmt.Errorf("failed unmarshalling LoginResponse: %w", err)
	}

	return &protoResp, nil
}

// Login authenticates with the Login5 endpoint using the given credentials
// protobuf. Supported credential types are StoredCredential,
// FacebookAccessToken, OneTimeToken, ParentChildCredential,
// AppleSignInCredential, SamsungSignInCredential, and GoogleSignInCredential.
//
// If the server responds with challenges (e.g. hashcash), they are solved
// automatically and the request is retried with the solutions.
func (c *Login5) Login(ctx context.Context, credentials proto.Message) error {
	c.loginOkLock.Lock()
	defer c.loginOkLock.Unlock()

	req := &pb.LoginRequest{
		ClientInfo: &pb.ClientInfo{
			ClientId: spotcontrol.ClientIdHex,
			DeviceId: c.deviceId,
		},
	}

	switch lm := credentials.(type) {
	case *credentialspb.StoredCredential:
		req.LoginMethod = &pb.LoginRequest_StoredCredential{StoredCredential: lm}
	case *credentialspb.Password:
		req.LoginMethod = &pb.LoginRequest_Password{Password: lm}
	case *credentialspb.FacebookAccessToken:
		req.LoginMethod = &pb.LoginRequest_FacebookAccessToken{FacebookAccessToken: lm}
	case *credentialspb.OneTimeToken:
		req.LoginMethod = &pb.LoginRequest_OneTimeToken{OneTimeToken: lm}
	case *credentialspb.ParentChildCredential:
		req.LoginMethod = &pb.LoginRequest_ParentChildCredential{ParentChildCredential: lm}
	case *credentialspb.AppleSignInCredential:
		req.LoginMethod = &pb.LoginRequest_AppleSignInCredential{AppleSignInCredential: lm}
	case *credentialspb.SamsungSignInCredential:
		req.LoginMethod = &pb.LoginRequest_SamsungSignInCredential{SamsungSignInCredential: lm}
	case *credentialspb.GoogleSignInCredential:
		req.LoginMethod = &pb.LoginRequest_GoogleSignInCredential{GoogleSignInCredential: lm}
	default:
		return fmt.Errorf("invalid credentials type: %T", lm)
	}

	resp, err := c.request(ctx, req)
	if err != nil {
		return fmt.Errorf("failed requesting login5 endpoint: %w", err)
	}

	// If the server responded with challenges, solve them and retry.
	if ch := resp.GetChallenges(); ch != nil && len(ch.Challenges) > 0 {
		req.LoginContext = resp.LoginContext
		req.ChallengeSolutions = &pb.ChallengeSolutions{}

		for _, challenge := range ch.Challenges {
			switch cc := challenge.Challenge.(type) {
			case *pb.Challenge_Hashcash:
				sol := solveHashcash(req.LoginContext, cc.Hashcash)
				req.ChallengeSolutions.Solutions = append(req.ChallengeSolutions.Solutions, &pb.ChallengeSolution{
					Solution: &pb.ChallengeSolution_Hashcash{Hashcash: sol},
				})
			case *pb.Challenge_Code:
				return fmt.Errorf("login5 code challenge not supported")
			default:
				return fmt.Errorf("login5 unknown challenge type: %T", cc)
			}
		}

		resp, err = c.request(ctx, req)
		if err != nil {
			return fmt.Errorf("failed requesting login5 endpoint with challenge solutions: %w", err)
		}
	}

	if ok := resp.GetOk(); ok != nil {
		c.loginOk = ok
		c.loginOkExp = time.Now().Add(time.Duration(c.loginOk.AccessTokenExpiresIn) * time.Second)
		c.log.WithField("username", spotcontrol.ObfuscateUsername(c.loginOk.Username)).
			Infof("authenticated Login5")
		return nil
	}

	return &LoginError{Code: resp.GetError()}
}

// Username returns the canonical username from the most recent successful
// Login5 authentication. It panics if Login has not been called successfully.
func (c *Login5) Username() string {
	c.loginOkLock.RLock()
	defer c.loginOkLock.RUnlock()

	if c.loginOk == nil {
		panic("login5 not authenticated")
	}

	return c.loginOk.Username
}

// StoredCredential returns the stored credential bytes from the most recent
// successful Login5 authentication. These can be used to re-authenticate
// without the original password. It panics if Login has not been called
// successfully.
func (c *Login5) StoredCredential() []byte {
	c.loginOkLock.RLock()
	defer c.loginOkLock.RUnlock()

	if c.loginOk == nil {
		panic("login5 not authenticated")
	}

	return c.loginOk.StoredCredential
}

// AccessToken returns a GetLogin5TokenFunc that retrieves (and automatically
// renews) the Login5 access token. The returned function is safe for
// concurrent use.
//
// When force is true, a fresh token is always obtained by re-authenticating
// with the stored credentials. When force is false, the cached token is
// returned if it has not yet expired.
func (c *Login5) AccessToken() spotcontrol.GetLogin5TokenFunc {
	return func(ctx context.Context, force bool) (string, error) {
		c.loginOkLock.RLock()
		if c.loginOk == nil {
			c.loginOkLock.RUnlock()
			return "", fmt.Errorf("login5 not authenticated")
		}

		// If not forced and not expired, return cached token.
		if !force && c.loginOkExp.After(time.Now()) {
			defer c.loginOkLock.RUnlock()
			return c.loginOk.AccessToken, nil
		}

		username, storedCred := c.loginOk.Username, c.loginOk.StoredCredential
		c.loginOkLock.RUnlock()

		c.log.Debug("renewing login5 access token")
		if err := c.Login(ctx, &credentialspb.StoredCredential{
			Username: username,
			Data:     storedCred,
		}); err != nil {
			return "", fmt.Errorf("failed renewing login5 access token: %w", err)
		}

		c.loginOkLock.RLock()
		defer c.loginOkLock.RUnlock()
		return c.loginOk.AccessToken, nil
	}
}
