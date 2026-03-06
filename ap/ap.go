package ap

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"golang.org/x/crypto/pbkdf2"
	"google.golang.org/protobuf/proto"

	spotcontrol "github.com/badfortrains/spotcontrol"
	"github.com/badfortrains/spotcontrol/dh"
	pb "github.com/badfortrains/spotcontrol/proto/spotify"
)

const pongAckInterval = 120 * time.Second

// AccesspointLoginError is returned when the AP rejects the login credentials.
type AccesspointLoginError struct {
	Message *pb.APLoginFailed
}

func (e *AccesspointLoginError) Error() string {
	desc := ""
	if e.Message.ErrorDescription != nil {
		desc = " " + *e.Message.ErrorDescription
	}
	return fmt.Sprintf("accesspoint login failed: %s%s", e.Message.ErrorCode.String(), desc)
}

// Accesspoint manages the TCP connection to a Spotify access point, including
// key exchange, Shannon-cipher encrypted communication, authentication,
// automatic reconnection, and packet dispatching to registered receivers.
type Accesspoint struct {
	log spotcontrol.Logger

	addr spotcontrol.GetAddressFunc

	nonce    []byte
	deviceId string

	dh *dh.DiffieHellman

	conn    net.Conn
	encConn *shannonConn

	stop              bool
	pongAckTickerStop chan struct{}
	recvLoopStop      chan struct{}
	recvLoopOnce      sync.Once
	recvChans         map[PacketType][]chan Packet
	recvChansLock     sync.RWMutex
	lastPongAck       time.Time
	lastPongAckLock   sync.Mutex

	// connMu is held for writing when performing reconnection and for reading
	// mainly when accessing welcome or sending packets. If it's not held, a
	// valid connection (and APWelcome) is available.
	connMu  sync.RWMutex
	welcome *pb.APWelcome
}

// NewAccesspoint creates a new Accesspoint that will dial using the given
// address function and identify itself with the given device ID.
func NewAccesspoint(log spotcontrol.Logger, addr spotcontrol.GetAddressFunc, deviceId string) *Accesspoint {
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}
	return &Accesspoint{
		log:       log,
		addr:      addr,
		deviceId:  deviceId,
		recvChans: make(map[PacketType][]chan Packet),
	}
}

// init prepares a fresh connection attempt: generates a new nonce, new DH
// parameters, and opens a TCP connection to the next available access point.
func (ap *Accesspoint) init(ctx context.Context) error {
	// Generate 16-byte nonce.
	ap.nonce = make([]byte, 16)
	if _, err := rand.Read(ap.nonce); err != nil {
		return fmt.Errorf("failed reading random nonce: %w", err)
	}

	// Generate new DH key pair.
	var err error
	if ap.dh, err = dh.NewDiffieHellman(); err != nil {
		return fmt.Errorf("failed initializing diffiehellman: %w", err)
	}

	// Close previous connection if any.
	if ap.conn != nil {
		_ = ap.conn.Close()
		ap.conn = nil
	}

	// Try several access points before giving up.
	attempts := 0
	for {
		attempts++
		ctx_, cancel := context.WithTimeout(ctx, 30*time.Second)
		addr := ap.addr(ctx_)

		var d net.Dialer
		conn, dialErr := d.DialContext(ctx_, "tcp", addr)
		cancel()

		if dialErr == nil {
			ap.conn = conn
			ap.log.Debugf("connected to %s", addr)
			return nil
		}

		if attempts >= 6 {
			return fmt.Errorf("failed to connect to AP %v: %w", addr, dialErr)
		}
		ap.log.WithError(dialErr).Warnf("failed to connect to AP %v, retrying with a different AP", addr)
	}
}

// ConnectSpotifyToken authenticates with a Spotify OAuth access token.
func (ap *Accesspoint) ConnectSpotifyToken(ctx context.Context, username, token string) error {
	return ap.Connect(ctx, &pb.LoginCredentials{
		Typ:      pb.AuthenticationType_AUTHENTICATION_SPOTIFY_TOKEN.Enum(),
		Username: proto.String(username),
		AuthData: []byte(token),
	})
}

