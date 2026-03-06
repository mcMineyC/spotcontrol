package dealer

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/coder/websocket"

	spotcontrol "github.com/mcMineyC/spotcontrol"
)

const (
	pingInterval = 30 * time.Second
	timeout      = 10 * time.Second
)

// Dealer manages a WebSocket connection to a Spotify dealer endpoint. It
// handles automatic ping/pong keep-alive, reconnection with exponential
// back-off, and dispatches incoming messages and requests to registered
// receivers.
//
// Dealer is safe for concurrent use.
type Dealer struct {
	log spotcontrol.Logger

	client *http.Client

	addr        spotcontrol.GetAddressFunc
	accessToken spotcontrol.GetLogin5TokenFunc

	conn *websocket.Conn

	stop           bool
	pingTickerStop chan struct{}
	recvLoopStop   chan struct{}
	recvLoopOnce   sync.Once
	lastPong       time.Time
	lastPongLock   sync.Mutex

	// spotConnId is the Spotify-Connection-Id header from the WebSocket
	// upgrade response. It is needed for PutConnectState requests.
	spotConnId     string
	spotConnIdLock sync.RWMutex

	// connMu is held for writing when performing reconnection and for reading
	// when accessing the conn. If it's not held, a valid connection is
	// available. Be careful not to deadlock anything with this.
	connMu sync.RWMutex

	messageReceivers     []messageReceiver
	messageReceiversLock sync.RWMutex

	requestReceivers     map[string]requestReceiver
	requestReceiversLock sync.RWMutex
}

type messageReceiver struct {
	uriPrefixes []string
	c           chan Message
}

type requestReceiver struct {
	c chan Request
}

// NewDealer creates a new Dealer. The dealerAddr function provides the dealer
// WebSocket address, and accessToken provides the Login5 bearer token used in
// the WebSocket URL query parameter.
func NewDealer(
	log spotcontrol.Logger,
	client *http.Client,
	dealerAddr spotcontrol.GetAddressFunc,
	accessToken spotcontrol.GetLogin5TokenFunc,
) *Dealer {
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &Dealer{
		client: &http.Client{
			Transport:     client.Transport,
			CheckRedirect: client.CheckRedirect,
			Jar:           client.Jar,
			Timeout:       timeout,
		},
		log:              log,
		addr:             dealerAddr,
		accessToken:      accessToken,
		requestReceivers: map[string]requestReceiver{},
	}
}

// Connect opens the WebSocket connection to the dealer. If a connection is
// already open (and not stopped), this is a no-op.
func (d *Dealer) Connect(ctx context.Context) error {
	d.connMu.Lock()
	defer d.connMu.Unlock()

	if d.conn != nil && !d.stop {
		d.log.Debugf("dealer connection already opened")
		return nil
	}

	return d.connect(ctx)
}

