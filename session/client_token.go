package session

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	pbdata "github.com/mcMineyC/spotcontrol/proto/spotify/clienttoken/data/v0"
	pbhttp "github.com/mcMineyC/spotcontrol/proto/spotify/clienttoken/http/v0"
	"google.golang.org/protobuf/proto"
)

// retrieveClientToken fetches a new client token from the Spotify client token
// endpoint (https://clienttoken.spotify.com/v1/clienttoken). The client token
// is required by the Login5 and spclient APIs and is bound to the device ID
// and platform.
//
// If the server responds with a challenge (e.g. hashcash or JS evaluation),
// an error is returned because challenge solving for client tokens is not
// currently supported.
func retrieveClientToken(c *http.Client, deviceId string) (string, error) {
	body, err := proto.Marshal(&pbhttp.ClientTokenRequest{
		RequestType: pbhttp.ClientTokenRequestType_REQUEST_CLIENT_DATA_REQUEST,
		Request: &pbhttp.ClientTokenRequest_ClientData{
			ClientData: &pbhttp.ClientDataRequest{
				ClientId:      spotcontrol.ClientIdHex,
				ClientVersion: spotcontrol.SpotifyLikeClientVersion(),
				Data: &pbhttp.ClientDataRequest_ConnectivitySdkData{
					ConnectivitySdkData: &pbdata.ConnectivitySdkData{
						DeviceId:             deviceId,
						PlatformSpecificData: spotcontrol.GetPlatformSpecificData(),
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed marshalling ClientTokenRequest: %w", err)
	}

	reqUrl, err := url.Parse("https://clienttoken.spotify.com/v1/clienttoken")
	if err != nil {
		return "", fmt.Errorf("invalid clienttoken url: %w", err)
	}

	resp, err := c.Do(&http.Request{
		Method: "POST",
		URL:    reqUrl,
		Header: http.Header{
			"Accept":       []string{"application/x-protobuf"},
			"Content-Type": []string{"application/x-protobuf"},
			"User-Agent":   []string{spotcontrol.UserAgent()},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	})
	if err != nil {
		return "", fmt.Errorf("failed requesting clienttoken: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("invalid status code from clienttoken: %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed reading clienttoken response: %w", err)
	}

	var protoResp pbhttp.ClientTokenResponse
	if err := proto.Unmarshal(respBody, &protoResp); err != nil {
		return "", fmt.Errorf("failed unmarshalling clienttoken response: %w", err)
	}

	switch protoResp.ResponseType {
	case pbhttp.ClientTokenResponseType_RESPONSE_GRANTED_TOKEN_RESPONSE:
		granted := protoResp.GetGrantedToken()
		if granted == nil {
			return "", fmt.Errorf("clienttoken granted response is nil")
		}
		return granted.Token, nil
	case pbhttp.ClientTokenResponseType_RESPONSE_CHALLENGES_RESPONSE:
		return "", fmt.Errorf("clienttoken challenge not supported")
	default:
		return "", fmt.Errorf("unknown clienttoken response type: %v", protoResp.ResponseType)
	}
}
