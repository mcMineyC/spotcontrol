package spotcontrol

import "encoding/hex"

// ClientId is the Spotify client ID used for authentication.
// This is the well-known client ID used by official Spotify clients.
var ClientId = []byte{0x65, 0xb7, 0x08, 0x07, 0x3f, 0xc0, 0x48, 0x0e, 0xa9, 0x2a, 0x07, 0x72, 0x33, 0xca, 0x87, 0xbd}

// ClientIdHex is the hex-encoded string representation of the client ID.
var ClientIdHex = hex.EncodeToString(ClientId)