// ConnectStored authenticates with a stored credential (reusable auth data
// from a previous APWelcome).
func (ap *Accesspoint) ConnectStored(ctx context.Context, username string, data []byte) error {
	return ap.Connect(ctx, &pb.LoginCredentials{
		Typ:      pb.AuthenticationType_AUTHENTICATION_STORED_SPOTIFY_CREDENTIALS.Enum(),
		Username: proto.String(username),
		AuthData: data,
	})
}

// ConnectBlob authenticates with an encrypted discovery blob (base64-encoded).
func (ap *Accesspoint) ConnectBlob(ctx context.Context, username string, encryptedBlob64 []byte) error {
	encryptedBlob := make([]byte, base64.StdEncoding.DecodedLen(len(encryptedBlob64)))
	written, err := base64.StdEncoding.Decode(encryptedBlob, encryptedBlob64)
	if err != nil {
		return fmt.Errorf("failed decoding encrypted blob: %w", err)
	}
	encryptedBlob = encryptedBlob[:written]

	secret := sha1.Sum([]byte(ap.deviceId))
	baseKey := pbkdf2.Key(secret[:], []byte(username), 256, 20, sha1.New)

	key := make([]byte, 24)
	copy(key, func() []byte { sum := sha1.Sum(baseKey); return sum[:] }())
	binary.BigEndian.PutUint32(key[20:], 20)

	bc, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed initializing AES cipher: %w", err)
	}

	decryptedBlob := make([]byte, len(encryptedBlob))
	for i := 0; i < len(encryptedBlob)-1; i += aes.BlockSize {
		bc.Decrypt(decryptedBlob[i:], encryptedBlob[i:])
	}

	for i := 0; i < len(decryptedBlob)-16; i++ {
		decryptedBlob[len(decryptedBlob)-i-1] ^= decryptedBlob[len(decryptedBlob)-i-17]
	}

	blob := bytes.NewReader(decryptedBlob)

	// Discard first byte.
	_, _ = blob.Seek(1, io.SeekCurrent)

	// Discard username-length bytes.
	discardLen, _ := binary.ReadUvarint(blob)
	_, _ = blob.Seek(int64(discardLen), io.SeekCurrent)

	// Discard separator byte.
	_, _ = blob.Seek(1, io.SeekCurrent)

	// Read authentication type.
	authTyp, _ := binary.ReadUvarint(blob)

	// Discard separator byte.
	_, _ = blob.Seek(1, io.SeekCurrent)

	// Read auth data.
	authDataLen, _ := binary.ReadUvarint(blob)
	authData := make([]byte, authDataLen)
	_, _ = blob.Read(authData)

	return ap.Connect(ctx, &pb.LoginCredentials{
		Typ:      pb.AuthenticationType(authTyp).Enum(),
		Username: proto.String(username),
		AuthData: authData,
	})
}

// Connect performs the full connection flow: TCP dial, key exchange, challenge
// solving, and authentication. It retries with back-off on transient failures.
func (ap *Accesspoint) Connect(ctx context.Context, creds *pb.LoginCredentials) error {
	ap.connMu.Lock()
	defer ap.connMu.Unlock()

	return backoff.Retry(func() error {
		err := ap.connect(ctx, creds)
		if err != nil {
			// If it's a login error, don't retry.
			if _, ok := err.(*AccesspointLoginError); ok {
				return backoff.Permanent(err)
			}
			ap.log.WithError(err).Warnf("failed connecting to accesspoint, retrying")
		}
		return err
	}, backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(500*time.Millisecond), 5), ctx))
}

func (ap *Accesspoint) connect(ctx context.Context, creds *pb.LoginCredentials) error {
	ap.recvLoopStop = make(chan struct{}, 1)
	ap.pongAckTickerStop = make(chan struct{}, 1)

	if err := ap.init(ctx); err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = ap.conn.SetDeadline(deadline)
		defer func() { _ = ap.conn.SetDeadline(time.Time{}) }()
	}

	// Perform key exchange with Diffie-Hellman.
	exchangeData, err := ap.performKeyExchange()
	if err != nil {
		return fmt.Errorf("failed performing keyexchange: %w", err)
	}

	// Solve challenge and complete connection.
	if err := ap.solveChallenge(exchangeData); err != nil {
		return fmt.Errorf("failed solving challenge: %w", err)
	}

	// Authenticate with credentials.
	if err := ap.authenticate(ctx, creds); err != nil {
		return fmt.Errorf("failed authenticating: %w", err)
	}

	return nil
}

