//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// D2: an extension or Piglet adds a sprite to /sprite with ctx.ui.registerSprite. These tests run the real binary in a
// pseudo-terminal with a real Go, Python and Rust extension: the sprite appears in /sprite list, /sprite set saves it and
// the header draws its head at once, a restart with the extension keeps it, and a restart without the extension draws
// the default head while the saved choice stays.

// spriteProbePig is the standard pig; spriteProbeBlue colors its body, so the header shows the sprite by that color.
const (
	spriteProbeBlue    = "91;141;239"
	spriteProbeDefault = "72;163;129"
)

var spriteProbePig = []string{
	"................", "...OOOO..OOOO...", "...OeeO..OeeO...", "..OeePPPPPPeeO..",
	".OPPPpPPPPpPPPO.", ".OPPWWPPPPWWPPO.", ".OPPWKPPPPKWPPO.", ".OPbbPssssPbbPO.",
	".OPPPsKssKsPPPO.", ".OPPPPssssPPPPO.", ".OPPPPPPPPPPPPO.", "..OPPPPPPPPPPO..",
	"...OOOOOOOOOO...", "................",
}

func spriteProbeSources() (goSource, pySource, rsSource string) {
	pig, _ := json.Marshal(spriteProbePig)
	palette := `{"O": "#18141E", "K": "#18141E", "W": "#FFFFFF", "P": "#5B8DEF", "p": "#8FB2F5", "s": "#3D6BC4", "b": "#F49AA6", "e": "#4A7BD8"}`
	goSource = fmt.Sprintf(`package spriteprobe

import (
	"encoding/json"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("spriteprobe")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		var mascot []string
		var palette map[string]string
		_ = json.Unmarshal([]byte(%q), &mascot)
		_ = json.Unmarshal([]byte(%q), &palette)
		return nil, ctx.RegisterSprite(sdk.SpriteDefinition{ID: "blue-pig", Name: "Blue PiG", Tagline: "Registered by a Go extension.", Mascot: mascot, Palette: palette})
	})
	return ext
}
`, pig, palette)
	pySource = fmt.Sprintf(`import json
import os
import sys

sys.path.insert(0, os.environ["PIG_SDK_PY_ROOT"])
import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("spriteprobe")

    def start(ctx, _event):
        ctx.register_sprite(pig_sdk.SpriteDefinition(
            id="blue-pig", name="Blue PiG", tagline="Registered by a Python extension.",
            mascot=json.loads(%q), palette=json.loads(%q)))

    ext.on_event("session_start", start)
    return ext
`, pig, palette)
	rsSource = fmt.Sprintf(`use pig_sdk::{Extension, SpriteDefinition};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("spriteprobe");
    ext.on_event("session_start", false, |ctx, _data| {
        let mascot: Vec<String> = serde_json::from_str(%q).unwrap();
        let palette = serde_json::from_str(%q).unwrap();
        let _ = ctx.register_sprite(&SpriteDefinition {
            id: "blue-pig".to_string(),
            name: "Blue PiG".to_string(),
            tagline: "Registered by a Rust extension.".to_string(),
            mascot,
            palette,
        });
        None
    });
    ext
}
`, pig, palette)
	return goSource, pySource, rsSource
}

func writeSpriteProbe(t *testing.T, language string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "spriteprobe")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goSource, pySource, rsSource := spriteProbeSources()
	switch language {
	case "go":
		write("go.mod", fmt.Sprintf("module example.com/spriteprobe\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.3.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", filepath.Join(fixtureSourceRoot, "extensions", "sdk")))
		write("extension.go", goSource)
	case "py":
		write("spriteprobe.py", pySource)
	case "rs":
		write("Cargo.toml", fmt.Sprintf("[package]\nname = \"spriteprobe\"\nversion = \"0.1.0\"\nedition = \"2024\"\npublish = false\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", filepath.Join(fixtureSourceRoot, "extensions", "sdk-rs")))
		write("src/lib.rs", rsSource)
	}
	return root
}

// spriteSession is one interactive PiG process in a pseudo-terminal.
type spriteSession struct {
	t      *testing.T
	master *os.File
	output *ptyOutput
	exited chan struct{}
}

func startSpriteSession(t *testing.T, binary, pigHome, agentDir string, args ...string) *spriteSession {
	t.Helper()
	seedFirstRunDone(t, agentDir)
	master, slave := openPTY(t, 30, 120)
	cmd := exec.Command(binary, append(args, "--model", "test-faux/faux-1")...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+pigHome, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1",
		"PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	s := &spriteSession{t: t, master: master, output: &ptyOutput{}, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(s.exited) }()
	go func() { _, _ = io.Copy(s.output, master) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-s.exited
		_ = master.Close()
	})
	return s
}

