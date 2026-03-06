package mercury

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"

	spotcontrol "github.com/badfortrains/spotcontrol"
	"github.com/badfortrains/spotcontrol/ap"
	pb "github.com/badfortrains/spotcontrol/proto/spotify"
	"google.golang.org/protobuf/proto"
)

// Response represents a parsed Mercury response consisting of a header and
// zero or more payload parts.
type Response struct {
	HeaderData []byte
	Uri        string
	StatusCode int32
	Payload    [][]byte
}

// Request represents a Mercury request to be sent over the AP connection.
type Request struct {
	Method      string
	Uri         string
	ContentType string
	Payload     [][]byte
}

// pendingRequest tracks an in-flight Mercury request waiting for a response.
type pendingRequest struct {
	parts   [][]byte
	partial []byte
	count   int
	ch      chan Response
	isSub   bool
}

// Subscription represents an active Mercury subscription. Messages matching
// the subscription URI are delivered to the channel.
type Subscription struct {
	Uri string
	Ch  <-chan Response
	ch  chan Response
}

// Client manages Mercury (Hermes) request/response and pub/sub messaging over
// a Spotify access point connection. It is safe for concurrent use.
type Client struct {
	log spotcontrol.Logger
	ap  *ap.Accesspoint

	seq     atomic.Uint64
	pending map[uint64]*pendingRequest
	mu      sync.Mutex

	subs     map[string][]*Subscription
	subsLock sync.RWMutex

	recvCh <-chan ap.Packet
	stopCh chan struct{}
	once   sync.Once
}

// NewClient creates a new Mercury client that sends and receives packets over
// the given Accesspoint. The client registers to receive MercuryReq,
// MercurySub, MercuryUnsub, and MercuryEvent packet types from the AP.
func NewClient(log spotcontrol.Logger, accesspoint *ap.Accesspoint) *Client {
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	c := &Client{
		log:     log,
		ap:      accesspoint,
		pending: make(map[uint64]*pendingRequest),
		subs:    make(map[string][]*Subscription),
		stopCh:  make(chan struct{}),
	}

	c.recvCh = accesspoint.Receive(
		ap.PacketTypeMercuryReq,
		ap.PacketTypeMercurySub,
		ap.PacketTypeMercuryUnsub,
		ap.PacketTypeMercuryEvent,
	)

	go c.recvLoop()

	return c
}

// Close stops the Mercury client receive loop.
func (c *Client) Close() {
	c.once.Do(func() {
		close(c.stopCh)
	})
}

// Do sends a Mercury request and waits for the response. The context controls
// the timeout for waiting.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	seq := c.seq.Add(1) - 1

	ch := make(chan Response, 1)

	c.mu.Lock()
	c.pending[seq] = &pendingRequest{ch: ch}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
	}()

	if err := c.sendRequest(ctx, ap.PacketTypeMercuryReq, seq, req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		return &resp, nil
	case <-c.stopCh:
		return nil, fmt.Errorf("mercury client closed")
	}
}

// Subscribe creates a Mercury subscription for the given URI. Messages
// matching the URI will be delivered to the returned Subscription's channel.
func (c *Client) Subscribe(ctx context.Context, uri string) (*Subscription, error) {
	seq := c.seq.Add(1) - 1

	ch := make(chan Response, 1)

	c.mu.Lock()
	c.pending[seq] = &pendingRequest{ch: ch, isSub: true}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
	}()

	req := Request{
		Method: "SUB",
		Uri:    uri,
	}

	if err := c.sendRequest(ctx, ap.PacketTypeMercurySub, seq, req); err != nil {
		return nil, err
	}

	// Wait for confirmation.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("mercury subscribe failed with status %d", resp.StatusCode)
		}
	case <-c.stopCh:
		return nil, fmt.Errorf("mercury client closed")
	}

	sub := &Subscription{
		Uri: uri,
		ch:  make(chan Response, 16),
	}
	sub.Ch = sub.ch

	c.subsLock.Lock()
	c.subs[uri] = append(c.subs[uri], sub)
	c.subsLock.Unlock()

	return sub, nil
}

// Unsubscribe removes a Mercury subscription.
func (c *Client) Unsubscribe(ctx context.Context, uri string) error {
	seq := c.seq.Add(1) - 1

	ch := make(chan Response, 1)

	c.mu.Lock()
	c.pending[seq] = &pendingRequest{ch: ch}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
	}()

	req := Request{
		Method: "UNSUB",
		Uri:    uri,
	}

	if err := c.sendRequest(ctx, ap.PacketTypeMercuryUnsub, seq, req); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		// Remove subscriptions.
		c.subsLock.Lock()
		if subs, ok := c.subs[uri]; ok {
			for _, sub := range subs {
				close(sub.ch)
			}
			delete(c.subs, uri)
		}
		c.subsLock.Unlock()
		return nil
	case <-c.stopCh:
		return fmt.Errorf("mercury client closed")
	}
}

