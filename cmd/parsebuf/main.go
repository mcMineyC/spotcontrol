// Command parsebuf decodes mitmproxy-captured HTTP request bodies from Spotify's
// connect-state player command endpoint:
//
//	POST /connect-state/v1/player/command/from/{fromDevice}/to/{toDevice}
//
// The captured .buf files are mitmproxy's "URL Encoded" view of a binary body
// that is actually gzip-compressed JSON. mitmproxy renders the binary data using
// Python-style \xNN escape sequences mixed with printable ASCII, splitting across
// lines with "? " prefixes (form values) and ": " prefixes (form keys).
//
// This tool attempts multiple decoding strategies:
//  1. Parse the mitmproxy text format, reassemble bytes, gzip-decompress, parse JSON
//  2. If gzip fails, try treating reassembled bytes as raw JSON or protobuf
//  3. If text parsing fails, try treating the file as raw binary (gzip or proto)
//
// Usage:
//
//	go run ./cmd/parsebuf play.buf pause.buf
//	go run ./cmd/parsebuf -raw capture.bin       # treat as raw gzipped binary
//	go run ./cmd/parsebuf -proto capture.bin     # treat as raw protobuf
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

var (
	flagRaw   = flag.Bool("raw", false, "treat input as raw binary (gzipped) instead of mitmproxy text")
	flagProto = flag.Bool("proto", false, "treat input as raw protobuf (no gzip)")
	flagHex   = flag.Bool("hex", false, "also print hex dump of reassembled bytes")
)

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintf(os.Stderr, "Usage: parsebuf [flags] file1.buf [file2.buf ...]\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	for _, path := range flag.Args() {
		fmt.Printf("=== %s ===\n", path)
		if err := processFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR processing %s: %v\n", path, err)
		}
		fmt.Println()
	}
}

func processFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	if *flagProto {
		fmt.Println("[mode: raw protobuf]")
		dumpProtobufFields(data, 0)
		return nil
	}

	if *flagRaw {
		fmt.Println("[mode: raw binary]")
		return processRawBinary(data)
	}

	// Detect if the file is likely mitmproxy text (ASCII with \x escapes)
	// vs actual binary data.
	if isMitmproxyText(data) {
		fmt.Println("[detected: mitmproxy URL-encoded text dump]")
		return processMitmproxyText(data)
	}

	// Fall back to raw binary
	fmt.Println("[detected: binary file]")
	return processRawBinary(data)
}

// isMitmproxyText returns true if the data looks like mitmproxy's text rendering
// of URL-encoded form data (ASCII text with \xNN escape sequences).
func isMitmproxyText(data []byte) bool {
	// Quick heuristic: mitmproxy text starts with "? " and contains "\x" escape sequences
	s := string(data)
	return strings.HasPrefix(s, "? ") && strings.Contains(s, `\x`)
}

