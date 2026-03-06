package ap

import (
	"encoding/binary"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

// writeMessage marshals a protobuf message and writes it to w with a 4-byte
// big-endian length prefix. If withHello is true, the bytes 0x00, 0x04 are
// written before the length (this is required for the initial ClientHello).
func writeMessage(w io.Writer, withHello bool, m proto.Message) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed marshalling message: %w", err)
	}

	var helloLen int
	if withHello {
		if _, err := w.Write([]byte{0, 4}); err != nil {
			return fmt.Errorf("failed writing hello bytes: %w", err)
		}
		helloLen = 2
	}

	if err := binary.Write(w, binary.BigEndian, uint32(helloLen+4+len(data))); err != nil {
		return fmt.Errorf("failed writing message length: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed writing message: %w", err)
	}

	return nil
}

// readMessage reads a length-prefixed protobuf message from r. The first 4
// bytes are a big-endian uint32 that encodes the total frame length (including
// itself). The remaining bytes are unmarshalled into m.
//
// If maxLength > 0 the frame length is checked against it to avoid huge
// allocations from malformed data. Pass -1 to disable the check.
func readMessage(r io.Reader, maxLength int, m proto.Message) error {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return fmt.Errorf("failed reading message length: %w", err)
	}

	if maxLength > 0 && length > uint32(maxLength) {
		return fmt.Errorf("message too long: %d", length)
	}

	data := make([]byte, length-4)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("failed reading message body: %w", err)
	}

	if err := proto.Unmarshal(data, m); err != nil {
		return fmt.Errorf("failed unmarshalling message: %w", err)
	}

	return nil
}
