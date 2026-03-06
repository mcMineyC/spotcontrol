package apresolve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	spotcontrol "github.com/badfortrains/spotcontrol"
)

type apResolveResponse struct {
	Accesspoint []string `json:"accesspoint,omitempty"`
	Dealer      []string `json:"dealer,omitempty"`
	Spclient    []string `json:"spclient,omitempty"`
}

// ApResolver fetches and caches Spotify service endpoint URLs (accesspoints,
// dealers, and spclients) from the apresolve service. It is safe for
// concurrent use.
type ApResolver struct {
	log spotcontrol.Logger

	baseUrl *url.URL
	client  *http.Client

	endpoints     map[endpointType][]string
	endpointsExp  map[endpointType]time.Time
	endpointsLock sync.RWMutex
}

// NewApResolver creates a new resolver. If client is nil a default HTTP client
// with a 30-second timeout is used.
func NewApResolver(log spotcontrol.Logger, client *http.Client) *ApResolver {
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	baseUrl, err := url.Parse("https://apresolve.spotify.com/")
	if err != nil {
		panic("invalid apresolve base URL")
	}

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &ApResolver{
		log:          log,
		baseUrl:      baseUrl,
		client:       client,
		endpoints:    map[endpointType][]string{},
		endpointsExp: map[endpointType]time.Time{},
	}
}

func (r *ApResolver) fetchUrls(ctx context.Context, types ...endpointType) error {
	anyExpired := false
	r.endpointsLock.RLock()
	for _, t := range types {
		if exp, ok := r.endpointsExp[t]; !ok {
			anyExpired = true
			break
		} else if exp.Before(time.Now()) {
			anyExpired = true
			break
		}
	}
	r.endpointsLock.RUnlock()

	if !anyExpired {
		return nil
	}

	query := url.Values{}
	for _, t := range types {
		query.Add("type", string(t))
	}

	reqUrl := *r.baseUrl
	reqUrl.RawQuery = query.Encode()

	req := &http.Request{
		Method: "GET",
		URL:    &reqUrl,
		Header: http.Header{
			"User-Agent": []string{spotcontrol.UserAgent()},
		},
	}

	resp, err := r.client.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed fetching apresolve URL: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("invalid status code from apresolve: %d", resp.StatusCode)
	}

	var respJson apResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&respJson); err != nil {
		return fmt.Errorf("failed unmarshalling apresolve response: %w", err)
	}

	r.endpointsLock.Lock()
	defer r.endpointsLock.Unlock()

	ttl := 1 * time.Hour

	for _, t := range types {
		switch t {
		case endpointTypeAccesspoint:
			if len(respJson.Accesspoint) > 0 {
				r.endpoints[endpointTypeAccesspoint] = respJson.Accesspoint
				r.endpointsExp[endpointTypeAccesspoint] = time.Now().Add(ttl)
				r.log.Debugf("fetched new accesspoints: %v", respJson.Accesspoint)
			}
		case endpointTypeDealer:
			if len(respJson.Dealer) > 0 {
				r.endpoints[endpointTypeDealer] = respJson.Dealer
				r.endpointsExp[endpointTypeDealer] = time.Now().Add(ttl)
				r.log.Debugf("fetched new dealers: %v", respJson.Dealer)
			}
		case endpointTypeSpclient:
			if len(respJson.Spclient) > 0 {
				r.endpoints[endpointTypeSpclient] = respJson.Spclient
				r.endpointsExp[endpointTypeSpclient] = time.Now().Add(ttl)
				r.log.Debugf("fetched new spclients: %v", respJson.Spclient)
			}
		}
	}

	return nil
}

// FetchAll fetches all three endpoint types in a single request. Subsequent
// calls are served from the cache until the 1-hour TTL expires.
func (r *ApResolver) FetchAll(ctx context.Context) error {
	return r.fetchUrls(ctx, endpointTypeAccesspoint, endpointTypeDealer, endpointTypeSpclient)
}

func (r *ApResolver) get(ctx context.Context, t endpointType) ([]string, error) {
	if err := r.fetchUrls(ctx, t); err != nil {
		return nil, err
	}

	r.endpointsLock.RLock()
	defer r.endpointsLock.RUnlock()

	addrs, ok := r.endpoints[t]
	if !ok || len(addrs) == 0 {
		return nil, fmt.Errorf("no %s endpoint present", t)
	}

	return addrs, nil
}

// getFunc returns a GetAddressFunc that iterates through the cached addresses
// for the given endpoint type, automatically fetching new ones when the list
// is exhausted.
func (r *ApResolver) getFunc(ctx context.Context, t endpointType) (spotcontrol.GetAddressFunc, error) {
	addrs, err := r.get(ctx, t)
	if err != nil {
		return nil, err
	}

	idx := 0
	return func(innerCtx context.Context) string {
		if idx < len(addrs) {
			addr := addrs[idx]
			idx++
			return addr
		}

		// try fetching new addresses
		newAddrs, err := r.get(innerCtx, t)
		if err != nil {
			r.log.WithError(err).Warnf("failed fetching new endpoint for %s", t)
			// fall back to first known address
			return addrs[0]
		}

		addrs = newAddrs
		idx = 1
		return addrs[0]
	}, nil
}

// GetAccesspoint returns a GetAddressFunc that rotates through available
// access point addresses.
func (r *ApResolver) GetAccesspoint(ctx context.Context) (spotcontrol.GetAddressFunc, error) {
	return r.getFunc(ctx, endpointTypeAccesspoint)
}

// GetSpclient returns a GetAddressFunc that rotates through available spclient
// addresses.
func (r *ApResolver) GetSpclient(ctx context.Context) (spotcontrol.GetAddressFunc, error) {
	return r.getFunc(ctx, endpointTypeSpclient)
}

// GetDealer returns a GetAddressFunc that rotates through available dealer
// addresses.
func (r *ApResolver) GetDealer(ctx context.Context) (spotcontrol.GetAddressFunc, error) {
	return r.getFunc(ctx, endpointTypeDealer)
}