// processMitmproxyText parses the mitmproxy URL-encoded view text format.
//
// Format observed:
//
//	? <value bytes with \xNN escapes>      ← form field value
//	  <continuation of value>              ← indented continuation lines
//	: <key>                                 ← form field key (often '' for empty)
//
// Lines may wrap in the middle of \xNN escape sequences (the "\" at end of
// one line, "xNN" at the start of the next). Some lines start with a quote
// character (') as a continuation from the previous line's value.
//
// The mitmproxy view can also show form key values that contain binary data,
// in both single-escaped (\xNN) and double-escaped (\\xNN) forms.
func processMitmproxyText(data []byte) error {
	text := string(data)
	lines := strings.Split(text, "\n")

	// Phase 1: Reassemble all data sections.
	// We collect "sections" which are form field value+key pairs.
	// Each section's bytes represent part of the HTTP body.
	type formField struct {
		value string
		key   string
	}

	var fields []formField
	var currentValue strings.Builder
	var currentKey string
	inValue := false

	flushField := func() {
		if inValue || currentValue.Len() > 0 {
			fields = append(fields, formField{
				value: currentValue.String(),
				key:   currentKey,
			})
			currentValue.Reset()
			currentKey = ""
			inValue = false
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")

		if strings.HasPrefix(trimmed, "? ") {
			// Start of a new form field value
			if inValue {
				// We hit a new "?" before seeing ":", which means the previous
				// value is followed by another value. The key between them
				// may have been on a ": " line we'll see shortly.
				// Actually in the observed format, "? " always starts a new value.
				// Flush what we have.
				flushField()
			}
			currentValue.WriteString(trimmed[2:])
			inValue = true
		} else if strings.HasPrefix(trimmed, ": ") {
			// Form field key
			keyPart := trimmed[2:]
			// Check if this is the empty key marker
			if keyPart == "''" {
				// Empty key — end of this field
				flushField()
			} else if strings.HasPrefix(keyPart, `"`) && strings.HasSuffix(keyPart, `"`) {
				// Quoted key value — this is binary data in the key
				currentKey = keyPart
				flushField()
			} else {
				// Key contains data (possibly binary-escaped) — this happens
				// when mitmproxy wraps data into the key field.
				// Append to the current value since it's all part of the body.
				currentValue.WriteString(keyPart)
			}
		} else if strings.HasPrefix(trimmed, "  ") {
			// Indented continuation line
			currentValue.WriteString(strings.TrimLeft(trimmed, " "))
		} else if strings.HasPrefix(trimmed, "'") {
			// Quote-prefixed continuation (from line wrapping inside a quoted string)
			currentValue.WriteString(trimmed[1:])
		} else if trimmed == "" {
			// Empty line — skip
			continue
		} else {
			// Any other line — likely a continuation from a split escape sequence
			currentValue.WriteString(trimmed)
		}
	}
	flushField()

	fmt.Printf("Found %d form field(s)\n", len(fields))

	// Phase 2: For each field, try to decode the escaped bytes.
	// Then concatenate all decoded fields to form the complete body.
	var allBytes []byte

	for i, f := range fields {
		// Merge value and key data. In the mitmproxy x-www-form-urlencoded view,
		// the body is split as key=value pairs. For binary bodies, the split
		// between key and value is arbitrary. We concatenate them all.

		src := f.value
		valueBytes, err := decodeMitmproxyEscapes(src)
		if err != nil {
			fmt.Printf("  Field %d value: decode error: %v\n", i, err)
			fmt.Printf("    Raw text (%d chars): %.100s...\n", len(src), src)
		} else {
			fmt.Printf("  Field %d value: %d bytes\n", i, len(valueBytes))
		}
		allBytes = append(allBytes, valueBytes...)

		if f.key != "" && f.key != "''" {
			// The key also contains data
			keySrc := f.key
			// Strip surrounding quotes if present
			keySrc = strings.TrimPrefix(keySrc, `"`)
			keySrc = strings.TrimSuffix(keySrc, `"`)

			// Key may use double-escaped sequences (\\xNN) from mitmproxy's
			// rendering of a bytes-inside-string value.
			keyBytes, err := decodeMitmproxyEscapes(keySrc)
			if err != nil {
				fmt.Printf("  Field %d key: decode error: %v\n", i, err)
			} else {
				fmt.Printf("  Field %d key: %d bytes\n", i, len(keyBytes))
			}
			allBytes = append(allBytes, keyBytes...)
		}
	}

	fmt.Printf("Total reassembled: %d bytes\n", len(allBytes))

	if *flagHex {
		fmt.Println("Hex dump of reassembled bytes:")
		fmt.Println(hex.Dump(allBytes))
	}

	return processRawBinary(allBytes)
}

// decodeMitmproxyEscapes decodes a string containing mitmproxy's rendering
// of binary data: a mix of printable ASCII characters and \xNN hex escape
// sequences, with \\\\ for literal backslashes and \t, \n, \r for control chars.
//
// It also handles double-escaped sequences like \\\\xNN (which mitmproxy uses
// when rendering bytes inside a quoted string representation).
func decodeMitmproxyEscapes(s string) ([]byte, error) {
	var buf bytes.Buffer
	i := 0
	for i < len(s) {
		if s[i] == '\\' {
			// Look ahead to determine escape type
			if i+1 < len(s) {
				switch s[i+1] {
				case 'x':
					// \xNN — two hex digits
					if i+3 < len(s) {
						hexStr := s[i+2 : i+4]
						b, err := strconv.ParseUint(hexStr, 16, 8)
						if err != nil {
							// Not a valid hex escape; emit the backslash literally
							buf.WriteByte('\\')
							i++
							continue
						}
						buf.WriteByte(byte(b))
						i += 4
						continue
					}
					// Incomplete \x at end of string — partial escape from line wrapping.
					// This shouldn't happen after proper line joining, but handle gracefully.
					buf.WriteByte('\\')
					i++
				case '\\':
					// \\\\ could be:
					// 1. A literal backslash (\\)
					// 2. Start of a double-escaped sequence (\\xNN)
					if i+2 < len(s) && s[i+2] == 'x' && i+5 < len(s) {
						// \\xNN — double-escaped hex byte
						hexStr := s[i+3 : i+5]
						b, err := strconv.ParseUint(hexStr, 16, 8)
						if err != nil {
							// Not valid; treat as literal backslash
							buf.WriteByte('\\')
							i += 2
							continue
						}
						buf.WriteByte(byte(b))
						i += 5
						continue
					}
					// Just a literal backslash
					buf.WriteByte('\\')
					i += 2
				case 'n':
					buf.WriteByte('\n')
					i += 2
				case 'r':
					buf.WriteByte('\r')
					i += 2
				case 't':
					buf.WriteByte('\t')
					i += 2
				case '\'':
					buf.WriteByte('\'')
					i += 2
				case '"':
					buf.WriteByte('"')
					i += 2
				default:
					buf.WriteByte('\\')
					i++
				}
			} else {
				// Trailing backslash at end of string
				buf.WriteByte('\\')
				i++
			}
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.Bytes(), nil
}

// processRawBinary attempts to interpret raw bytes as:
// 1. Gzip-compressed data → decompress then parse contents
// 2. Raw JSON
// 3. Raw protobuf
func processRawBinary(data []byte) error {
	if len(data) == 0 {
		fmt.Println("(empty)")
		return nil
	}

	fmt.Printf("First bytes: %s\n", hex.EncodeToString(data[:min(len(data), 20)]))

	// Check for gzip magic number
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		fmt.Println("Detected gzip magic number (1f 8b)")
		decompressed, err := gzipDecompress(data)
		if err != nil {
			fmt.Printf("Gzip decompression failed: %v\n", err)
			fmt.Println("This is expected when the data was copy-pasted from mitmproxy's text view,")
			fmt.Println("which corrupts binary data through character encoding and line wrapping.")
			fmt.Println()
			fmt.Println("To properly capture the data, use mitmproxy's export feature:")
			fmt.Println("  1. In mitmproxy, select the flow")
			fmt.Println("  2. Press 'e' to export, or use: mitmdump -w dumpfile")
			fmt.Println("  3. Or use 'Export' > 'curl command' and replay with curl --output")
			fmt.Println()
			fmt.Println("Attempting partial analysis of the corrupted data...")
			tryPartialGzip(data)
			return nil
		}
		fmt.Printf("Gzip decompressed: %d bytes → %d bytes\n", len(data), len(decompressed))
		return analyzePayload(decompressed)
	}

	// No gzip — try direct parsing
	return analyzePayload(data)
}

func gzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer r.Close()
	return io.ReadAll(r)
}

// tryPartialGzip attempts to decompress what it can from a potentially
// corrupted gzip stream and reports any partial results.
func tryPartialGzip(data []byte) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		fmt.Printf("  Cannot even open gzip reader: %v\n", err)
		return
	}
	defer r.Close()

	buf := make([]byte, 4096)
	var partial []byte
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			partial = append(partial, buf[:n]...)
		}
		if readErr != nil {
			break
		}
	}

	if len(partial) > 0 {
		fmt.Printf("  Partial decompression yielded %d bytes:\n", len(partial))
		fmt.Printf("  Hex: %s\n", hex.EncodeToString(partial[:min(len(partial), 100)]))
		if isPrintable(partial) {
			fmt.Printf("  Text: %s\n", string(partial))
		}
		analyzePayload(partial) //nolint:errcheck
	} else {
		fmt.Println("  No bytes could be decompressed.")
	}
}

