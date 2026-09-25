package pigrunner

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
)

func TestExtensionRegistersRunnerCommandsOnly(t *testing.T) {
	extensionConn, hostConn := net.Pipe()
	defer func() { _ = hostConn.Close() }()
	done := make(chan error, 1)
	go func() { done <- Extension().RunWithConn(extensionConn) }()

	var header [4]byte
	if _, err := io.ReadFull(hostConn, header[:]); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(hostConn, frame); err != nil {
		t.Fatal(err)
	}
	var registration struct {
		Register struct {
			Name     string `json:"name"`
			Commands []struct {
				Name string `json:"name"`
			} `json:"commands"`
		} `json:"register"`
	}
	if err := json.Unmarshal(frame, &registration); err != nil {
		t.Fatal(err)
	}
	if registration.Register.Name != "pigrunner" {
		t.Fatalf("extension name = %q", registration.Register.Name)
	}
	if len(registration.Register.Commands) != 2 || registration.Register.Commands[0].Name != "runner" || registration.Register.Commands[1].Name != "pig-runner" {
		t.Fatalf("registered commands = %+v", registration.Register.Commands)
	}
	writeFrame := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		binary.BigEndian.PutUint32(header[:], uint32(len(data)))
		if _, err := hostConn.Write(header[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := hostConn.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	writeFrame(map[string]any{"type": "ready", "ready": map[string]any{"width": 80}})
	writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("extension did not stop after shutdown")
	}
}

func TestRunnerComponentTimerInvalidatesAndStops(t *testing.T) {
	component := newRunnerComponent(7, standardlogin.FindVariant(""), func() int { return 24 })
	_, _ = component.HandleInput(" ")
	invalidated := make(chan struct{}, 8)
	component.SetInvalidate(func() {
		select {
		case invalidated <- struct{}{}:
		default:
		}
	})

	select {
	case <-invalidated:
	case <-time.After(5 * tickRate):
		t.Fatal("runner timer did not request a frame")
	}
	distance := func() float64 {
		component.mu.Lock()
		defer component.mu.Unlock()
		return component.game.Distance
	}
	before := distance()
	select {
	case <-invalidated:
	case <-time.After(5 * tickRate):
		t.Fatal("runner timer stopped before disposal")
	}
	if after := distance(); after <= before {
		t.Fatalf("runner did not advance: before=%v after=%v", before, after)
	}

	component.SetInvalidate(nil)
	component.Dispose()
	for len(invalidated) > 0 {
		<-invalidated
	}
	select {
	case <-invalidated:
		t.Fatal("runner requested a frame after invalidation detach and disposal")
	case <-time.After(2 * tickRate):
	}
}

func TestRunnerComponentControlsAndQuit(t *testing.T) {
	component := newRunnerComponent(11, standardlogin.FindVariant(""), func() int { return 24 })
	component.SetInvalidate(nil)
	defer component.Dispose()
	_, _ = component.HandleInput(" ")

	_, _ = component.HandleInput("p")
	component.mu.Lock()
	paused := component.game.Paused
	component.mu.Unlock()
	if !paused {
		t.Fatal("p did not pause the runner")
	}
	_, _ = component.HandleInput("p")
	_, _ = component.HandleInput("\x1b[A")
	component.mu.Lock()
	velocity := component.game.Pig.VelY
	component.mu.Unlock()
	if velocity >= 0 {
		t.Fatalf("up arrow did not jump: velocity=%v", velocity)
	}

	result, err := component.HandleInput("q")
	if err != nil || !result.Done {
		t.Fatalf("quit result = %+v, err=%v", result, err)
	}
	state, ok := result.Value.(runnerState)
	if !ok || state.HighScore < 11 {
		t.Fatalf("quit state = %#v", result.Value)
	}
}

func TestRunnerRestartKeepsHighScoreAndSprite(t *testing.T) {
	component := newRunnerComponent(5, standardlogin.FindVariant("sheriff"), func() int { return 24 })
	defer component.Dispose()
	_, _ = component.HandleInput(" ")
	component.mu.Lock()
	component.game.Over, component.game.HighScore = true, 90
	component.mu.Unlock()
	_, _ = component.HandleInput("r")
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.game.Over || component.game.HighScore != 90 || component.game.Variant.ID != "sheriff" {
		t.Fatalf("restart = over %v, high %d, sprite %q", component.game.Over, component.game.HighScore, component.game.Variant.ID)
	}
}

func TestRunnerHighScoreStateIsStrictAndProtected(t *testing.T) {
	configHome := t.TempDir()
	if err := saveHighScore(configHome, 42); err != nil {
		t.Fatal(err)
	}
	if got := loadHighScore(configHome); got != 42 {
		t.Fatalf("loaded high score = %d", got)
	}
	info, err := os.Stat(statePath(configHome))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
	if err := os.WriteFile(statePath(configHome), []byte(`{"highScore":42,"private":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadHighScore(configHome); got != 0 {
		t.Fatalf("state with unknown field loaded high score %d", got)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(statePath(configHome)), ".pigrunner-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("state write left temporary files: %v", matches)
	}
}

func TestRunnerScoresUsesWireResultAndFallback(t *testing.T) {
	fallback := runnerState{Score: 3, HighScore: 8}
	score, high := runnerScores(map[string]any{"score": float64(12), "highScore": float64(10)}, fallback)
	if score != 12 || high != 12 {
		t.Fatalf("wire scores = %d/%d", score, high)
	}
	score, high = runnerScores(nil, fallback)
	if score != 3 || high != 8 {
		t.Fatalf("fallback scores = %d/%d", score, high)
	}
}