func (s *spriteSession) await(stage string, mark int, marker string) {
	s.t.Helper()
	budget := testbudget.Wait(s.t)
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		select {
		case <-s.exited:
			s.t.Fatalf("pig exited %s; last output: %q", stage, s.tail())
		default:
		}
		if bytes.Contains(s.output.since(mark), []byte(marker)) {
			s.output.waitQuiet(mark, []byte(marker), 300*time.Millisecond, budget)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("%s: %q never appeared; last output: %q", stage, marker, s.tail())
}

func (s *spriteSession) tail() string {
	all := s.output.since(0)
	return string(all[max(0, len(all)-3000):])
}

func (s *spriteSession) send(text string) int {
	s.t.Helper()
	mark := s.output.mark()
	if _, err := s.master.WriteString(text); err != nil {
		s.t.Fatal(err)
	}
	return mark
}

func savedSprite(t *testing.T, pigHome string) string {
	t.Helper()
	data, err := os.ReadFile(piglogin.StatePath(pigHome))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Variant string `json:"variant"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state.Variant
}

func TestExtensionSpriteJoinsSpriteAndPersistsAcrossRestarts(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, language := range []string{"go", "py", "rs"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			probe := writeSpriteProbe(t, language)
			pigHome, agentDir := t.TempDir(), t.TempDir()

			first := startSpriteSession(t, binary, pigHome, agentDir, "-e", probe)
			first.await("starting", 0, "v")
			mark := first.send("/sprite list\r")
			first.await("listing the sprites", mark, "blue-pig: Blue PiG: Registered by a")
			mark = first.send("/sprite set blue-pig\r")
			first.await("drawing the chosen sprite", mark, spriteProbeBlue)
			if got := savedSprite(t, pigHome); got != "blue-pig" {
				t.Fatalf("saved sprite = %q, want blue-pig", got)
			}
			// /sprite preview shows the full art in an overlay, with the sprite's name and tagline; a key closes it.
			mark = first.send("/sprite preview blue-pig\r")
			first.await("previewing the sprite", mark, "Press any key to close.")
			if !bytes.Contains(first.output.since(mark), []byte("Blue PiG\x1b[0m")) || !bytes.Contains(first.output.since(mark), []byte(spriteProbeBlue)) {
				t.Fatalf("the preview lacks the sprite's name or colors: %q", first.tail())
			}
			first.send("q")
			time.Sleep(300 * time.Millisecond)

			// A restart with the extension draws the saved sprite again.
			again := startSpriteSession(t, binary, pigHome, agentDir, "-e", probe)
			again.await("restarting with the extension", 0, spriteProbeBlue)

			// Without the extension the header draws the default head and the saved choice stays.
			without := startSpriteSession(t, binary, pigHome, agentDir)
			without.await("restarting without the extension", 0, spriteProbeDefault)
			if strings.Contains(string(without.output.since(0)), spriteProbeBlue) {
				t.Fatal("the header drew an unloaded extension's sprite")
			}
			mark = without.send("/sprite list\r")
			without.await("listing without the extension", mark, "sheriff: Sheriff PiG")
			if strings.Contains(string(without.output.since(mark)), "blue-pig") {
				t.Fatal("/sprite list shows an unloaded extension's sprite")
			}
			if got := savedSprite(t, pigHome); got != "blue-pig" {
				t.Fatalf("saved sprite after a run without the extension = %q, want blue-pig", got)
			}
		})
	}
}

// Outside interactive mode there is no header, and registering a sprite is not an error.
func TestExtensionSpriteRegistrationInPrintMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	probe := writeSpriteProbe(t, "go")
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), binary, "-e", probe, "--model", "test-faux/faux-1", "--mode", mode, "-p", "What is 20+22?")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+seededAgentDir(t), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
			output, err := cmd.CombinedOutput()
			if err != nil || bytes.Contains(output, []byte("sprite")) {
				t.Fatalf("pig --mode %s -p: %v\n%s", mode, err, output)
			}
		})
	}
}

// D87: the real binary starts in fullscreen mode by default, as Pi 1.0 does (settings-manager.ts getTuiMode), so mouse
// reporting is on, and a click inside the header's pig head plays the logo animation (its "to return" hint appears); a
// click beside the head, on the version, does not. The owner's click did nothing while PiG still defaulted to regular mode,
// which reports no mouse events.
func TestFullscreenClickOnTheHeaderHeadPlaysTheAnimation(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	s := startSpriteSession(t, binary, t.TempDir(), t.TempDir())
	s.await("starting in fullscreen", 0, "can explain")
	if !bytes.Contains(s.output.since(0), []byte("\x1b[?1006h")) || !bytes.Contains(s.output.since(0), []byte("\x1b[?1000h")) {
		t.Fatalf("fullscreen mode did not turn on SGR mouse reporting: %q", s.tail())
	}
	// The header starts on screen row 2 after the spacer; the head takes columns 2 to 17 after the one-cell padding, and
	// the version starts at column 19.
	// The hint fades in from 1.4 s to 1.9 s into the animation, so 3 s without it means no animation started.
	mark := s.send("\x1b[<0;20;2M\x1b[<0;20;2m")
	time.Sleep(3 * time.Second)
	if bytes.Contains(s.output.since(mark), []byte("to return")) {
		t.Fatal("a click on the version played the animation")
	}
	mark = s.send("\x1b[<0;4;3M\x1b[<0;4;3m")
	s.await("clicking the head", mark, "to return")
}
