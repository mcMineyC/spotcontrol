package spotcontrol

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	devicespb "github.com/mcMineyC/spotcontrol/proto/spotify/connectstate/devices"
)

// DeviceType is a type alias for the protobuf DeviceType enum. Using a type
// alias (=) rather than a new type means these constants are fully
// interchangeable with devicespb.DeviceType values — existing code that uses
// the protobuf constants directly continues to compile without changes.
type DeviceType = devicespb.DeviceType

// Device type constants re-exported from the protobuf package for convenience.
// Users can use these instead of importing the deeply nested protobuf package
// directly. For example:
//
//	spotcontrol.DeviceTypeComputer
//
// is equivalent to:
//
//	devicespb.DeviceType_COMPUTER
const (
	DeviceTypeComputer    = devicespb.DeviceType_COMPUTER
	DeviceTypeTablet      = devicespb.DeviceType_TABLET
	DeviceTypeSmartphone  = devicespb.DeviceType_SMARTPHONE
	DeviceTypeSpeaker     = devicespb.DeviceType_SPEAKER
	DeviceTypeTV          = devicespb.DeviceType_TV
	DeviceTypeAVR         = devicespb.DeviceType_AVR
	DeviceTypeSTB         = devicespb.DeviceType_STB
	DeviceTypeAudioDongle = devicespb.DeviceType_AUDIO_DONGLE
	DeviceTypeGameConsole = devicespb.DeviceType_GAME_CONSOLE
	DeviceTypeCastVideo   = devicespb.DeviceType_CAST_VIDEO
	DeviceTypeCastAudio   = devicespb.DeviceType_CAST_AUDIO
	DeviceTypeAutomobile  = devicespb.DeviceType_AUTOMOBILE
	DeviceTypeSmartwatch  = devicespb.DeviceType_SMARTWATCH
	DeviceTypeChromebook  = devicespb.DeviceType_CHROMEBOOK
	DeviceTypeCarThing    = devicespb.DeviceType_CAR_THING
)

// GenerateDeviceId generates a random 40-character hex string (20 random bytes)
// suitable for use as a Spotify device identifier. It uses crypto/rand for
// cryptographically secure random bytes.
//
// The returned string is guaranteed to be exactly 40 lowercase hex characters,
// which is the format required by session.Options.DeviceId.
func GenerateDeviceId() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("spotcontrol: failed generating device id: %v", err))
	}
	return hex.EncodeToString(b)
}