// sendRequest encodes a Mercury request into the AP packet format and sends it.
func (c *Client) sendRequest(ctx context.Context, pktType ap.PacketType, seq uint64, req Request) error {
	// Build Mercury header protobuf.
	header := &pb.MercuryHeader{
		Uri:         proto.String(req.Uri),
		Method:      proto.String(req.Method),
		ContentType: proto.String(req.ContentType),
	}

	headerBytes, err := proto.Marshal(header)
	if err != nil {
		return fmt.Errorf("failed marshalling mercury header: %w", err)
	}

	// The packet payload format:
	//   [2 bytes: sequence length]
	//   [sequence bytes (big-endian uint64)]
	//   [1 byte: flags]
	//   [2 bytes: part count (1 + len(payloads))]
	//   For each part:
	//     [2 bytes: part length]
	//     [part bytes]

	seqBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(seqBytes, seq)

	numParts := 1 + len(req.Payload)

	var buf bytes.Buffer

	// Sequence length (always 8).
	_ = binary.Write(&buf, binary.BigEndian, uint16(8))
	buf.Write(seqBytes)

	// Flags (always 1).
	buf.WriteByte(1)

	// Number of parts.
	_ = binary.Write(&buf, binary.BigEndian, uint16(numParts))

	// Header part.
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(headerBytes)))
	buf.Write(headerBytes)

	// Payload parts.
	for _, part := range req.Payload {
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(part)))
		buf.Write(part)
	}

	return c.ap.Send(ctx, pktType, buf.Bytes())
}

// recvLoop reads Mercury packets from the AP and dispatches them to pending
// requests or subscriptions.
func (c *Client) recvLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		case pkt, ok := <-c.recvCh:
			if !ok {
				return
			}
			c.handlePacket(pkt)
		}
	}
}

// handlePacket parses a received Mercury packet and dispatches the completed
// response.
func (c *Client) handlePacket(pkt ap.Packet) {
	reader := bytes.NewReader(pkt.Payload)

	// Read sequence length.
	var seqLen uint16
	if err := binary.Read(reader, binary.BigEndian, &seqLen); err != nil {
		c.log.WithError(err).Error("failed reading mercury seq length")
		return
	}

	seqData := make([]byte, seqLen)
	if _, err := reader.Read(seqData); err != nil {
		c.log.WithError(err).Error("failed reading mercury seq")
		return
	}

	// Parse sequence number.
	var seq uint64
	if seqLen == 8 {
		seq = binary.BigEndian.Uint64(seqData)
	} else if seqLen == 4 {
		seq = uint64(binary.BigEndian.Uint32(seqData))
	} else if seqLen == 2 {
		seq = uint64(binary.BigEndian.Uint16(seqData))
	}

	// Read flags.
	var flags byte
	if f, err := reader.ReadByte(); err != nil {
		c.log.WithError(err).Error("failed reading mercury flags")
		return
	} else {
		flags = f
	}
	_ = flags

	// Read part count.
	var numParts uint16
	if err := binary.Read(reader, binary.BigEndian, &numParts); err != nil {
		c.log.WithError(err).Error("failed reading mercury part count")
		return
	}

	// Read parts.
	parts := make([][]byte, 0, numParts)
	for i := 0; i < int(numParts); i++ {
		var partLen uint16
		if err := binary.Read(reader, binary.BigEndian, &partLen); err != nil {
			c.log.WithError(err).Error("failed reading mercury part length")
			return
		}

		part := make([]byte, partLen)
		if _, err := reader.Read(part); err != nil {
			c.log.WithError(err).Error("failed reading mercury part")
			return
		}

		parts = append(parts, part)
	}

	if len(parts) == 0 {
		c.log.Warn("mercury response with no parts")
		return
	}

	// Parse the header from the first part.
	var header pb.MercuryHeader
	if err := proto.Unmarshal(parts[0], &header); err != nil {
		c.log.WithError(err).Error("failed unmarshalling mercury header")
		return
	}

	resp := Response{
		HeaderData: parts[0],
		Uri:        header.GetUri(),
		StatusCode: header.GetStatusCode(),
		Payload:    parts[1:],
	}

	// Check if this is an event (push notification for subscriptions).
	if pkt.Type == ap.PacketTypeMercuryEvent {
		c.subsLock.RLock()
		subs, ok := c.subs[resp.Uri]
		if !ok {
			// Try prefix matching for wildcard subscriptions.
			for uri, s := range c.subs {
				if len(uri) > 0 && uri[len(uri)-1] == '*' {
					prefix := uri[:len(uri)-1]
					if len(resp.Uri) >= len(prefix) && resp.Uri[:len(prefix)] == prefix {
						subs = s
						ok = true
						break
					}
				}
			}
		}
		c.subsLock.RUnlock()

		if ok {
			for _, sub := range subs {
				select {
				case sub.ch <- resp:
				default:
					c.log.Warnf("dropping mercury event for %s: channel full", resp.Uri)
				}
			}
		} else {
			c.log.Debugf("no subscription for mercury event uri: %s", resp.Uri)
		}
		return
	}

	// Dispatch to pending request.
	c.mu.Lock()
	pending, ok := c.pending[seq]
	c.mu.Unlock()

	if ok && pending.ch != nil {
		select {
		case pending.ch <- resp:
		default:
		}
	} else {
		c.log.Debugf("no pending request for mercury seq %d, uri: %s", seq, resp.Uri)
	}
}