// Close terminates the AP connection and stops all background goroutines.
func (ap *Accesspoint) Close() {
	ap.connMu.Lock()
	defer ap.connMu.Unlock()

	ap.stop = true

	if ap.conn == nil {
		return
	}

	select {
	case ap.recvLoopStop <- struct{}{}:
	default:
	}
	select {
	case ap.pongAckTickerStop <- struct{}{}:
	default:
	}
	_ = ap.conn.Close()
}

// Send sends an encrypted packet to the access point.
func (ap *Accesspoint) Send(ctx context.Context, pktType PacketType, payload []byte) error {
	ap.connMu.RLock()
	defer ap.connMu.RUnlock()
	return ap.encConn.sendPacket(ctx, pktType, payload)
}

// Receive registers one or more packet types for reception and returns a
// channel that will receive matching packets. The receive loop is started
// automatically on the first call.
func (ap *Accesspoint) Receive(types ...PacketType) <-chan Packet {
	ch := make(chan Packet)
	ap.recvChansLock.Lock()
	for _, t := range types {
		ll, _ := ap.recvChans[t]
		ll = append(ll, ch)
		ap.recvChans[t] = ll
	}
	ap.recvChansLock.Unlock()

	// Start the recv loop if necessary.
	ap.startReceiving()

	return ch
}

func (ap *Accesspoint) startReceiving() {
	ap.recvLoopOnce.Do(func() {
		ap.log.Tracef("starting accesspoint recv loop")
		go ap.recvLoop()

		// Set last pong ack in the future so we don't immediately timeout.
		ap.lastPongAck = time.Now().Add(pongAckInterval)
		go ap.pongAckTicker()
	})
}

func (ap *Accesspoint) recvLoop() {
loop:
	for {
		select {
		case <-ap.recvLoopStop:
			break loop
		default:
			// No need to hold connMu since reconnection happens in this routine.
			pkt, payload, err := ap.encConn.receivePacket(context.TODO())
			if err != nil {
				if !ap.stop {
					ap.log.WithError(err).Errorf("failed receiving packet")
				}
				break loop
			}

			switch pkt {
			case PacketTypePing:
				ap.log.Tracef("received accesspoint ping")
				if err := ap.encConn.sendPacket(context.TODO(), PacketTypePong, payload); err != nil {
					ap.log.WithError(err).Errorf("failed sending Pong packet")
					break loop
				}
			case PacketTypePongAck:
				ap.log.Tracef("received accesspoint pong ack")
				ap.lastPongAckLock.Lock()
				ap.lastPongAck = time.Now()
				ap.lastPongAckLock.Unlock()
				continue
			default:
				ap.recvChansLock.RLock()
				ll, _ := ap.recvChans[pkt]
				ap.recvChansLock.RUnlock()

				handled := false
				for _, ch := range ll {
					ch <- Packet{Type: pkt, Payload: payload}
					handled = true
				}

				if !handled {
					ap.log.Debugf("skipping packet %v, len: %d", pkt, len(payload))
				}
			}
		}
	}

	// Always close as we might end up here because of application errors.
	_ = ap.conn.Close()

	// If we shouldn't stop, try to reconnect.
	if !ap.stop {
		ap.connMu.Lock()
		if err := backoff.Retry(ap.reconnect, backoff.NewExponentialBackOff()); err != nil {
			ap.log.WithError(err).Errorf("failed reconnecting accesspoint")
			ap.connMu.Unlock()

			// Something went very wrong, give up.
			ap.Close()
			return
		}
		ap.connMu.Unlock()

		// Reconnection was successful, do not close receivers.
		return
	}

	ap.recvChansLock.RLock()
	defer ap.recvChansLock.RUnlock()

	var closedChannels []chan Packet
	for _, ll := range ap.recvChans {
		for _, ch := range ll {
			alreadyClosed := false
			for _, cc := range closedChannels {
				if cc == ch {
					alreadyClosed = true
					break
				}
			}
			if !alreadyClosed {
				closedChannels = append(closedChannels, ch)
				close(ch)
			}
		}
	}
}

