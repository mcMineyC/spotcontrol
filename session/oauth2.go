package session

import (
	"context"
	"fmt"
	"net"
	"net/http"

	spotcontrol "github.com/mcMineyC/spotcontrol"
)

// NewOAuth2Server starts a local HTTP server that listens for the OAuth2
// authorization code callback from the Spotify accounts service. It returns
// the actual port the server is listening on, a channel that will receive the
// authorization code, and any error encountered during setup.
//
// The server is shut down gracefully when the provided context is cancelled.
// The code channel receives exactly one value (the authorization code string)
// when the callback is received, then the channel is closed.
func NewOAuth2Server(ctx context.Context, log spotcontrol.Logger, port int) (int, <-chan string, error) {
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	codeCh := make(chan string, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errMsg := r.URL.Query().Get("error")
			if errMsg == "" {
				errMsg = "no authorization code received"
			}
			http.Error(w, fmt.Sprintf("Authorization failed: %s", errMsg), http.StatusBadRequest)
			log.Errorf("oauth2 callback error: %s", errMsg)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>SpotControl</title></head>
<body>
<h1>Authentication successful!</h1>
<p>You can close this window and return to the application.</p>
</body>
</html>`))

		log.Infof("received oauth2 authorization code")

		select {
		case codeCh <- code:
		default:
		}
	})

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to start oauth2 callback listener on port %d: %w", port, err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port

	server := &http.Server{
		Handler: mux,
	}

	// Start serving in a goroutine.
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Errorf("oauth2 callback server error")
		}
	}()

	// Shut down the server when the context is cancelled.
	go func() {
		<-ctx.Done()
		if err := server.Close(); err != nil {
			log.WithError(err).Warnf("failed to close oauth2 callback server")
		}
		// Ensure the code channel is closed so consumers don't hang.
		// The select prevents double-close panic if a code was already sent.
		select {
		case <-codeCh:
		default:
			close(codeCh)
		}
	}()

	log.Infof("oauth2 callback server listening on http://127.0.0.1:%d/login", actualPort)

	return actualPort, codeCh, nil
}