func (d *Dealer) connect(ctx context.Context) error {
	d.recvLoopStop = make(chan struct{}, 1)
	d.pingTickerStop = make(chan struct{}, 1)
	d.stop = false

	accessToken, err := d.accessToken(ctx, false)
	if err != nil {
		return fmt.Errorf("failed obtaining dealer access token: %w", err)
	}

	conn, wsResp, err := websocket.Dial(
		ctx,
		fmt.Sprintf("wss://%s/?access_token=%s", d.addr(ctx), accessToken),
		&websocket.DialOptions{
			HTTPClient: d.client,
			HTTPHeader: http.Header{
				"User-Agent": []string{spotcontrol.UserAgent()},
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed dialing dealer websocket: %w", err)
	}

	// Capture the Spotify-Connection-Id from the WebSocket upgrade response.
	// This header is required for PutConnectState requests to the spclient.
	if wsResp != nil {
		if connId := wsResp.Header.Get("Spotify-Connection-Id"); connId != "" {
			d.spotConnIdLock.Lock()
			d.spotConnId = connId
			d.spotConnIdLock.Unlock()
			d.log.Debugf("captured Spotify-Connection-Id from dealer handshake (%d bytes)", len(connId))
		} else {
			d.log.Debugf("dealer WebSocket response did not contain Spotify-Connection-Id header (will arrive as dealer message)")
		}
	}

	// We assign to d.conn after because if Dial fails we'll have a nil d.conn
	// which we don't want.
	d.conn = conn

	// Remove the read limit.
	d.conn.SetReadLimit(math.MaxInt64)

	d.log.Debugf("dealer connection opened")
	return nil
}

// ConnectionId returns the Spotify-Connection-Id obtained from the most recent
// dealer WebSocket handshake. This is needed for PutConnectState requests.
// Returns an empty string if no connection ID has been captured yet.
func (d *Dealer) ConnectionId() string {
	d.spotConnIdLock.RLock()
	defer d.spotConnIdLock.RUnlock()
	return d.spotConnId
}

// Close terminates the dealer connection and stops all background goroutines.
// All receiver channels will be closed.
func (d *Dealer) Close() {
	d.connMu.Lock()
	defer d.connMu.Unlock()

	d.stop = true

	if d.conn == nil {
		return
	}

	select {
	case d.recvLoopStop <- struct{}{}:
	default:
	}
	select {
	case d.pingTickerStop <- struct{}{}:
	default:
	}
	_ = d.conn.Close(websocket.StatusGoingAway, "")
}

func (d *Dealer) startReceiving() {
	d.recvLoopOnce.Do(func() {
		d.log.Tracef("starting dealer recv loop")
		go d.recvLoop()

		// Set last pong in the future so we don't immediately timeout.
		d.lastPong = time.Now().Add(pingInterval)
		go d.pingTicker()
	})
}

func (d *Dealer) pingTicker() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-d.pingTickerStop:
			break loop
		case <-ticker.C:
			d.lastPongLock.Lock()
			timePassed := time.Since(d.lastPong)
			d.lastPongLock.Unlock()

			if timePassed > pingInterval+timeout {
				d.log.Errorf("did not receive last pong from dealer, %.0fs passed", timePassed.Seconds())

				// Closing the connection should make the read on the
				// "recvLoop" fail; continue hoping for a new connection.
				d.connMu.RLock()
				_ = d.conn.Close(websocket.StatusServiceRestart, "")
				d.connMu.RUnlock()
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			d.connMu.RLock()
			err := d.conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`))
			d.connMu.RUnlock()
			cancel()
			d.log.Tracef("sent dealer ping")

			if err != nil {
				if d.stop {
					break loop
				}

				d.log.WithError(err).Warnf("failed sending dealer ping")

				d.connMu.RLock()
				_ = d.conn.Close(websocket.StatusServiceRestart, "")
				d.connMu.RUnlock()
				continue
			}
		}
	}
}

func (d *Dealer) recvLoop() {
loop:
	for {
		select {
		case <-d.recvLoopStop:
			break loop
		default:
			// No need to hold connMu since reconnection happens in this
			// routine.
			msgType, messageBytes, err := d.conn.Read(context.Background())

			// Don't log closed error if we're stopping.
			if d.stop && websocket.CloseStatus(err) == websocket.StatusGoingAway {
				d.log.Debugf("dealer connection closed")
				break loop
			} else if err != nil {
				d.log.WithError(err).Errorf("failed receiving dealer message")
				break loop
			} else if msgType != websocket.MessageText {
				d.log.Warnf("unsupported message type: %v, len: %d", msgType, len(messageBytes))
				continue
			}

			var message RawMessage
			if err := json.Unmarshal(messageBytes, &message); err != nil {
				d.log.WithError(err).Error("failed unmarshalling dealer message")
				break loop
			}

			switch message.Type {
			case "message":
				d.handleMessage(&message)
			case "request":
				d.handleRequest(&message)
			case "ping":
				// We never receive ping messages from the server in practice.
			case "pong":
				d.lastPongLock.Lock()
				d.lastPong = time.Now()
				d.lastPongLock.Unlock()
				d.log.Tracef("received dealer pong")
			default:
				d.log.Warnf("unknown dealer message type: %s", message.Type)
			}
		}
	}

	// Always close as we might end up here because of application errors.
	_ = d.conn.Close(websocket.StatusInternalError, "")

	// If we shouldn't stop, try to reconnect.
	if !d.stop {
		d.connMu.Lock()
		if err := backoff.Retry(d.reconnect, backoff.NewExponentialBackOff()); err != nil {
			d.log.WithError(err).Errorf("failed reconnecting dealer")
			d.connMu.Unlock()

			// Something went very wrong, give up.
			d.Close()
			return
		}
		d.connMu.Unlock()

		// Reconnection was successful, do not close receivers.
		return
	}

	// Close all receiver channels.
	d.requestReceiversLock.RLock()
	for _, recv := range d.requestReceivers {
		close(recv.c)
	}
	d.requestReceiversLock.RUnlock()

	d.messageReceiversLock.RLock()
	for _, recv := range d.messageReceivers {
		close(recv.c)
	}
	d.messageReceiversLock.RUnlock()

	d.log.Debugf("dealer recv loop stopped")
}

func (d *Dealer) reconnect() error {
	if err := d.connect(context.TODO()); err != nil {
		return err
	}

	d.lastPongLock.Lock()
	d.lastPong = time.Now()
	d.lastPongLock.Unlock()

	// Restart the recv loop.
	go d.recvLoop()

	d.log.Debugf("re-established dealer connection")
	return nil
}

func (d *Dealer) sendReply(key string, success bool) error {
	reply := Reply{Type: "reply", Key: key}
	reply.Payload.Success = success

	replyBytes, err := json.Marshal(reply)
	if err != nil {
		return fmt.Errorf("failed marshalling reply: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	d.connMu.RLock()
	err = d.conn.Write(ctx, websocket.MessageText, replyBytes)
	d.connMu.RUnlock()
	cancel()
	if err != nil {
		return fmt.Errorf("failed sending dealer reply: %w", err)
	}

	return nil
}

// handleTransferEncoding decompresses the payload if the Transfer-Encoding
// header indicates gzip compression.
func handleTransferEncoding(headers map[string]string, data []byte) ([]byte, error) {
	if transEnc, ok := headers["Transfer-Encoding"]; ok {
		switch transEnc {
		case "gzip":
			gz, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("invalid gzip stream: %w", err)
			}

			defer func() { _ = gz.Close() }()

			data, err = io.ReadAll(gz)
			if err != nil {
				return nil, fmt.Errorf("failed decompressing gzip payload: %w", err)
			}
		default:
			return nil, fmt.Errorf("unsupported transfer encoding: %s", transEnc)
		}

		delete(headers, "Transfer-Encoding")
	}

	return data, nil
}

func (d *Dealer) handleMessage(rawMsg *RawMessage) {
	log := d.log.WithField("uri", rawMsg.Uri)

	if len(rawMsg.Payloads) > 1 {
		log.Warnf("unsupported number of payloads: %d", len(rawMsg.Payloads))
		return
	}

	var matchedReceivers []messageReceiver

	// Lookup receivers that want to match this message.
	d.messageReceiversLock.RLock()
	for _, recv := range d.messageReceivers {
		for _, uriPrefix := range recv.uriPrefixes {
			if strings.HasPrefix(rawMsg.Uri, uriPrefix) {
				matchedReceivers = append(matchedReceivers, recv)
				break
			}
		}
	}
	d.messageReceiversLock.RUnlock()

	if len(matchedReceivers) == 0 {
		log.Debugf("skipping dealer message: uri=%s, headers=%v, payloads=%d", rawMsg.Uri, rawMsg.Headers, len(rawMsg.Payloads))
		return
	}

	var payloadBytes []byte
	if len(rawMsg.Payloads) > 0 {
		var err error
		switch payload := rawMsg.Payloads[0].(type) {
		case string:
			payloadBytes, err = base64.StdEncoding.DecodeString(payload)
			if err != nil {
				log.WithError(err).Error("invalid base64 payload")
				return
			}
		case []byte:
			payloadBytes = payload
		default:
			log.Warnf("unsupported payload format: %s", reflect.TypeOf(rawMsg.Payloads[0]))
			return
		}

		payloadBytes, err = handleTransferEncoding(rawMsg.Headers, payloadBytes)
		if err != nil {
			log.WithError(err).Errorf("failed decoding message transfer encoding")
			return
		}
	}

	msg := Message{
		Uri:     rawMsg.Uri,
		Headers: rawMsg.Headers,
		Payload: payloadBytes,
	}

	for _, recv := range matchedReceivers {
		recv.c <- msg
	}
}

// ReceiveMessage registers a receiver for dealer messages whose URI matches
// any of the given prefixes. Returns a channel on which matching messages
// will be delivered. The receive loop is started automatically on the first
// call to ReceiveMessage or ReceiveRequest.
//
// The returned channel is closed when the dealer is closed.
func (d *Dealer) ReceiveMessage(uriPrefixes ...string) <-chan Message {
	if len(uriPrefixes) == 0 {
		panic("uri prefixes list cannot be empty")
	}

	d.messageReceiversLock.Lock()
	defer d.messageReceiversLock.Unlock()

	c := make(chan Message)
	d.messageReceivers = append(d.messageReceivers, messageReceiver{uriPrefixes, c})

	// Start receiving if necessary.
	d.startReceiving()

	return c
}

func (d *Dealer) handleRequest(rawMsg *RawMessage) {
	log := d.log.WithField("uri", rawMsg.MessageIdent)

	d.requestReceiversLock.RLock()
	recv, ok := d.requestReceivers[rawMsg.MessageIdent]
	d.requestReceiversLock.RUnlock()

	if !ok {
		log.Warn("ignoring dealer request")
		return
	}

	payloadBytes, err := handleTransferEncoding(rawMsg.Headers, rawMsg.Payload.Compressed)
	if err != nil {
		log.WithError(err).Errorf("failed decoding request transfer encoding")
		return
	}

	var payload RequestPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		log.WithError(err).Error("failed unmarshalling dealer request payload")
		return
	}

	// Dispatch request and wait for response.
	resp := make(chan bool, 1)
	recv.c <- Request{
		resp:         resp,
		MessageIdent: rawMsg.MessageIdent,
		Payload:      payload,
	}

	// Wait for response and send it.
	success := <-resp
	if err := d.sendReply(rawMsg.Key, success); err != nil {
		log.WithError(err).Error("failed sending dealer reply")
		return
	}
}

// ReceiveRequest registers a receiver for dealer requests matching the given
// URI. Only one receiver per URI is allowed; registering a second receiver for
// the same URI will panic.
//
// The returned channel delivers Request values. Each Request must be replied to
// by calling Request.Reply(success).
//
// The returned channel is closed when the dealer is closed.
func (d *Dealer) ReceiveRequest(uri string) <-chan Request {
	d.requestReceiversLock.Lock()
	defer d.requestReceiversLock.Unlock()

	if _, ok := d.requestReceivers[uri]; ok {
		panic(fmt.Sprintf("cannot have more request receivers for %s", uri))
	}

	c := make(chan Request)
	d.requestReceivers[uri] = requestReceiver{c}

	// Start receiving if necessary.
	d.startReceiving()

	return c
}
