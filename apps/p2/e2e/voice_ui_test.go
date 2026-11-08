// Ports test/p2/voice-ui.test.ts (Pi main c23b9ec1): voice mode in a real browser (Chromium's fake microphone): the composer's
// microphone turns voice mode on, what the (fake) speech service hears becomes a message, and the agent's answer is read aloud
// through the room. Needs Node with playwright-core and Chromium (P2_UI_NODE_PATH names the node_modules holding
// playwright-core; PLAYWRIGHT_BROWSERS_PATH the browsers): skipped without, as Pi's `browserTests` are without Chromium.

package e2e_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	voicecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/voice/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/voice/voicetest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	// The web facet nothing else loads on the server: the app layout.
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
)

// uiBrowser is a Chromium driven by ui/driver.mjs (Pi's test/p2/browser.ts `Browser`).
type uiBrowser struct {
	t      *testing.T
	stdin  *json.Encoder
	answer *bufio.Scanner
	mu     sync.Mutex
	next   int
	close  func()
}

func startUIBrowser(t *testing.T, flags ...string) *uiBrowser {
	t.Helper()
	command := exec.Command("node", append([]string{"ui/driver.mjs"}, flags...)...)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	browser := &uiBrowser{t: t, stdin: json.NewEncoder(input), answer: bufio.NewScanner(output)}
	browser.answer.Buffer(make([]byte, 1<<20), 1<<20)
	var once sync.Once
	browser.close = func() {
		once.Do(func() {
			_ = browser.stdin.Encode(map[string]any{"id": 0, "op": "close"})
			_ = input.Close()
			_ = command.Wait()
		})
	}
	t.Cleanup(browser.close)
	return browser
}