// analyzePayload tries to interpret decompressed/raw payload data.
func analyzePayload(data []byte) error {
	// Try JSON first
	if len(data) > 0 && (data[0] == '{' || data[0] == '[') {
		if tryJSON(data) {
			return nil
		}
	}

	// Try as protobuf
	if tryProtobuf(data) {
		return nil
	}

	// Print as hex/text
	if isPrintable(data) {
		fmt.Println("Payload (text):")
		fmt.Println(string(data))
	} else {
		fmt.Println("Payload (hex):")
		fmt.Println(hex.Dump(data))
	}
	return nil
}

// tryJSON attempts to parse and pretty-print JSON data.
// It also interprets the Spotify connect-state command structure.
func tryJSON(data []byte) bool {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}

	fmt.Println("Successfully parsed as JSON!")
	fmt.Println()

	// Pretty-print the full JSON
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err == nil {
		fmt.Println("Full JSON:")
		fmt.Println(pretty.String())
	}

	// Try to interpret as a connect-state player command (Request envelope)
	var req struct {
		MessageId      uint32 `json:"message_id"`
		SentByDeviceId string `json:"sent_by_device_id"`
		Command        struct {
			Endpoint      string          `json:"endpoint"`
			Value         json.RawMessage `json:"value,omitempty"`
			Position      json.RawMessage `json:"position,omitempty"`
			Context       json.RawMessage `json:"context,omitempty"`
			PlayOrigin    json.RawMessage `json:"play_origin,omitempty"`
			Options       json.RawMessage `json:"options,omitempty"`
			LoggingParams struct {
				InteractionIds   []string `json:"interaction_ids,omitempty"`
				DeviceIdentifier string   `json:"device_identifier,omitempty"`
				CommandInitTime  *int64   `json:"command_initiated_time,omitempty"`
				PageInstanceIds  []string `json:"page_instance_ids,omitempty"`
				CommandId        string   `json:"command_id,omitempty"`
			} `json:"logging_params"`
			Track      json.RawMessage `json:"track,omitempty"`
			NextTracks json.RawMessage `json:"next_tracks,omitempty"`
			PrevTracks json.RawMessage `json:"prev_tracks,omitempty"`
		} `json:"command"`
	}

	if err := json.Unmarshal(data, &req); err == nil && req.Command.Endpoint != "" {
		fmt.Println()
		fmt.Println("=== Interpreted as Connect-State Player Command ===")
		fmt.Printf("  Message ID:        %d\n", req.MessageId)
		fmt.Printf("  Sent by device:    %s\n", req.SentByDeviceId)
		fmt.Printf("  Command endpoint:  %s\n", req.Command.Endpoint)

		if len(req.Command.Value) > 0 {
			fmt.Printf("  Value:             %s\n", string(req.Command.Value))
		}
		if len(req.Command.Position) > 0 {
			fmt.Printf("  Position:          %s\n", string(req.Command.Position))
		}
		if req.Command.LoggingParams.DeviceIdentifier != "" {
			fmt.Printf("  Device identifier: %s\n", req.Command.LoggingParams.DeviceIdentifier)
		}
		if req.Command.LoggingParams.CommandInitTime != nil {
			fmt.Printf("  Command initiated: %d\n", *req.Command.LoggingParams.CommandInitTime)
		}
		if req.Command.LoggingParams.CommandId != "" {
			fmt.Printf("  Command ID:        %s\n", req.Command.LoggingParams.CommandId)
		}
		if len(req.Command.LoggingParams.InteractionIds) > 0 {
			fmt.Printf("  Interaction IDs:   %v\n", req.Command.LoggingParams.InteractionIds)
		}
		if len(req.Command.LoggingParams.PageInstanceIds) > 0 {
			fmt.Printf("  Page instance IDs: %v\n", req.Command.LoggingParams.PageInstanceIds)
		}

		if len(req.Command.Context) > 0 && string(req.Command.Context) != "null" {
			fmt.Println("  Context:")
			var ctxPretty bytes.Buffer
			json.Indent(&ctxPretty, req.Command.Context, "    ", "  ")
			fmt.Printf("    %s\n", ctxPretty.String())
		}
		if len(req.Command.PlayOrigin) > 0 && string(req.Command.PlayOrigin) != "null" {
			fmt.Println("  Play origin:")
			var poPretty bytes.Buffer
			json.Indent(&poPretty, req.Command.PlayOrigin, "    ", "  ")
			fmt.Printf("    %s\n", poPretty.String())
		}
		if len(req.Command.Options) > 0 && string(req.Command.Options) != "null" {
			fmt.Println("  Options:")
			var optPretty bytes.Buffer
			json.Indent(&optPretty, req.Command.Options, "    ", "  ")
			fmt.Printf("    %s\n", optPretty.String())
		}

		// Map endpoint to the expected implementation
		fmt.Println()
		fmt.Println("=== Implementation Notes ===")
		describeEndpoint(req.Command.Endpoint)
	}

	return true
}

