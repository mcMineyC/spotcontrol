package ap

import (
	"bytes"
	"net"
)

// connAccumulator wraps a net.Conn and records all data read from and written
// to the underlying connection. The accumulated bytes are used during the AP
// key-exchange to compute the challenge HMAC.
type connAccumulator struct {
	net.Conn

	data bytes.Buffer
}

// Dump returns all bytes that have been read from or written to the connection
// since the accumulator was created.
func (c *connAccumulator) Dump() []byte {
	return c.data.Bytes()
}

// Read reads from the underlying connection and records the bytes read.
func (c *connAccumulator) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if err != nil {
		return n, err
	}

	_, _ = c.data.Write(b[:n])
	return n, err
}

// Write writes to the underlying connection and records the bytes written.
func (c *connAccumulator) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if err != nil {
		return n, err
	}

	_, _ = c.data.Write(b[:n])
	return n, err
}
