// Ports the server cases of test/p2/voice.test.ts (Pi main c23b9ec1): voice mode's switch and voice (the owner's and a
// read/write guest's, a view-only guest refused), "voice routes and mode" (engines pushed live as voice.json changes, the
// conversation's room bridged to the fake speech service of test/p2/voice-service.ts, the browser's diagnostics log, the
// owner-only sample) and the voice prompt section the per-conversation switch turns on. Pi's cases that run the browser's
// own TypeScript are plugins/voice/voice.web.test.ts; its prompt language case is plugins/voice/worker_test.go.

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/voice"
	voicecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/voice/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/voice/voicetest"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/gorilla/websocket"
)

// voices is the fake service's VOICES, as the plugin publishes it.
func voices(t *testing.T) string {
	t.Helper()
	parsed, err := chordjson.Decode(voicetest.VoicesJSON())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := chordjson.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func voiceUntil(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
	t.Helper()
	value, err := p2test.Until(source, predicate, 0)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func voicePoll(t *testing.T, check func() bool, timeout time.Duration, what string) {
	t.Helper()
	if err := p2test.Poll(func() (bool, error) { return check(), nil }, timeout, what); err != nil {
		t.Fatal(err)
	}
}

// voiceGuest is a guest admitted through a share link of mode: its cookie's headers.
func voiceGuest(t *testing.T, fixture *p2test.Fixture, owner host.Host, session, mode, name string) map[string]string {
	t.Helper()
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", mode), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links := voiceUntil(t, sharing.Member("links"), func(value delta.JsonValue) bool {
		for _, each := range list(value) {
			if object(each).Value("mode") == mode {
				return true
			}
		}
		return false
	})
	var token any
	for _, each := range list(links) {
		if object(each).Value("mode") == mode {
			token = object(each).Value("token")
		}
	}
	body, _ := chordjson.Marshal(delta.JsonObjectOf("token", token, "name", name))
	headers := map[string]string{"Content-Type": "application/json"}
	for key, value := range tunnelHeaders {
		headers[key] = value
	}
	joined, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(string(body))})
	if err != nil || joined.Status != 200 {
		t.Fatalf("join: %v %+v", err, joined)
	}
	cookie, _, _ := strings.Cut(joined.Headers.Get("Set-Cookie"), ";")
	return map[string]string{"Host": "share.test", "Origin": "https://share.test", "Cookie": cookie}
}

func voiceHeader(headers map[string]string) http.Header {
	out := http.Header{}
	for name, value := range headers {
		out.Set(name, value)
	}
	return out
}

// voiceRoom is a browser's socket to /voice/room, collecting what it receives.
type voiceRoom struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	texts  []map[string]any
	frames [][]byte
	done   chan struct{}
}

func (r *voiceRoom) textsMatching(match func(map[string]any) bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, text := range r.texts {
		if match(text) {
			count++
		}
	}
	return count
}

func (r *voiceRoom) hasText(kind string) bool {
	return r.textsMatching(func(text map[string]any) bool { return text["type"] == kind }) > 0
}

func (r *voiceRoom) hasCode(code string) bool {
	return r.textsMatching(func(text map[string]any) bool { return text["code"] == code }) > 0
}

func (r *voiceRoom) send(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
		t.Fatal(err)
	}
}

func (r *voiceRoom) close() {
	_ = r.conn.Close()
	<-r.done
}

func voiceJoin(t *testing.T, port int, path string, headers map[string]string) *voiceRoom {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+strconv.Itoa(port)+path, voiceHeader(headers))
	if err != nil {
		t.Fatalf("join the room: %v", err)
	}
	room := &voiceRoom{conn: conn, done: make(chan struct{})}
	go func() {
		defer close(room.done)
		for {
			kind, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			room.mu.Lock()
			if kind == websocket.BinaryMessage {
				room.frames = append(room.frames, payload)
			} else {
				var text map[string]any
				if json.Unmarshal(payload, &text) == nil {
					room.texts = append(room.texts, text)
				}
			}
			room.mu.Unlock()
		}
	}()
	t.Cleanup(room.close)
	voicePoll(t, func() bool { return room.hasText("welcome") }, 5*time.Second, "welcome")
	return room
}