func describeEndpoint(endpoint string) {
	switch endpoint {
	case "resume":
		fmt.Println("  This is a RESUME (play) command.")
		fmt.Println("  The receiving device should resume playback of the current track.")
		fmt.Println("  Equivalent to Web API: PUT /v1/me/player/play")
	case "pause":
		fmt.Println("  This is a PAUSE command.")
		fmt.Println("  The receiving device should pause playback.")
		fmt.Println("  Equivalent to Web API: PUT /v1/me/player/pause")
	case "play":
		fmt.Println("  This is a PLAY command with context.")
		fmt.Println("  The receiving device should start playing the specified context/track.")
		fmt.Println("  Includes context URI, play_origin, and options (skip_to, etc).")
	case "skip_next":
		fmt.Println("  This is a SKIP NEXT command.")
		fmt.Println("  The receiving device should skip to the next track.")
		fmt.Println("  Equivalent to Web API: POST /v1/me/player/next")
	case "skip_prev":
		fmt.Println("  This is a SKIP PREVIOUS command.")
		fmt.Println("  The receiving device should skip to the previous track (or seek to 0).")
		fmt.Println("  Equivalent to Web API: POST /v1/me/player/previous")
	case "seek_to":
		fmt.Println("  This is a SEEK command.")
		fmt.Println("  The value field contains the target position in milliseconds.")
		fmt.Println("  Equivalent to Web API: PUT /v1/me/player/seek?position_ms=VALUE")
	case "set_shuffling_context":
		fmt.Println("  This is a SET SHUFFLE command.")
		fmt.Println("  The value field is a boolean (true/false).")
		fmt.Println("  Equivalent to Web API: PUT /v1/me/player/shuffle?state=VALUE")
	case "set_repeating_context":
		fmt.Println("  This is a SET REPEAT CONTEXT command.")
		fmt.Println("  The value field is a boolean. true=repeat context, false=off.")
	case "set_repeating_track":
		fmt.Println("  This is a SET REPEAT TRACK command.")
		fmt.Println("  The value field is a boolean. true=repeat current track.")
	case "set_options":
		fmt.Println("  This is a SET OPTIONS command.")
		fmt.Println("  Can set shuffle, repeat context, and repeat track simultaneously.")
	case "add_to_queue":
		fmt.Println("  This is an ADD TO QUEUE command.")
		fmt.Println("  The track field contains the track to add.")
	case "set_queue":
		fmt.Println("  This is a SET QUEUE command.")
		fmt.Println("  Contains next_tracks and prev_tracks arrays.")
	case "transfer":
		fmt.Println("  This is a TRANSFER command.")
		fmt.Println("  Transfers playback state to the receiving device.")
	case "update_context":
		fmt.Println("  This is an UPDATE CONTEXT command.")
		fmt.Println("  Updates the playback context without changing the current track.")
	default:
		fmt.Printf("  Unknown endpoint: %s\n", endpoint)
	}

	fmt.Println()
	fmt.Println("  The sending endpoint should be:")
	fmt.Println("    POST /connect-state/v1/player/command/from/{fromDevice}/to/{toDevice}")
	fmt.Println("  With headers:")
	fmt.Println("    Content-Type: application/json")
	fmt.Println("    Content-Encoding: gzip  (optional, but the desktop client uses it)")
	fmt.Println("    X-Spotify-Connection-Id: <dealer connection ID>")
}

