package ap

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/devgianlu/shannon"
)

// shannonConn wraps a net.Conn with Shannon stream cipher encryption and
// decryption. Each direction has its own cipher instance and nonce counter.
// Send and receive operations are independently synchronized so that multiple
// goroutines can safely call sendPacket and receivePacket concurrently.
type shannonConn struct {
	conn net.Conn

	sendLock sync.Mutex
	recvLock sync.Mutex

	sendCipher *shannon.Shannon
	sendNonce  uint32

	recvCipher *shannon.Shannon
	recvNonce  uint32

	// unreadBytes stores bytes that were peeked (via peekUnencrypted) but not
	// yet consumed by receivePacket.
	unreadBytes []byte
}

// newShannonConn creates a new Shannon-encrypted connection using the provided
// send and receive keys derived from the Diffie-Hellman shared secret during
// the AP handshake.
func newShannonConn(conn net.Conn, sendKey []byte, recvKey []byte) *shannonConn {
	return &shannonConn{
		conn:       conn,
		sendCipher: shannon.New(sendKey),
		sendNonce:  0,
		recvCipher: shannon.New(recvKey),
		recvNonce:  0,
	}
}

// sendPacket encrypts and sends a single AP packet. The packet format is:
//
//	[1 byte type] [2 byte big-endian payload length] [payload bytes] [4 byte MAC]
//
// The entire header+payload is encrypted with the Shannon cipher and a 4-byte
// MAC is appended.
func (c *shannonConn) sendPacket(ctx context.Context, pktType PacketType, payload []byte) error {
	if len(payload) > 65535 {
		return fmt.Errorf("payload too big: %d", len(payload))
	}

	// Assemble the plaintext packet: type (1) + length (2) + payload.
	packet := make([]byte, 1+2+len(payload))
	packet[0] = byte(pktType)
	binary.BigEndian.PutUint16(packet[1:3], uint16(len(payload)))
	copy(packet[3:], payload)

	c.sendLock.Lock()
	defer c.sendLock.Unlock()

	// Set nonce on cipher and increment for next packet.
	c.sendCipher.NonceU32(c.sendNonce)
	c.sendNonce++

	// Encrypt the packet in place.
	c.sendCipher.Encrypt(packet)

	// Calculate 4-byte MAC.
	mac := make([]byte, 4)
	c.sendCipher.Finish(mac)

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	}

	// Write encrypted packet followed by MAC.
	if _, err := c.conn.Write(packet); err != nil {
		return fmt.Errorf("failed writing packet: %w", err)
	}
	if _, err := c.conn.Write(mac); err != nil {
		return fmt.Errorf("failed writing packet mac: %w", err)
	}

	return nil
}

// peekUnencrypted reads count raw (unencrypted) bytes from the connection and
// stores them so they will be consumed by the next receivePacket call. This is
// used after authentication to detect whether the server sent an unencrypted
// error response instead of the expected encrypted APWelcome.
func (c *shannonConn) peekUnencrypted(count int) ([]byte, error) {
	c.recvLock.Lock()
	defer c.recvLock.Unlock()

	if len(c.unreadBytes) > 0 {
		panic("peekUnencrypted called with existing unread bytes")
	}

	buf := make([]byte, count)
	if _, err := io.ReadFull(c.conn, buf); err != nil {
		return nil, fmt.Errorf("failed peeking unencrypted: %w", err)
	}

	c.unreadBytes = buf
	return buf, nil
}

// receivePacket reads and decrypts a single AP packet from the connection. It
// returns the packet type, the decrypted payload, and any error. The MAC is
// verified after decryption.
func (c *shannonConn) receivePacket(ctx context.Context) (PacketType, []byte, error) {
	c.recvLock.Lock()
	defer c.recvLock.Unlock()

	// If we have unread bytes from a previous peekUnencrypted call, prepend
	// them so they get consumed first.
	var unreadReader *bytes.Reader
	var reader io.Reader
	if len(c.unreadBytes) > 0 {
		unreadReader = bytes.NewReader(c.unreadBytes)
		reader = io.MultiReader(unreadReader, c.conn)

		defer func() { c.unreadBytes, _ = io.ReadAll(unreadReader) }()
	} else {
		reader = c.conn
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	}

	// Set nonce on cipher and increment for next packet.
	c.recvCipher.NonceU32(c.recvNonce)
	c.recvNonce++

	// Read the 3-byte encrypted header: [type (1)] [length (2)].
	packetHeader := make([]byte, 3)
	if _, err := io.ReadFull(reader, packetHeader); err != nil {
		return 0, nil, fmt.Errorf("failed reading packet header: %w", err)
	}

	// Decrypt header.
	c.recvCipher.Decrypt(packetHeader)

	// Extract payload length and read payload.
	payloadLen := binary.BigEndian.Uint16(packetHeader[1:3])
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, fmt.Errorf("failed reading packet payload: %w", err)
	}

	// Decrypt payload.
	c.recvCipher.Decrypt(payload)

	// Read and verify the 4-byte MAC.
	expectedMac := make([]byte, 4)
	if _, err := io.ReadFull(reader, expectedMac); err != nil {
		return 0, nil, fmt.Errorf("failed reading packet mac: %w", err)
	}

	if c.recvCipher.CheckMac(expectedMac) != nil {
		return 0, nil, fmt.Errorf("invalid packet mac")
	}

	return PacketType(packetHeader[0]), payload, nil
}
