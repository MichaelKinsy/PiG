// Package voicetest is a speech service as sts-server answers (for the voice tests), with a fixed token: Pi's
// test/p2/voice-service.ts. /voices lists two engines; POST /speak answers a short WAV tone. /room is a voice room as
// rooms.py specifies, reduced to what p2 relies on: a welcome with the members; reset and vad (acknowledged) from anyone;
// owner-only config, speak (stable IDs, repeats acknowledged as duplicates) and cancel; each utterance as tts_start, 0.3 s of
// a quiet tone as kind-1 frames (ID-prefixed, padded to an even offset), tts_done, to every connection; microphone audio
// relayed as kind-2 frames to the other participants, and, with `Utterance`, once per connection after a second of audio:
// speech_start, talking, an interim with the utterance's first word, speech_end, then a final with that text to the speaker.
// It records each upgrade's headers, what it was asked to say, and the text commands it received.
package voicetest

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

//go:embed voices.json
var voicesJSON []byte

// VoicesJSON is the service's `GET /voices` answer (`VOICES` of voice-service.ts); the TypeScript cases read the same file.
func VoicesJSON() []byte { return append([]byte(nil), voicesJSON...) }

type engine struct {
	ID         string `json:"id"`
	SampleRate int    `json:"sampleRate"`
}

var voices = func() struct {
	Engines []engine `json:"engines"`
	Default struct {
		Engine string `json:"engine"`
		Voice  string `json:"voice"`
	} `json:"default"`
} {
	var parsed struct {
		Engines []engine `json:"engines"`
		Default struct {
			Engine string `json:"engine"`
			Voice  string `json:"voice"`
		} `json:"default"`
	}
	if err := json.Unmarshal(voicesJSON, &parsed); err != nil {
		panic(err)
	}
	return parsed
}()

// tone is `seconds` of a quiet 440 Hz tone, 16-bit mono PCM.
func tone(seconds float64, rate int) []byte {
	samples := int(math.Round(seconds * float64(rate)))
	pcm := make([]byte, samples*2)
	for index := range samples {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(int16(math.Round(math.Sin(2*math.Pi*440*float64(index)/float64(rate))*3000))))
	}
	return pcm
}

func wav(pcm []byte, rate int) []byte {
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(pcm)))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(pcm)))
	return append(header, pcm...)
}

// RoomFrame is a binary room frame: kind, the ID's length, the ID, a pad byte to an even offset, then PCM.
func RoomFrame(kind byte, id string, pcm []byte) []byte {
	frame := []byte{kind, byte(len(id))}
	frame = append(frame, id...)
	if (2+len(id))&1 == 1 {
		frame = append(frame, 0)
	}
	return append(frame, pcm...)
}

// Said is what the room was asked to say.
type Said struct{ ID, Text, Room, Engine, Voice string }

// Command is a text command a participant sent.
type Command struct {
	From    string
	Command map[string]any
}

type member struct{ ID, Name, Role string }

func (m member) json(connections int) map[string]any {
	return map[string]any{"id": m.ID, "name": m.Name, "role": m.Role, "connections": connections}
}

type connection struct {
	ws     *websocket.Conn
	member member
	room   string
	bytes  int
	said   bool
}

type roomState struct {
	config map[string]any
	said   map[string]bool
}

// Service is the running fake.
type Service struct {
	// URL is the service's base URL.
	URL string

	token     string
	utterance *string
	server    *http.Server
	listener  net.Listener
	upgrader  websocket.Upgrader

	mu          sync.Mutex
	upgrades    []http.Header
	spoken      []Said
	commands    []Command
	received    [][]byte
	samples     []map[string]any
	connections map[*connection]struct{}
	rooms       map[string]*roomState
	order       []*connection
	handlers    sync.WaitGroup
	closed      bool
}

// Option configures Start.
type Option func(*Service)

// Utterance makes the service hear this text once per connection after a second of microphone audio.
func Utterance(text string) Option { return func(s *Service) { s.utterance = &text } }