// tryProtobuf attempts to decode and display data as a protobuf message.
func tryProtobuf(data []byte) bool {
	// Validate that the data looks like valid protobuf by trying to consume fields.
	if !looksLikeProtobuf(data) {
		return false
	}

	fmt.Println("Attempting protobuf decode (unknown schema):")
	dumpProtobufFields(data, 0)
	return true
}

// looksLikeProtobuf does a quick check that data could be valid protobuf
// by trying to consume at least one field.
func looksLikeProtobuf(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	remaining := data
	fieldsFound := 0
	for len(remaining) > 0 && fieldsFound < 5 {
		_, _, n := protowire.ConsumeField(remaining)
		if n < 0 {
			break
		}
		remaining = remaining[n:]
		fieldsFound++
	}
	// Consider it protobuf if we found at least 1 field and consumed a reasonable portion
	return fieldsFound > 0 && (len(remaining) < len(data)/2 || fieldsFound >= 2)
}

// dumpProtobufFields recursively dumps protobuf wire-format fields.
func dumpProtobufFields(data []byte, depth int) {
	indent := strings.Repeat("  ", depth)
	remaining := data

	for len(remaining) > 0 {
		num, wtype, n := protowire.ConsumeTag(remaining)
		if n < 0 {
			if len(remaining) > 0 {
				fmt.Printf("%s[remaining %d unparsed bytes]\n", indent, len(remaining))
			}
			return
		}
		remaining = remaining[n:]

		switch wtype {
		case protowire.VarintType:
			val, n := protowire.ConsumeVarint(remaining)
			if n < 0 {
				fmt.Printf("%sfield %d: varint (corrupt)\n", indent, num)
				return
			}
			remaining = remaining[n:]
			// Show as both unsigned, signed (zigzag), and bool
			zigzag := protowire.DecodeZigZag(val)
			if val <= 1 {
				fmt.Printf("%sfield %d: varint = %d (bool: %v)\n", indent, num, val, val == 1)
			} else {
				fmt.Printf("%sfield %d: varint = %d (signed: %d)\n", indent, num, val, zigzag)
			}

		case protowire.Fixed32Type:
			val, n := protowire.ConsumeFixed32(remaining)
			if n < 0 {
				fmt.Printf("%sfield %d: fixed32 (corrupt)\n", indent, num)
				return
			}
			remaining = remaining[n:]
			fmt.Printf("%sfield %d: fixed32 = %d (0x%08x)\n", indent, num, val, val)

		case protowire.Fixed64Type:
			val, n := protowire.ConsumeFixed64(remaining)
			if n < 0 {
				fmt.Printf("%sfield %d: fixed64 (corrupt)\n", indent, num)
				return
			}
			remaining = remaining[n:]
			fmt.Printf("%sfield %d: fixed64 = %d (0x%016x)\n", indent, num, val, val)

		case protowire.BytesType:
			val, n := protowire.ConsumeBytes(remaining)
			if n < 0 {
				fmt.Printf("%sfield %d: bytes (corrupt)\n", indent, num)
				return
			}
			remaining = remaining[n:]

			if isPrintable(val) && len(val) > 0 {
				fmt.Printf("%sfield %d: string = %q\n", indent, num, string(val))
			} else if looksLikeProtobuf(val) {
				fmt.Printf("%sfield %d: embedded message (%d bytes):\n", indent, num, len(val))
				dumpProtobufFields(val, depth+1)
			} else {
				fmt.Printf("%sfield %d: bytes (%d bytes) = %s\n", indent, num, len(val), hex.EncodeToString(val[:min(len(val), 64)]))
				if len(val) > 64 {
					fmt.Printf("%s  ... (%d more bytes)\n", indent, len(val)-64)
				}
			}

		case protowire.StartGroupType:
			fmt.Printf("%sfield %d: start group\n", indent, num)
			// Groups are deprecated but we should at least not crash
		case protowire.EndGroupType:
			fmt.Printf("%sfield %d: end group\n", indent, num)
		default:
			fmt.Printf("%sfield %d: unknown wire type %d\n", indent, num, wtype)
			return
		}
	}
}

func isPrintable(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	for _, b := range data {
		if b < 0x20 || b > 0x7e {
			if b != '\n' && b != '\r' && b != '\t' {
				return false
			}
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