func (b *uiBrowser) call(op string, fields map[string]any) any {
	b.t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	request := map[string]any{"id": b.next, "op": op}
	for key, value := range fields {
		request[key] = value
	}
	if err := b.stdin.Encode(request); err != nil {
		b.t.Fatal(err)
	}
	if !b.answer.Scan() {
		b.t.Fatalf("the browser driver ended during %s: %v", op, b.answer.Err())
	}
	var reply struct {
		Value any    `json:"value"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b.answer.Bytes(), &reply); err != nil {
		b.t.Fatal(err)
	}
	if reply.Error != "" {
		b.t.Fatalf("%s %v: %s", op, fields, reply.Error)
	}
	return reply.Value
}

func (b *uiBrowser) open(url string) { b.t.Helper(); b.call("open", map[string]any{"url": url}) }
func (b *uiBrowser) click(selector string) {
	b.t.Helper()
	b.call("click", map[string]any{"selector": selector})
}
func (b *uiBrowser) wait(expression string) {
	b.t.Helper()
	b.call("wait", map[string]any{"expression": expression})
}
func (b *uiBrowser) evaluate(expression string) any {
	b.t.Helper()
	return b.call("evaluate", map[string]any{"expression": expression})
}

func TestVoiceModeInTheBrowser(t *testing.T) {
	if os.Getenv("P2_UI_NODE_PATH") == "" {
		t.Skip("P2_UI_NODE_PATH (node_modules with playwright-core) is not set")
	}
	enabled := true
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Web: &enabled})
	speech := voicetest.Start(t, "token", voicetest.Utterance("What time is it?"))
	config := fmt.Sprintf(`{"url":%q,"token":"token"}`, speech.URL)
	if err := os.WriteFile(filepath.Join(fixture.DataDir, "voice.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	browser := startUIBrowser(t, "--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream", "--autoplay-policy=no-user-gesture-required")

	t.Run("sends what it hears and reads the answer aloud", func(t *testing.T) {
		browser.t = t
		owner := fixture.Browser()
		sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		voiceMust(t, err)
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
		voiceMust(t, err)
		session := object(created).Value("id").(string)
		input := delta.JsonObjectOf("session", session, "conversation", float64(1))
		chat, err := p2test.HoldIn(owner, chatcontract.Chat, input, "participant")
		voiceMust(t, err)
		defer chat.Release()
		mode, err := p2test.HoldIn(owner, voicecontract.VoiceMode, input, "owner")
		voiceMust(t, err)
		defer mode.Release()
		isEnabled := func(want bool) func(delta.JsonValue) bool {
			return func(value delta.JsonValue) bool { return (value == true) == want }
		}
		modeEnabled := func() bool { value, _ := mode.Member("enabled").Value(); return value == true }
		port := strconv.Itoa(fixture.Server.Port)
		browser.open("http://127.0.0.1:" + port + "/s/" + session)
		browser.click(".voice-chip")
		browser.wait(`document.querySelector(".voice-orb")`)
		voiceUntil(t, mode.Member("enabled"), isEnabled(true))
		// The composer holds only the orb: what voice mode does is its label, no text beside it.
		browser.wait(`document.querySelector(".voice-chip")?.getAttribute("aria-label")?.includes("Turn voice mode off")`)
		if browser.evaluate(`Boolean(document.querySelector(".voice-status, .voice-name"))`) != false {
			t.Error("the composer shows text beside the orb")
		}
		voiceUntil(t, chat.Member("view"), func(view delta.JsonValue) bool {
			encoded, _ := chordjson.Marshal(object(view).Value("entries"))
			return strings.Contains(string(encoded), "Echo: What time is it?")
		})
		voicePoll(t, func() bool {
			for _, said := range speech.Spoken() {
				if said.Text == "Echo: What time is it?" {
					return true
				}
			}
			return false
		}, 20*time.Second, "answer spoken")
		spoken := speech.Spoken()
		if last := spoken[len(spoken)-1]; last.Engine != "supertonic" || last.Voice != "supertonic-F1" {
			t.Errorf("spoken with %s/%s", last.Engine, last.Voice)
		}
		// Muted: still in voice mode, but no microphone audio reaches the room, and the room drops what it heard.
		browser.wait(`!document.querySelector(".voice-orb.speaking, .voice-orb.preparing")`)
		browser.click(".voice-mute")
		browser.wait(`document.querySelector(".voice-mute")?.getAttribute("aria-pressed") === "true"`)
		voicePoll(t, func() bool {
			for _, each := range speech.Commands() {
				if each.Command["type"] == "reset" {
					return true
				}
			}
			return false
		}, 5*time.Second, "reset on mute")
		heard := len(speech.Received())
		// An absence is asserted: nothing may arrive in this window.
		time.Sleep(1500 * time.Millisecond)
		if got := len(speech.Received()); got != heard {
			t.Errorf("%d frames sent while muted", got-heard)
		}
		if browser.evaluate(`Boolean(document.querySelector(".voice-orb"))`) != true {
			t.Error("voice mode stays on")
		}
		browser.click(".voice-mute")
		voicePoll(t, func() bool { return len(speech.Received()) > heard }, 5*time.Second, "audio again once unmuted")
		// Leaving the conversation (home here; a subagent's the same) suspends voice mode: the orb goes, the switch stays on.
		// Coming back starts it again.
		browser.evaluate(`(history.pushState(null, "", "/"), dispatchEvent(new PopStateEvent("popstate")), true)`)
		browser.wait(`!document.querySelector(".voice-orb")`)
		time.Sleep(500 * time.Millisecond)
		if !modeEnabled() {
			t.Error("suspended, not switched off")
		}
		browser.evaluate(`(history.back(), true)`)
		browser.wait(`document.querySelector(".voice-orb")`)
		if !modeEnabled() {
			t.Error("voice mode is off after coming back")
		}
		// Off again (once the answer is read: a tap while it is read stops only the speech): the switch follows.
		browser.click(".voice-chip")
		browser.wait(`!document.querySelector(".voice-orb") && !document.querySelector(".voice-mute")`)
		voiceUntil(t, mode.Member("enabled"), isEnabled(false))
		// The button follows voice.json live, without a reload.
		file := filepath.Join(fixture.DataDir, "voice.json")
		voiceMust(t, os.Rename(file, file+".away"))
		browser.wait(`!document.querySelector(".voice-chip")`)
		voiceMust(t, os.Rename(file+".away", file))
		browser.wait(`document.querySelector(".voice-chip")`)
	})
}
