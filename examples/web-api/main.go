// web-api — spotcontrol example
//
// Exposes the same playback/device/metadata commands that the
// examples/micro-controller CLI offers, but over a small JSON HTTP API
// instead of a stdin REPL. Useful for driving Spotify Connect from curl,
// a phone shortcut, a home-automation system, or any other HTTP client.
//
// Usage:
//
//	go run . -interactive
//	go run . -addr :8080
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	"github.com/mcMineyC/spotcontrol/controller"
	"github.com/mcMineyC/spotcontrol/quick"
)

const defaultStatePath = "spotcontrol_state.json"

// ---------------------------------------------------------------------------
// Spotify URL parsing (same logic as examples/micro-controller)
// ---------------------------------------------------------------------------

// parseSpotifyURL parses a Spotify URL like
//
//	https://open.spotify.com/track/2AX9H0uIFZqo9zAcwclQy9?si=75167feac6a447db
//	https://open.spotify.com/playlist/5ese9XhQqKHoQg4WJ4sZef
//	https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy
//
// and returns the entity type ("track", "playlist", "album", etc.) and the
// base62 ID. Returns empty strings if the URL is not a recognised Spotify URL.
func parseSpotifyURL(raw string) (entityType string, id string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}

	host := strings.ToLower(u.Hostname())
	if host != "open.spotify.com" && host != "play.spotify.com" {
		return "", ""
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")

	if len(parts) >= 3 && strings.HasPrefix(parts[0], "intl") {
		parts = parts[1:]
	}

	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}

	return parts[0], parts[1]
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// server wraps the connected spotcontrol session/controller and serves the
// HTTP API.
type server struct {
	result *quick.ConnectResult
}

func main() {
	deviceName := flag.String("devicename", "SpotControl Web API", "name of this device")
	interactive := flag.Bool("interactive", false, "use interactive OAuth2 PKCE login")
	callbackPort := flag.Int("callback-port", 0, "OAuth2 callback port (0 = random)")
	statePath := flag.String("state", defaultStatePath, "path to state file")
	addr := flag.String("addr", ":8080", "address for the HTTP API to listen on")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	result, err := quick.Connect(ctx, quick.QuickConfig{
		StatePath:    *statePath,
		DeviceName:   *deviceName,
		DeviceType:   spotcontrol.DeviceTypeComputer,
		Interactive:  *interactive,
		CallbackPort: *callbackPort,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer result.Close()

	fmt.Printf("Connected as: %s\n", result.Session.Username())
	fmt.Printf("Device ID: %s\n", result.Session.DeviceId())

	srv := &server{result: result}

	mux := http.NewServeMux()
	srv.routes(mux)

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: withCORS(mux),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Listening on %s\n", *addr)
	fmt.Println("See README.md for the full list of endpoints.")

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http server error: %v", err)
	}

	fmt.Println("Goodbye!")
}

// withCORS adds permissive CORS headers so the API can be called directly
// from browser-based tools during local development.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", s.handleHealth)

	// Devices
	mux.HandleFunc("GET /devices", s.handleDevices)
	mux.HandleFunc("GET /devices/api", s.handleDevicesFromAPI)

	// Player state / metadata
	mux.HandleFunc("GET /state", s.handleState)
	mux.HandleFunc("GET /metadata", s.handleMetadataCached)
	mux.HandleFunc("GET /metadata/fetch", s.handleMetadataFetch)

	// Transport controls
	mux.HandleFunc("POST /play", s.handlePlay)
	mux.HandleFunc("POST /pause", s.handlePause)
	mux.HandleFunc("POST /next", s.handleNext)
	mux.HandleFunc("POST /previous", s.handlePrevious)
	mux.HandleFunc("POST /seek", s.handleSeek)
	mux.HandleFunc("POST /volume", s.handleVolume)
	mux.HandleFunc("POST /shuffle", s.handleShuffle)
	mux.HandleFunc("POST /repeat", s.handleRepeat)
	mux.HandleFunc("POST /queue", s.handleQueue)
	mux.HandleFunc("POST /transfer", s.handleTransfer)

	// Loading / playing content
	mux.HandleFunc("POST /load", s.handleLoad)
	mux.HandleFunc("POST /play-track", s.handlePlayTrack)
	mux.HandleFunc("POST /play-playlist", s.handlePlayPlaylist)
	mux.HandleFunc("POST /open", s.handleOpen)

	// Live events (SSE)
	mux.HandleFunc("GET /events", s.handleEvents)
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// decodeJSON reads a JSON body into v. A missing/empty body is treated as an
// empty object so that endpoints with all-optional fields (e.g. "play the
// active device") can be called with no body at all.
func decodeJSON(r *http.Request, v interface{}) error {
	if r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Health / devices / state / metadata
// ---------------------------------------------------------------------------

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"username":  s.result.Session.Username(),
		"device_id": s.result.Session.DeviceId(),
	})
}

// GET /devices — devices from the cached connect-state cluster (instant, no
// network request).
func (s *server) handleDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.result.ListDevices()
	if devices == nil {
		devices = []controller.DeviceInfo{}
	}
	writeJSON(w, http.StatusOK, devices)
}

