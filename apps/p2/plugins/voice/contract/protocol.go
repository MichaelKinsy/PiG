package contract

import (
	"regexp"
	"strconv"

	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/voice/protocol.ts: voice mode's wire shapes, shared by the server routes and the browser.

// MaxSpeakText is the longest text one speech request synthesizes: the browser sends a sentence or two at a time.
const MaxSpeakText = 2000

var id = str(schema.Pattern("^[A-Za-z0-9_-]{1,64}$"))

// LanguageSchema is a language: the service's lowercase ISO 639-1 code ("en", "de"); a BCP 47 tag is accepted too ("pt-BR").
var LanguageSchema = str(schema.Pattern("^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$"))

// TtsEngineSchema is a text-to-speech engine of the speech service (`GET /voices` there); voice IDs are per engine.
var TtsEngineSchema = obj(schema.Properties{
	prop("id", id),
	prop("name", str()),
	// It sends audio as it is synthesized (rather than a sentence at a time).
	prop("streaming", schema.Bool()),
	prop("sampleRate", num()),
	prop("voices", arr(obj(schema.Properties{prop("id", id), prop("name", str())}))),
	// The languages it speaks, by their own names ("Deutsch"); absent from a service without languages.
	opt("languages", arr(obj(schema.Properties{prop("id", LanguageSchema), prop("name", str())}))),
	opt("defaultLanguage", LanguageSchema),
})

// VoicesSchema is the speech service's `GET /voices`.
var VoicesSchema = obj(schema.Properties{
	prop("engines", arr(TtsEngineSchema)),
	prop("default", obj(schema.Properties{prop("engine", id), prop("voice", id), opt("language", LanguageSchema)}, schema.Open())),
}, schema.Open())

// The room's pause before an utterance ends, in seconds.
const (
	MinPause     = 0.5
	MaxPause     = 2
	DefaultPause = 0.8
)

// RoomPersonalSchema is what everyone may send to the room: `reset` (its own microphone's VAD) and `vad` (its own pause
// before an utterance ends, the "Pause before sending" setting).
var RoomPersonalSchema = union(
	obj(schema.Properties{prop("type", lit("reset"))}, schema.Open()),
	obj(schema.Properties{prop("type", lit("vad")), prop("silence", num(schema.Minimum(MinPause), schema.Maximum(MaxPause)))}, schema.Open()),
)

// RoomCommandSchema is what a browser may send as text (`WS /voice/room?session=…&conversation=…`, bridged to the service's
// `/room`): everyone `reset` and `vad`; the owner also the room's voice (`config`), what Pi says (`speak`, with a stable ID:
// repeats from other tabs are the service's duplicates) and `cancel`. Microphone audio goes up as binary 16 kHz PCM16.
var RoomCommandSchema = union(
	RoomPersonalSchema,
	obj(schema.Properties{
		prop("type", lit("config")), opt("engine", id), opt("voice", id),
		opt("speed", num(schema.Minimum(0.5), schema.Maximum(2))), opt("language", LanguageSchema),
	}, schema.Open()),
	obj(schema.Properties{prop("type", lit("speak")), prop("id", id), prop("text", str(schema.MinLength(1), schema.MaxLength(MaxSpeakText)))}, schema.Open()),
	obj(schema.Properties{prop("type", lit("cancel")), opt("id", id)}, schema.Open()),
)

// Binary frames of the room: agent speech (ID = utterance, rate from `tts_start`), or a person's microphone.
const (
	AgentAudio = 1
	HumanAudio = 2
)

// HumanRate is the sample rate of relayed microphones.
const HumanRate = 16000

// RoomFrame is a binary room frame: its kind, the utterance's or speaker's ID, and the PCM16LE mono samples.
type RoomFrame struct {
	Kind int
	ID   string
	PCM  []byte
}

// ParseRoomFrame splits a binary room frame: kind (byte 0), ID length n (byte 1), the ID (ASCII), a zero byte if 2 + n is
// odd, then PCM16LE mono. The PCM is copied out (a buffer of its own). nil: a frame too short for its ID.
func ParseRoomFrame(frame []byte) *RoomFrame {
	if len(frame) < 2 {
		return nil
	}
	length := int(frame[1])
	offset := 2 + length + ((2 + length) & 1)
	if len(frame) < offset {
		return nil
	}
	name := make([]byte, length)
	copy(name, frame[2:2+length])
	pcm := make([]byte, len(frame)-offset)
	copy(pcm, frame[offset:])
	return &RoomFrame{Kind: int(frame[0]), ID: string(name), PCM: pcm}
}

var roomNamePattern = regexp.MustCompile(`^p2-[A-Za-z0-9_-]{3,160}$`)

// RoomName is the service's room name for a conversation (its pattern: `^p2-[A-Za-z0-9_-]{3,160}$`); "" when the session ID
// does not fit.
func RoomName(session string, conversation int) string {
	name := "p2-" + session + "-" + strconv.Itoa(conversation)
	if !roomNamePattern.MatchString(name) {
		return ""
	}
	return name
}

// SampleSchema is `POST /voice/sample`: the owner hears a voice in its own browser (the settings sheet), outside the room.
var SampleSchema = obj(schema.Properties{
	opt("engine", id), opt("voice", id), prop("speed", num(schema.Minimum(0.5), schema.Maximum(2))), opt("language", LanguageSchema),
}, schema.Open())

// SampleText is what a sample says.
const SampleText = "Hi. This is how I sound when I read my answers to you."

// `POST /voice/log`: the browser's voice diagnostics (a batch), written to the server's log.
const (
	MaxLogEvents  = 100
	MaxLogMessage = 500
)

// LogSchema is a batch of the browser's voice diagnostics.
var LogSchema = obj(schema.Properties{
	// Which page sent it (a few random characters), to tell tabs and reloads apart.
	prop("client", str(schema.Pattern("^[A-Za-z0-9_-]{1,16}$"))),
	prop("events", arr(obj(schema.Properties{
		// The browser's `Date.now()` when it happened.
		prop("t", num(schema.Minimum(0), schema.Maximum(8.64e15))),
		prop("message", str(schema.MaxLength(MaxLogMessage))),
	}, schema.Open()), schema.MaxItems(MaxLogEvents))),
}, schema.Open())