// voiceStatus is the status the server answers a room upgrade with.
func voiceStatus(t *testing.T, port int, path string, headers map[string]string) int {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+strconv.Itoa(port)+path, voiceHeader(headers))
	if err == nil {
		_ = conn.Close()
		t.Fatalf("%s was upgraded", path)
	}
	if response == nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

// voiceStdout captures what the process writes to its standard output, as Pi's tests mock `console.log`; the returned
// function restores it and gives the lines written.
func voiceStdout(t *testing.T) func() []string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&captured, reader)
	}()
	restored := false
	restore := func() []string {
		if !restored {
			restored = true
			os.Stdout = original
			_ = writer.Close()
			<-copied
			_ = reader.Close()
		}
		return strings.Split(captured.String(), "\n")
	}
	t.Cleanup(func() { restore() })
	return restore
}

func voiceLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, "[voice") {
			out = append(out, line)
		}
	}
	return out
}

func voiceMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestVoiceModeSwitchAndVoiceFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture, owner, session, _ := sessionFixture(t)
	where := delta.JsonObjectOf("session", session, "conversation", float64(1))
	t.Run("keeps the owner's switch and voice; a read/write guest switches only its own and cannot choose", func(t *testing.T) {
		ownerMode, err := p2test.HoldIn(owner, voicecontract.VoiceMode, where, "owner")
		voiceMust(t, err)
		defer ownerMode.Release()
		aliceHeaders := voiceGuest(t, fixture, owner, session, "full", "Alice")
		alice, _ := fixture.Client(aliceHeaders)
		aliceMode, err := p2test.HoldIn(alice, voicecontract.VoiceMode, where, "participant")
		voiceMust(t, err)
		defer aliceMode.Release()
		_, err = ownerMode.Call("set", delta.JsonObjectOf("enabled", true), p2test.Background)
		voiceMust(t, err)
		// The answer follows its state: the owner's switch and voice are on the page when each call resolves.
		if value, _ := ownerMode.Member("enabled").Value(); value != true {
			t.Fatalf("owner enabled = %v", value)
		}
		choice := delta.JsonObjectOf("engine", "kokoro", "voice", "kokoro-af_heart", "speed", float64(1), "language", "de")
		_, err = ownerMode.Call("choose", choice, p2test.Background)
		voiceMust(t, err)
		if value, _ := ownerMode.Member("voice").Value(); object(value).Value("voice") != "kokoro-af_heart" {
			t.Fatalf("owner voice = %v", value)
		}
		// The guest sees the owner's choice, language included.
		seen := voiceUntil(t, aliceMode.Member("voice"), func(value delta.JsonValue) bool { return object(value).Value("voice") == "kokoro-af_heart" })
		if object(seen).Value("language") != "de" {
			t.Fatalf("the guest's voice = %v", seen)
		}
		if value, _ := aliceMode.Member("enabled").Value(); value != false {
			t.Fatalf("a guest's switch is its own: %v", value)
		}
		_, err = aliceMode.Call("set", delta.JsonObjectOf("enabled", true), p2test.Background)
		voiceMust(t, err)
		if value, _ := aliceMode.Member("enabled").Value(); value != true {
			t.Fatalf("guest enabled = %v", value)
		}
		// The guest turning its microphone off leaves the owner's voice mode on.
		_, err = aliceMode.Call("set", delta.JsonObjectOf("enabled", false), p2test.Background)
		voiceMust(t, err)
		if value, _ := aliceMode.Member("enabled").Value(); value != false {
			t.Fatalf("guest enabled = %v", value)
		}
		if value, _ := ownerMode.Member("enabled").Value(); value != true {
			t.Fatalf("owner enabled after the guest's off = %v", value)
		}
		// A reference carries every variant's members; the participant's binding refuses the owner's.
		_, err = aliceMode.Call("choose", choice, p2test.Background)
		p2test.ExpectChord(t, err, data.CodeInvalid)
		// A view-only guest has no voice mode.
		bob, _ := fixture.Client(voiceGuest(t, fixture, owner, session, "view", "Bob"))
		held, err := bob.Hold(p2test.Background, voicecontract.VoiceMode, where, host.GetOptions{})
		voiceMust(t, err)
		defer held.Release()
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, held), data.CodeForbidden)
	})
}

func TestVoiceRoutesAndModeFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	speech := voicetest.Start(t, "secret-token")
	port := fixture.Server.Port
	tunnel := map[string]string{"Host": "share.test"}
	t.Run("publishes the service's engines live as voice.json appears, breaks, moves away and returns", func(t *testing.T) {
		service, err := p2test.HoldIn(fixture.Browser(), voicecontract.VoiceService, delta.NewJsonObject(0), "talker")
		voiceMust(t, err)
		defer service.Release()
		voiceUntil(t, service.Member("voices"), func(value delta.JsonValue) bool { return value == nil })
		file := filepath.Join(fixture.DataDir, "voice.json")
		config := fmt.Sprintf(`{"url":"%s/","token":"secret-token"}`, speech.URL)
		voiceMust(t, os.WriteFile(file, []byte(config), 0o600))
		got := voiceUntil(t, service.Member("voices"), func(value delta.JsonValue) bool { return value != nil })
		if encoded, _ := chordjson.Marshal(got); string(encoded) != voices(t) {
			t.Fatalf("voices = %s", encoded)
		}
		voiceMust(t, os.WriteFile(file, []byte("{ not json"), 0o600))
		voiceUntil(t, service.Member("voices"), func(value delta.JsonValue) bool { return value == nil })
		voiceMust(t, os.WriteFile(file, []byte(config), 0o600))
		voiceUntil(t, service.Member("voices"), func(value delta.JsonValue) bool { return value != nil })
		voiceMust(t, os.Rename(file, file+".away"))
		voiceUntil(t, service.Member("voices"), func(value delta.JsonValue) bool { return value == nil })
		voiceMust(t, os.Rename(file+".away", file))
		got = voiceUntil(t, service.Member("voices"), func(value delta.JsonValue) bool { return value != nil })
		if encoded, _ := chordjson.Marshal(got); string(encoded) != voices(t) {
			t.Fatalf("voices after it returned = %s", encoded)
		}
		if value, _ := service.Member("failure").Value(); value != nil {
			t.Fatalf("failure = %v", value)
		}
	})

	t.Run("bridges the conversation's room: p2's identity, the owner's commands, speech for everyone", func(t *testing.T) {
		owner := fixture.Browser()
		sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		voiceMust(t, err)
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
		voiceMust(t, err)
		session := object(created).Value("id").(string)
		full := voiceGuest(t, fixture, owner, session, "full", "Alice")
		view := voiceGuest(t, fixture, owner, session, "view", "Bob")
		path := "/voice/room?session=" + session + "&conversation=1"
		// What a browser claims about itself is never passed on.
		spoofed := map[string]string{"X-Room": "p2-other-room", "X-Participant-Id": "mallory", "X-Participant-Role": "owner"}
		joinRoom := func(as map[string]string) *voiceRoom { return voiceJoin(t, port, path, merged(as, spoofed)) }
		ownerRoom := joinRoom(nil)
		guestRoom := joinRoom(full)
		room := "p2-" + session + "-1"
		upgrades := speech.Upgrades()
		ownerHeaders, guestHeaders := upgrades[len(upgrades)-2], upgrades[len(upgrades)-1]
		for name, want := range map[string]string{
			"X-Room": room, "X-Participant-Id": "owner", "X-Participant-Name": "Owner", "X-Participant-Role": "owner", "Authorization": "Bearer secret-token",
		} {
			if got := ownerHeaders.Get(name); got != want {
				t.Errorf("the owner's %s = %q, want %q", name, got, want)
			}
		}
		if guestHeaders.Get("X-Room") != room || guestHeaders.Get("X-Participant-Role") != "guest" || guestHeaders.Get("X-Participant-Name") != "Alice" {
			t.Errorf("the guest's headers = %v", guestHeaders)
		}
		guestID := guestHeaders.Get("X-Participant-Id")
		if matched, _ := regexp.MatchString(`^[A-Za-z0-9_-]+$`, guestID); !matched || guestID == "mallory" {
			t.Errorf("the guest's ID = %q", guestID)
		}

		// The owner's tabs say a sentence once: the repeat is the room's duplicate. Everyone hears it.
		ownerRoom.send(t, map[string]any{"type": "speak", "id": "17-0", "text": "Hello there."})
		ownerRoom.send(t, map[string]any{"type": "speak", "id": "17-0", "text": "Hello there."})
		acks := func() []bool {
			ownerRoom.mu.Lock()
			defer ownerRoom.mu.Unlock()
			var out []bool
			for _, text := range ownerRoom.texts {
				if text["type"] == "speak_ack" {
					duplicate, _ := text["duplicate"].(bool)
					out = append(out, duplicate)
				}
			}
			return out
		}
		voicePoll(t, func() bool { return len(acks()) == 2 }, 5*time.Second, "acks")
		if got := acks(); got[0] || !got[1] {
			t.Errorf("acks = %v, want [false true]", got)
		}
		var said []string
		for _, each := range speech.Spoken() {
			if each.Room == room {
				said = append(said, each.ID)
			}
		}
		if strings.Join(said, ",") != "17-0" {
			t.Errorf("spoken = %v", said)
		}
		voicePoll(t, func() bool { return guestRoom.hasText("tts_done") }, 5*time.Second, "guest hears it")
		guestRoom.mu.Lock()
		total := 0
		for _, frame := range guestRoom.frames {
			parsed := voicecontract.ParseRoomFrame(frame)
			if parsed == nil || parsed.Kind != 1 || parsed.ID != "17-0" {
				t.Errorf("the guest heard %+v", parsed)
				continue
			}
			total += len(parsed.PCM)
		}
		guestRoom.mu.Unlock()
		if want := int(0.3 * 16000 * 2); total != want {
			t.Errorf("the guest heard %d bytes of PCM, want %d", total, want)
		}

		// A guest may only reset its own microphone; anything else stops at p2.
		before := len(speech.Commands())
		guestRoom.send(t, map[string]any{"type": "speak", "id": "x-0", "text": "I am Pi now."})
		guestRoom.send(t, map[string]any{"type": "config", "voice": "kokoro-af_heart"})
		guestRoom.send(t, map[string]any{"type": "reset"})
		forbidden := func(room *voiceRoom) int {
			return room.textsMatching(func(text map[string]any) bool { return text["code"] == "forbidden" })
		}
		voicePoll(t, func() bool { return forbidden(guestRoom) == 2 }, 5*time.Second, "refusals")
		voicePoll(t, func() bool { return len(speech.Commands()) == before+1 }, 5*time.Second, "reset passed")
		if last := speech.Commands()[len(speech.Commands())-1]; last.From != guestID || last.Command["type"] != "reset" || len(last.Command) != 1 {
			t.Errorf("the room got %+v", last)
		}
		// The owner's language reaches the room; a malformed one stops at p2.
		ownerRoom.send(t, map[string]any{"type": "config", "language": "de"})
		ownerRoom.send(t, map[string]any{"type": "config", "language": "Deutsch"})
		voicePoll(t, func() bool {
			for _, each := range speech.Commands() {
				if each.Command["language"] == "de" {
					return true
				}
			}
			return false
		}, 5*time.Second, "language passed")
		voicePoll(t, func() bool { return ownerRoom.hasCode("invalid_message") }, 5*time.Second, "bad language")
		for _, each := range speech.Commands() {
			if each.Command["language"] == "Deutsch" {
				t.Error("a malformed language reached the room")
			}
		}
		// Everyone's own pause before sending passes, within 0.5-2 s.
		guestRoom.send(t, map[string]any{"type": "vad", "silence": 1.2})
		guestRoom.send(t, map[string]any{"type": "vad", "silence": 3})
		ownerRoom.send(t, map[string]any{"type": "vad", "silence": 0.5})
		ownerRoom.send(t, map[string]any{"type": "vad", "silence": 0.4})
		// reset, the language and the two valid pauses passed.
		voicePoll(t, func() bool { return len(speech.Commands()) == before+4 }, 5*time.Second, "pauses passed")
		voicePoll(t, func() bool { return forbidden(guestRoom) == 3 }, 5*time.Second, "guest range")
		voicePoll(t, func() bool { return ownerRoom.hasCode("invalid_message") }, 5*time.Second, "owner range")
		voicePoll(t, func() bool { return guestRoom.hasText("vad") }, 5*time.Second, "guest's pause acknowledged")
		commands := speech.Commands()
		lastTwo := commands[len(commands)-2:]
		if lastTwo[0].From != guestID || lastTwo[0].Command["type"] != "vad" || lastTwo[0].Command["silence"] != 1.2 ||
			lastTwo[1].From != "owner" || lastTwo[1].Command["type"] != "vad" || lastTwo[1].Command["silence"] != 0.5 {
			t.Errorf("the pauses that passed = %+v", lastTwo)
		}
		// The guest's microphone reaches the others, as the guest's.
		voiceMust(t, guestRoom.conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)))
		relayed := func() *voicecontract.RoomFrame {
			ownerRoom.mu.Lock()
			defer ownerRoom.mu.Unlock()
			for _, frame := range ownerRoom.frames {
				if parsed := voicecontract.ParseRoomFrame(frame); parsed != nil && parsed.Kind == 2 {
					return parsed
				}
			}
			return nil
		}
		voicePoll(t, func() bool { return relayed() != nil }, 5*time.Second, "relayed audio")
		if got := relayed(); got.ID != guestID || len(got.PCM) != 640 {
			t.Errorf("relayed audio = %s, %d bytes", got.ID, len(got.PCM))
		}
		ownerRoom.close()
		guestRoom.close()

		if got := voiceStatus(t, port, path, view); got != 403 {
			t.Errorf("view-only guests have no voice: %d", got)
		}
		if got := voiceStatus(t, port, "/voice/room?session=other-session&conversation=1", full); got != 403 {
			t.Errorf("another session's: %d", got)
		}
		if got := voiceStatus(t, port, "/voice/room?conversation=1", nil); got != 400 {
			t.Errorf("no session: %d", got)
		}
		if got := voiceStatus(t, port, "/voice/room?session="+session+"&conversation=0", nil); got != 400 {
			t.Errorf("conversation 0: %d", got)
		}

		// The engines for both (pushed live); a sample for the owner only; logs for both, the guest's marked as such.
		alice, _ := fixture.Client(full)
		aliceService, err := p2test.HoldIn(alice, voicecontract.VoiceService, delta.NewJsonObject(0), "talker")
		voiceMust(t, err)
		got := voiceUntil(t, aliceService.Member("voices"), func(value delta.JsonValue) bool { return value != nil })
		if encoded, _ := chordjson.Marshal(got); string(encoded) != voices(t) {
			t.Errorf("the guest's voices = %s", encoded)
		}
		aliceService.Release()
		bob, _ := fixture.Client(view)
		bobService, err := bob.Hold(p2test.Background, voicecontract.VoiceService, delta.NewJsonObject(0), host.GetOptions{})
		voiceMust(t, err)
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, bobService), data.CodeForbidden)
		bobService.Release()
		origin := map[string]string{"Origin": "http://127.0.0.1:" + strconv.Itoa(port), "Content-Type": "application/json"}
		sample := voiceHTTP(t, port, "/voice/sample", origin, `{"engine":"supertonic","voice":"supertonic-F1","speed":1.1,"language":"de"}`)
		if sample.Status != 200 || sample.Headers.Get("Content-Type") != "audio/wav" {
			t.Errorf("sample = %d %s", sample.Status, sample.Headers.Get("Content-Type"))
		}
		samples := speech.Samples()
		if last := samples[len(samples)-1]; last["voice"] != "supertonic-F1" || last["language"] != "de" {
			t.Errorf("the service was asked for %v", last)
		}
		if got := voiceHTTP(t, port, "/voice/sample", full, `{"speed":1}`).Status; got != 403 {
			t.Errorf("a guest's sample = %d", got)
		}
		restore := voiceStdout(t)
		batch := `{"client":"abc","events":[{"t":0,"message":"hello"}]}`
		if got := voiceHTTP(t, port, "/voice/log", full, batch).Status; got != 204 {
			t.Errorf("a guest's log = %d", got)
		}
		if got := voiceHTTP(t, port, "/voice/log", view, batch).Status; got != 403 {
			t.Errorf("a view-only guest's log = %d", got)
		}
		lines := voiceLines(restore())
		if len(lines) == 0 {
			t.Fatal("nothing was logged")
		}
		if matched, _ := regexp.MatchString(`^\[voice guest:\S+ abc\] `, lines[0]); !matched {
			t.Errorf("the guest's line = %q", lines[0])
		}
	})

	t.Run("writes the browser's diagnostics to the server log, for the owner only", func(t *testing.T) {
		origin := map[string]string{"Origin": "http://127.0.0.1:" + strconv.Itoa(port), "Content-Type": "application/json"}
		restore := voiceStdout(t)
		batch := map[string]any{
			"client": "abc123",
			"events": []any{
				map[string]any{"t": float64(time.Date(2026, 1, 1, 12, 34, 56, 789_000_000, time.UTC).UnixMilli()), "message": "socket open"},
				map[string]any{"t": float64(time.Date(2026, 1, 1, 12, 34, 57, 0, time.UTC).UnixMilli()), "message": "final: 12 chars\nsecond line"},
			},
		}
		post := func(headers map[string]string, body any) int {
			encoded, _ := json.Marshal(body)
			return voiceHTTP(t, port, "/voice/log", headers, string(encoded)).Status
		}
		if got := post(origin, batch); got != 204 {
			t.Fatalf("log = %d", got)
		}
		if got := post(merged(tunnel, origin), batch); got != 403 {
			t.Errorf("through a tunnel = %d", got)
		}
		if got := post(map[string]string{"Origin": "https://evil.test"}, batch); got != 403 {
			t.Errorf("from another origin = %d", got)
		}
		many := map[string]any{"client": "abc123", "events": make([]any, 101)}
		for index := range many["events"].([]any) {
			many["events"].([]any)[index] = map[string]any{"t": 0, "message": "x"}
		}
		if got := post(origin, many); got != 400 {
			t.Errorf("101 events = %d", got)
		}
		long := map[string]any{"client": "abc123", "events": []any{map[string]any{"t": 0, "message": strings.Repeat("x", 501)}}}
		if got := post(origin, long); got != 400 {
			t.Errorf("a 501-character message = %d", got)
		}
		if got := post(origin, map[string]any{"client": "a b", "events": []any{}}); got != 400 {
			t.Errorf("a client ID with a space = %d", got)
		}
		lines := voiceLines(restore())
		want := []string{"[voice abc123] 12:34:56.789 socket open", "[voice abc123] 12:34:57.000 final: 12 chars second line"}
		if strings.Join(lines, "\n") != strings.Join(want, "\n") {
			t.Errorf("the log = %q, want %q", lines, want)
		}
	})

	t.Run("turns the voice prompt section on per conversation", func(t *testing.T) {
		owner := fixture.Browser()
		sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		voiceMust(t, err)
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
		voiceMust(t, err)
		input := delta.JsonObjectOf("session", object(created).Value("id"), "conversation", float64(1))
		chat, err := p2test.HoldIn(owner, chatcontract.Chat, input, "participant")
		voiceMust(t, err)
		defer chat.Release()
		mode, err := p2test.HoldIn(owner, voicecontract.VoiceMode, input, "owner")
		voiceMust(t, err)
		defer mode.Release()
		prompt := voice.VoicePrompt[:40]
		sections := func(view delta.JsonValue) bool {
			encoded, _ := chordjson.Marshal(object(view).Value("entries"))
			return strings.Contains(string(encoded), prompt)
		}
		_, err = chat.Call("send", delta.JsonObjectOf("text", "typed", "requestId", "one"), p2test.Background)
		voiceMust(t, err)
		typed := voiceUntil(t, chat.Member("view"), hasEntryKind("pi.assistant"))
		if sections(typed) {
			t.Fatal("the voice section is on before voice mode is")
		}
		_, err = mode.Call("set", delta.JsonObjectOf("enabled", true), p2test.Background)
		voiceMust(t, err)
		voiceUntil(t, mode.Member("enabled"), func(value delta.JsonValue) bool { return value == true })
		_, err = chat.Call("send", delta.JsonObjectOf("text", "spoken", "requestId", "two"), p2test.Background)
		voiceMust(t, err)
		if _, err := p2test.Until(chat.Member("view"), sections, 20*time.Second); err != nil {
			t.Fatalf("the voice section never came on: %v", err)
		}
	})
}

func voiceHTTP(t *testing.T, port int, path string, headers map[string]string, body string) p2test.HTTPResult {
	t.Helper()
	return httpGet(t, port, path, p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(body)})
}