// Start runs the fake speech service with token until the test ends.
func Start(t testing.TB, token string, options ...Option) *Service {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{
		URL: "http://" + listener.Addr().String(), token: token, listener: listener,
		upgrader:    websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		connections: map[*connection]struct{}{}, rooms: map[string]*roomState{},
	}
	for _, option := range options {
		option(s)
	}
	s.server = &http.Server{Handler: http.HandlerFunc(s.serve)}
	//portlint:allow golifetime ends with Close
	go func() { _ = s.server.Serve(listener) }()
	t.Cleanup(s.Close)
	return s
}

func (s *Service) serve(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+s.token {
		response.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case request.URL.Path == "/voices":
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(voicesJSON)
	case request.URL.Path == "/speak" && request.Method == http.MethodPost:
		var sample map[string]any
		if err := json.NewDecoder(request.Body).Decode(&sample); err == nil {
			s.mu.Lock()
			s.samples = append(s.samples, sample)
			s.mu.Unlock()
		}
		response.Header().Set("Content-Type", "audio/wav")
		_, _ = response.Write(wav(tone(0.3, 16000), 16000))
	case request.URL.Path == "/room" && websocket.IsWebSocketUpgrade(request):
		s.room(response, request)
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

func (s *Service) others(of *connection) []*connection {
	var out []*connection
	for _, other := range s.order {
		if other.room == of.room && other.member.ID != of.member.ID {
			out = append(out, other)
		}
	}
	return out
}

func (s *Service) everyone(room string) []*connection {
	var out []*connection
	for _, other := range s.order {
		if other.room == room {
			out = append(out, other)
		}
	}
	return out
}

func send(ws *websocket.Conn, event map[string]any) { _ = ws.WriteJSON(event) }

func (s *Service) room(response http.ResponseWriter, request *http.Request) {
	headers := request.Header.Clone()
	name := request.Header.Get("X-Participant-Name")
	if unescaped, err := unescape(name); err == nil {
		name = unescaped
	}
	who := member{ID: request.Header.Get("X-Participant-Id"), Name: name, Role: request.Header.Get("X-Participant-Role")}
	room := request.Header.Get("X-Room")
	ws, err := s.upgrader.Upgrade(response, request, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ws.Close()
		return
	}
	s.handlers.Add(1)
	defer s.handlers.Done()
	s.upgrades = append(s.upgrades, headers)
	connection := &connection{ws: ws, member: who, room: room}
	state := s.rooms[room]
	if state == nil {
		state = &roomState{config: map[string]any{"engine": voices.Default.Engine, "voice": voices.Default.Voice, "speed": float64(1)}, said: map[string]bool{}}
		s.rooms[room] = state
	}
	s.connections[connection] = struct{}{}
	s.order = append(s.order, connection)
	var members []any
	for _, other := range s.everyone(room) {
		members = append(members, other.member.json(1))
	}
	you := who.json(1)
	you["connectionId"] = strconv.Itoa(len(s.connections))
	send(ws, map[string]any{
		"type": "welcome", "room": room, "you": you, "members": members, "tts": state.config,
		"current": nil, "micSampleRate": 16000, "limits": map[string]any{},
	})
	for _, other := range s.others(connection) {
		send(other.ws, map[string]any{"type": "member_joined", "member": who.json(1)})
	}
	s.mu.Unlock()
	for {
		kind, payload, err := ws.ReadMessage()
		if err != nil {
			break
		}
		s.mu.Lock()
		s.message(connection, state, kind == websocket.BinaryMessage, payload)
		s.mu.Unlock()
	}
	s.mu.Lock()
	delete(s.connections, connection)
	for index, each := range s.order {
		if each == connection {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	for _, other := range s.others(connection) {
		send(other.ws, map[string]any{"type": "member_left", "member": who.json(0)})
	}
	s.mu.Unlock()
	_ = ws.Close()
}

func unescape(text string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		if text[index] != '%' {
			out.WriteByte(text[index])
			continue
		}
		if index+2 >= len(text) {
			return "", strconv.ErrSyntax
		}
		value, err := strconv.ParseUint(text[index+1:index+3], 16, 8)
		if err != nil {
			return "", err
		}
		out.WriteByte(byte(value))
		index += 2
	}
	return out.String(), nil
}

func (s *Service) message(connection *connection, state *roomState, binaryMessage bool, payload []byte) {
	room, who := connection.room, connection.member
	if binaryMessage {
		s.received = append(s.received, append([]byte(nil), payload...))
		connection.bytes += len(payload)
		for _, other := range s.others(connection) {
			_ = other.ws.WriteMessage(websocket.BinaryMessage, RoomFrame(2, who.ID, payload))
		}
		if s.utterance != nil && !connection.said && connection.bytes >= 32000 {
			connection.said = true
			send(connection.ws, map[string]any{"type": "speech_start"})
			for _, other := range s.everyone(room) {
				send(other.ws, map[string]any{"type": "talking", "participant": who.ID, "on": true})
			}
			send(connection.ws, map[string]any{"type": "interim", "text": strings.SplitN(*s.utterance, " ", 2)[0], "ms": 1})
			send(connection.ws, map[string]any{"type": "speech_end"})
			for _, other := range s.everyone(room) {
				send(other.ws, map[string]any{"type": "talking", "participant": who.ID, "on": false})
			}
			send(connection.ws, map[string]any{"type": "final", "text": *s.utterance, "ms": 1})
		}
		return
	}
	var command map[string]any
	if err := json.Unmarshal(payload, &command); err != nil {
		return
	}
	s.commands = append(s.commands, Command{From: who.ID, Command: command})
	switch command["type"] {
	case "reset":
		return
	case "vad":
		send(connection.ws, map[string]any{"type": "vad", "silence": command["silence"]})
		return
	}
	if who.Role != "owner" {
		send(connection.ws, map[string]any{"type": "error", "code": "forbidden", "message": "owner only"})
		return
	}
	id, _ := command["id"].(string)
	text, _ := command["text"].(string)
	switch {
	case command["type"] == "config":
		for key, value := range command {
			state.config[key] = value
		}
		for _, other := range s.everyone(room) {
			event := map[string]any{"type": "config"}
			for key, value := range state.config {
				event[key] = value
			}
			send(other.ws, event)
		}
	case command["type"] == "speak" && id != "" && text != "":
		duplicate := state.said[id]
		send(connection.ws, map[string]any{"type": "speak_ack", "id": id, "duplicate": duplicate})
		if duplicate {
			return
		}
		state.said[id] = true
		engineID, _ := state.config["engine"].(string)
		voice, _ := state.config["voice"].(string)
		s.spoken = append(s.spoken, Said{ID: id, Text: text, Room: room, Engine: engineID, Voice: voice})
		rate := 16000
		for _, each := range voices.Engines {
			if each.ID == engineID {
				rate = each.SampleRate
			}
		}
		pcm := tone(0.3, rate)
		for _, other := range s.everyone(room) {
			send(other.ws, map[string]any{"type": "tts_start", "id": id, "engine": engineID, "voice": voice, "sampleRate": rate})
			_ = other.ws.WriteMessage(websocket.BinaryMessage, RoomFrame(1, id, pcm[:1000]))
			_ = other.ws.WriteMessage(websocket.BinaryMessage, RoomFrame(1, id, pcm[1000:]))
			send(other.ws, map[string]any{"type": "tts_done", "id": id, "cancelled": false})
		}
	}
}

// Upgrades are the headers of each room upgrade, in order.
func (s *Service) Upgrades() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.upgrades...)
}

// Spoken is what the rooms were asked to say, in order.
func (s *Service) Spoken() []Said {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Said(nil), s.spoken...)
}

// Commands are the text commands received, in order.
func (s *Service) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Command(nil), s.commands...)
}

// Received are the microphone frames received, in order.
func (s *Service) Received() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.received...)
}

// Samples are the bodies of the /speak requests, in order.
func (s *Service) Samples() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.samples...)
}

// Close ends every room socket and the service.
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for connection := range s.connections {
		_ = connection.ws.Close()
	}
	s.mu.Unlock()
	_ = s.server.Close()
	s.handlers.Wait()
}