func (ap *Accesspoint) pongAckTicker() {
	ticker := time.NewTicker(pongAckInterval)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-ap.pongAckTickerStop:
			break loop
		case <-ticker.C:
			ap.lastPongAckLock.Lock()
			timePassed := time.Since(ap.lastPongAck)
			ap.lastPongAckLock.Unlock()

			if timePassed > pongAckInterval {
				ap.log.Errorf("did not receive last pong ack from accesspoint, %.0fs passed", timePassed.Seconds())

				// Closing the connection should make the read on the
				// "recvLoop" fail; continue hoping for a new connection.
				_ = ap.conn.Close()
				continue
			}
		}
	}
}

func (ap *Accesspoint) reconnect() error {
	if ap.welcome == nil {
		return backoff.Permanent(fmt.Errorf("cannot reconnect without APWelcome"))
	}

	if err := ap.connect(context.TODO(), &pb.LoginCredentials{
		Typ:      ap.welcome.ReusableAuthCredentialsType,
		Username: ap.welcome.CanonicalUsername,
		AuthData: ap.welcome.ReusableAuthCredentials,
	}); err != nil {
		return err
	}

	// If we are here the recvLoop has already died, restart it.
	go ap.recvLoop()

	ap.log.Debugf("re-established accesspoint connection")
	return nil
}

