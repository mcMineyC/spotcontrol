package dealer

import (
	connectpb "github.com/badfortrains/spotcontrol/proto/spotify/connectstate"
)

// RawMessage is the JSON-framed message received over the dealer WebSocket.
type RawMessage struct {
	Type         string            `json:"type"`
	Method       string            `json:"method"`
	Uri          string            `json:"uri"`
	Headers      map[string]string `json:"headers"`
	MessageIdent string            `json:"message_ident"`
	Key          string            `json:"key"`
	Payloads     []interface{}     `json:"payloads"`
	Payload      struct {
		Compressed []byte `json:"compressed"`
	} `json:"payload"`
}

// Reply is the JSON message sent back to the dealer in response to a request.
type Reply struct {
	Type    string `json:"type"`
	Key     string `json:"key"`
	Payload struct {
		Success bool `json:"success"`
	} `json:"payload"`
}

// Message represents a decoded dealer push message with its URI, headers, and
// decoded payload bytes. Messages are delivered to receivers registered via
// Dealer.ReceiveMessage.
type Message struct {
	Uri     string
	Headers map[string]string
	Payload []byte
}

// Request represents a dealer request that expects a reply. Callers must call
// Reply(success) to send the response back to the dealer.
type Request struct {
	resp chan bool

	MessageIdent string
	Payload      RequestPayload
}

// Reply sends a success or failure response back to the dealer for this
// request.
func (req Request) Reply(success bool) {
	req.resp <- success
}

// RequestPayload is the JSON body of a dealer request message. It contains
// the command details used by the connect-state protocol to control playback.
type RequestPayload struct {
	MessageId      uint32 `json:"message_id"`
	SentByDeviceId string `json:"sent_by_device_id"`
	Command        struct {
		Endpoint         string                    `json:"endpoint"`
		SessionId        string                    `json:"session_id"`
		Data             []byte                    `json:"data"`
		Value            interface{}               `json:"value"`
		Position         int64                     `json:"position"`
		Relative         string                    `json:"relative"`
		Context          *connectpb.Context        `json:"context"`
		PlayOrigin       *connectpb.PlayOrigin     `json:"play_origin"`
		Track            *connectpb.ContextTrack   `json:"track"`
		PrevTracks       []*connectpb.ContextTrack `json:"prev_tracks"`
		NextTracks       []*connectpb.ContextTrack `json:"next_tracks"`
		RepeatingTrack   *bool                     `json:"repeating_track"`
		RepeatingContext *bool                     `json:"repeating_context"`
		ShufflingContext *bool                     `json:"shuffling_context"`
		LoggingParams    struct {
			CommandInitiatedTime int64    `json:"command_initiated_time"`
			PageInstanceIds      []string `json:"page_instance_ids"`
			InteractionIds       []string `json:"interaction_ids"`
			DeviceIdentifier     string   `json:"device_identifier"`
		} `json:"logging_params"`
		Options struct {
			RestorePaused       string `json:"restore_paused"`
			RestorePosition     string `json:"restore_position"`
			RestoreTrack        string `json:"restore_track"`
			AlwaysPlaySomething bool   `json:"always_play_something"`
			AllowSeeking        bool   `json:"allow_seeking"`
			SkipTo              struct {
				TrackUid   string `json:"track_uid"`
				TrackUri   string `json:"track_uri"`
				TrackIndex int    `json:"track_index"`
			} `json:"skip_to"`
			InitiallyPaused       bool                                    `json:"initially_paused"`
			SystemInitiated       bool                                    `json:"system_initiated"`
			PlayerOptionsOverride *connectpb.ContextPlayerOptionOverrides `json:"player_options_override"`
			Suppressions          *connectpb.Suppressions                 `json:"suppressions"`
			PrefetchLevel         string                                  `json:"prefetch_level"`
			AudioStream           string                                  `json:"audio_stream"`
			SessionId             string                                  `json:"session_id"`
			License               string                                  `json:"license"`
		} `json:"options"`
		PlayOptions struct {
			OverrideRestrictions bool   `json:"override_restrictions"`
			OnlyForLocalDevice   bool   `json:"only_for_local_device"`
			SystemInitiated      bool   `json:"system_initiated"`
			Reason               string `json:"reason"`
			Operation            string `json:"operation"`
			Trigger              string `json:"trigger"`
		} `json:"play_options"`
		FromDeviceIdentifier string `json:"from_device_identifier"`
	} `json:"command"`
}