// GET /devices/api — devices queried live from the Spotify Web API.
func (s *server) handleDevicesFromAPI(w http.ResponseWriter, r *http.Request) {
	devices, err := s.result.ListDevicesFromAPI(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if devices == nil {
		devices = []controller.DeviceInfo{}
	}
	writeJSON(w, http.StatusOK, devices)
}

// GET /state — current player state (cluster cache, falls back to Web API).
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	state, err := s.result.GetPlayerState(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if state == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// GET /metadata — cached rich metadata for the currently playing track.
func (s *server) handleMetadataCached(w http.ResponseWriter, r *http.Request) {
	meta := s.result.GetTrackMetadata()
	writeJSON(w, http.StatusOK, meta)
}

// GET /metadata/fetch?uri=spotify:track:... — fetch rich metadata for a
// specific track. If uri is omitted, fetches metadata for whatever is
// currently playing.
func (s *server) handleMetadataFetch(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.Query().Get("uri")

	var meta *controller.TrackMetadata
	var err error
	if uri != "" {
		meta, err = s.result.FetchTrackMetadata(r.Context(), uri)
	} else {
		meta, err = s.result.FetchCurrentTrackMetadata(r.Context())
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// ---------------------------------------------------------------------------
// Transport controls
// ---------------------------------------------------------------------------

type deviceOnlyRequest struct {
	DeviceId string `json:"device_id"`
}

func (s *server) handlePlay(w http.ResponseWriter, r *http.Request) {
	var req deviceOnlyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.result.Play(r.Context(), req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

func (s *server) handlePause(w http.ResponseWriter, r *http.Request) {
	var req deviceOnlyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.result.Pause(r.Context(), req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

func (s *server) handleNext(w http.ResponseWriter, r *http.Request) {
	var req deviceOnlyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.result.Next(r.Context(), req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

func (s *server) handlePrevious(w http.ResponseWriter, r *http.Request) {
	var req deviceOnlyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.result.Previous(r.Context(), req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type seekRequest struct {
	PositionMs int64  `json:"position_ms"`
	DeviceId   string `json:"device_id"`
}

func (s *server) handleSeek(w http.ResponseWriter, r *http.Request) {
	var req seekRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.result.Seek(r.Context(), req.PositionMs, req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type volumeRequest struct {
	Volume   int    `json:"volume"`
	DeviceId string `json:"device_id"`
}

func (s *server) handleVolume(w http.ResponseWriter, r *http.Request) {
	var req volumeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Volume < 0 || req.Volume > 100 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("volume must be between 0 and 100"))
		return
	}
	if err := s.result.SetVolume(r.Context(), req.Volume, req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type shuffleRequest struct {
	State    bool   `json:"state"`
	DeviceId string `json:"device_id"`
}

func (s *server) handleShuffle(w http.ResponseWriter, r *http.Request) {
	var req shuffleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.result.SetShuffle(r.Context(), req.State, req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type repeatRequest struct {
	// State must be one of "off", "context", or "track".
	State    string `json:"state"`
	DeviceId string `json:"device_id"`
}

func (s *server) handleRepeat(w http.ResponseWriter, r *http.Request) {
	var req repeatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch req.State {
	case "off", "context", "track":
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf(`state must be "off", "context", or "track"`))
		return
	}
	if err := s.result.SetRepeat(r.Context(), req.State, req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type queueRequest struct {
	URI      string `json:"uri"`
	DeviceId string `json:"device_id"`
}

func (s *server) handleQueue(w http.ResponseWriter, r *http.Request) {
	var req queueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.URI == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("uri is required"))
		return
	}
	uri := req.URI
	if !strings.HasPrefix(uri, "spotify:") {
		uri = "spotify:track:" + uri
	}
	if err := s.result.AddToQueue(r.Context(), uri, req.DeviceId); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type transferRequest struct {
	DeviceId string `json:"device_id"`
	Play     bool   `json:"play"`
}

func (s *server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.DeviceId == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("device_id is required"))
		return
	}
	if err := s.result.TransferPlayback(r.Context(), req.DeviceId, req.Play); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

// ---------------------------------------------------------------------------
// Loading / playing content
// ---------------------------------------------------------------------------

type loadRequest struct {
	URIs           []string `json:"uris"`
	ContextURI     string   `json:"context_uri"`
	OffsetURI      string   `json:"offset_uri"`
	OffsetPosition *int     `json:"offset_position"`
	PositionMs     int64    `json:"position_ms"`
	DeviceId       string   `json:"device_id"`
}

// POST /load — load and play track(s) via the Web API, optionally within a
// context (album/playlist) and/or starting at a given offset/position.
// Equivalent to the CLI's "load" command.
func (s *server) handleLoad(w http.ResponseWriter, r *http.Request) {
	var req loadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.URIs) == 0 && req.ContextURI == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("uris or context_uri is required"))
		return
	}

	uris := make([]string, len(req.URIs))
	for i, u := range req.URIs {
		if !strings.HasPrefix(u, "spotify:") {
			u = "spotify:track:" + u
		}
		uris[i] = u
	}

	opts := &controller.LoadTrackOptions{
		DeviceId:       req.DeviceId,
		ContextURI:     req.ContextURI,
		OffsetURI:      req.OffsetURI,
		OffsetPosition: req.OffsetPosition,
		PositionMs:     req.PositionMs,
	}

	if err := s.result.LoadTrack(r.Context(), uris, opts); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type playTrackRequest struct {
	URIs        []string `json:"uris"`
	DeviceId    string   `json:"device_id"`
	Shuffle     bool     `json:"shuffle"`
	SkipToURI   string   `json:"skip_to_uri"`
	SkipToIndex *int     `json:"skip_to_index"`
}

// POST /play-track — play one or more tracks directly via connect-state, with
// no surrounding context/recommendations. Equivalent to the CLI's "playtrack"
// command.
func (s *server) handlePlayTrack(w http.ResponseWriter, r *http.Request) {
	var req playTrackRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.URIs) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("uris is required"))
		return
	}

	uris := make([]string, len(req.URIs))
	for i, u := range req.URIs {
		if !strings.HasPrefix(u, "spotify:") {
			u = "spotify:track:" + u
		}
		uris[i] = u
	}

	opts := &controller.PlayTrackOptions{
		DeviceId:    req.DeviceId,
		Shuffle:     req.Shuffle,
		SkipToURI:   req.SkipToURI,
		SkipToIndex: req.SkipToIndex,
	}

	if err := s.result.PlayTrack(r.Context(), uris, opts); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type playPlaylistRequest struct {
	PlaylistId     string `json:"playlist_id"`
	DeviceId       string `json:"device_id"`
	Shuffle        bool   `json:"shuffle"`
	SkipToTrackURI string `json:"skip_to_track_uri"`
	SkipToTrackUID string `json:"skip_to_track_uid"`
}

// POST /play-playlist — play a playlist by ID. Equivalent to the CLI's
// "playlist" command.
func (s *server) handlePlayPlaylist(w http.ResponseWriter, r *http.Request) {
	var req playPlaylistRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.PlaylistId == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("playlist_id is required"))
		return
	}
	playlistId := strings.TrimPrefix(req.PlaylistId, "spotify:playlist:")

	opts := &controller.PlayPlaylistOptions{
		DeviceId:       req.DeviceId,
		Shuffle:        req.Shuffle,
		SkipToTrackURI: req.SkipToTrackURI,
		SkipToTrackUID: req.SkipToTrackUID,
	}

	if err := s.result.Controller.PlayPlaylist(r.Context(), playlistId, opts); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w)
}

type openRequest struct {
	URL      string `json:"url"`
	DeviceId string `json:"device_id"`
}

// POST /open — play a Spotify open.spotify.com URL (track, playlist, or
// album). Equivalent to the CLI's "open" command.
func (s *server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req openRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("url is required"))
		return
	}

	entityType, entityId := parseSpotifyURL(req.URL)
	if entityType == "" || entityId == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"could not parse Spotify URL, expected https://open.spotify.com/{track,playlist,album}/<id>"))
		return
	}

	ctx := r.Context()

	switch entityType {
	case "track":
		trackURI := "spotify:track:" + entityId
		opts := &controller.PlayTrackOptions{DeviceId: req.DeviceId}
		if err := s.result.PlayTrack(ctx, []string{trackURI}, opts); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
	case "playlist":
		opts := &controller.PlayPlaylistOptions{DeviceId: req.DeviceId}
		if err := s.result.Controller.PlayPlaylist(ctx, entityId, opts); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
	case "album":
		albumURI := "spotify:album:" + entityId
		opts := &controller.LoadTrackOptions{ContextURI: albumURI, DeviceId: req.DeviceId}
		if err := s.result.LoadTrack(ctx, nil, opts); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported Spotify entity type: %s", entityType))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"entity_type": entityType,
		"entity_id":   entityId,
	})
}

// ---------------------------------------------------------------------------
// Live events (Server-Sent Events) — equivalent to the CLI's "watch" command
// ---------------------------------------------------------------------------

// sseEvent is the envelope written for every event sent down /events. Type is
// one of "device_list", "playback", or "metadata".
type sseEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// GET /events — streams device list, playback, and metadata changes as
// Server-Sent Events for as long as the client stays connected. Try it with:
//
//	curl -N http://localhost:8080/events
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	deviceCh := s.result.SubscribeDeviceList()
	playbackCh := s.result.SubscribePlayback()
	metadataCh := s.result.SubscribeMetadata()

	send := func(evt sseEvent) {
		b, err := json.Marshal(evt)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// Periodic heartbeat comment so intermediaries/proxies don't time out an
	// idle connection.
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()

		case evt, ok := <-deviceCh:
			if !ok {
				return
			}
			send(sseEvent{Type: "device_list", Data: evt})

		case evt, ok := <-playbackCh:
			if !ok {
				return
			}
			send(sseEvent{Type: "playback", Data: evt})

		case evt, ok := <-metadataCh:
			if !ok {
				return
			}
			send(sseEvent{Type: "metadata", Data: evt})
		}
	}
}