func (ap *Accesspoint) performKeyExchange() ([]byte, error) {
	// Accumulate transferred data for challenge.
	cc := &connAccumulator{Conn: ap.conn}

	var productFlags []pb.ProductFlags
	if spotcontrol.VersionNumberString() == "dev" {
		productFlags = []pb.ProductFlags{pb.ProductFlags_PRODUCT_FLAG_DEV_BUILD}
	} else {
		productFlags = []pb.ProductFlags{pb.ProductFlags_PRODUCT_FLAG_NONE}
	}

	// Send ClientHello message.
	if err := writeMessage(cc, true, &pb.ClientHello{
		BuildInfo: &pb.BuildInfo{
			Product:      pb.Product_PRODUCT_CLIENT.Enum(),
			ProductFlags: productFlags,
			Platform:     spotcontrol.GetPlatform().Enum(),
			Version:      proto.Uint64(spotcontrol.SpotifyVersionCode),
		},
		CryptosuitesSupported: []pb.Cryptosuite{pb.Cryptosuite_CRYPTO_SUITE_SHANNON},
		ClientNonce:           ap.nonce,
		Padding:               []byte{0x1e},
		LoginCryptoHello: &pb.LoginCryptoHelloUnion{
			DiffieHellman: &pb.LoginCryptoDiffieHellmanHello{
				Gc:              ap.dh.PublicKeyBytes(),
				ServerKeysKnown: proto.Uint32(1),
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed writing ClientHello message: %w", err)
	}

	// Receive APResponseMessage.
	var apResponse pb.APResponseMessage
	if err := readMessage(cc, -1, &apResponse); err != nil {
		return nil, fmt.Errorf("failed reading APResponseMessage: %w", err)
	}

	challenge := apResponse.Challenge
	if challenge == nil || challenge.LoginCryptoChallenge == nil ||
		challenge.LoginCryptoChallenge.DiffieHellman == nil {
		if apResponse.LoginFailed != nil {
			return nil, &AccesspointLoginError{Message: apResponse.LoginFailed}
		}
		return nil, fmt.Errorf("missing DH challenge in AP response")
	}

	dhChallenge := challenge.LoginCryptoChallenge.DiffieHellman

	// Verify server signature.
	if !verifySignature(dhChallenge.Gs, dhChallenge.GsSignature) {
		return nil, fmt.Errorf("failed verifying server signature")
	}

	// Exchange keys and compute shared secret.
	ap.dh.Exchange(dhChallenge.Gs)

	ap.log.Debugf("completed keyexchange")
	return cc.Dump(), nil
}

func (ap *Accesspoint) solveChallenge(exchangeData []byte) error {
	macData := make([]byte, 0, sha1.Size*5)

	mac := hmac.New(sha1.New, ap.dh.SharedSecretBytes())
	for i := byte(1); i < 6; i++ {
		mac.Reset()
		mac.Write(exchangeData)
		mac.Write([]byte{i})
		macData = mac.Sum(macData)
	}

	mac = hmac.New(sha1.New, macData[:20])
	mac.Write(exchangeData)

	if err := writeMessage(ap.conn, false, &pb.ClientResponsePlaintext{
		PowResponse:    &pb.PoWResponseUnion{},
		CryptoResponse: &pb.CryptoResponseUnion{},
		LoginCryptoResponse: &pb.LoginCryptoResponseUnion{
			DiffieHellman: &pb.LoginCryptoDiffieHellmanResponse{
				Hmac: mac.Sum(nil),
			},
		},
	}); err != nil {
		return fmt.Errorf("failed writing ClientResponsePlaintext: %w", err)
	}

	// We are not sure if the challenge is actually completed — we check in
	// authenticate by reading the first encrypted packet.
	ap.encConn = newShannonConn(ap.conn, macData[20:52], macData[52:84])
	ap.log.Debug("completed challenge")
	return nil
}

func (ap *Accesspoint) authenticate(ctx context.Context, credentials *pb.LoginCredentials) error {
	if ap.encConn == nil {
		panic("accesspoint not connected")
	}

	// Assemble ClientResponseEncrypted message.
	payload, err := proto.Marshal(&pb.ClientResponseEncrypted{
		LoginCredentials: credentials,
		VersionString:    proto.String(spotcontrol.VersionString()),
		SystemInfo: &pb.SystemInfo{
			Os:                      spotcontrol.GetOS().Enum(),
			CpuFamily:               spotcontrol.GetCpuFamily().Enum(),
			SystemInformationString: proto.String(spotcontrol.SystemInfoString()),
			DeviceId:                proto.String(ap.deviceId),
		},
	})
	if err != nil {
		return fmt.Errorf("failed marshalling ClientResponseEncrypted: %w", err)
	}

	// Send Login packet.
	if err := ap.encConn.sendPacket(ctx, PacketTypeLogin, payload); err != nil {
		return fmt.Errorf("failed sending Login packet: %w", err)
	}

	// Check if we received an unencrypted APResponseMessage from the
	// challenge (this would indicate the handshake failed).
	var challengeResp pb.APResponseMessage
	if peekBytes, err := ap.encConn.peekUnencrypted(9); err != nil {
		return fmt.Errorf("failed peeking unencrypted bytes: %w", err)
	} else if err = readMessage(bytes.NewReader(peekBytes), 9, &challengeResp); err == nil {
		return &AccesspointLoginError{Message: challengeResp.LoginFailed}
	}

	// Receive APWelcome or AuthFailure.
	recvPkt, recvPayload, err := ap.encConn.receivePacket(ctx)
	if err != nil {
		return fmt.Errorf("failed receiving Login response packet: %w", err)
	}

	if recvPkt == PacketTypeAPWelcome {
		var welcome pb.APWelcome
		if err := proto.Unmarshal(recvPayload, &welcome); err != nil {
			return fmt.Errorf("failed unmarshalling APWelcome: %w", err)
		}

		ap.welcome = &welcome
		ap.log.WithField("username", spotcontrol.ObfuscateUsername(*welcome.CanonicalUsername)).
			Infof("authenticated AP")

		return nil
	} else if recvPkt == PacketTypeAuthFailure {
		var loginFailed pb.APLoginFailed
		if err := proto.Unmarshal(recvPayload, &loginFailed); err != nil {
			return fmt.Errorf("failed unmarshalling APLoginFailed: %w", err)
		}

		return &AccesspointLoginError{Message: &loginFailed}
	}

	return fmt.Errorf("unexpected command after Login packet: 0x%02x", byte(recvPkt))
}

// Username returns the canonical username from the most recent APWelcome. It
// panics if the access point has not been authenticated yet.
func (ap *Accesspoint) Username() string {
	ap.connMu.RLock()
	defer ap.connMu.RUnlock()

	if ap.welcome == nil {
		panic("accesspoint not authenticated")
	}

	return *ap.welcome.CanonicalUsername
}

// StoredCredentials returns the reusable auth credentials from the most recent
// APWelcome. These can be persisted and used to re-authenticate later without
// the original password. It panics if the access point has not been
// authenticated yet.
func (ap *Accesspoint) StoredCredentials() []byte {
	ap.connMu.RLock()
	defer ap.connMu.RUnlock()

	if ap.welcome == nil {
		panic("accesspoint not authenticated")
	}

	return ap.welcome.ReusableAuthCredentials
}

// Welcome returns the full APWelcome protobuf from the most recent
// authentication. Returns nil if not yet authenticated.
func (ap *Accesspoint) Welcome() *pb.APWelcome {
	ap.connMu.RLock()
	defer ap.connMu.RUnlock()
	return ap.welcome
}
