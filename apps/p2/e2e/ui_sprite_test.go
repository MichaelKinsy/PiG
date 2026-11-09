package e2e_test

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/brand/sprite"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"strconv"
)

// The branded page's mark follows PiG's `/sprite` selection live (ui/sprite.mjs): the default pig, then each sprite chosen
// while the page is open. P2_SHOTS names a directory for the screenshots; P2_SHOT_THEME the theme (default light).
func TestWebClientMarkFollowsTheSpriteSelection(t *testing.T) {
	modules := os.Getenv("P2_UI_NODE_PATH")
	if modules == "" {
		t.Skip("P2_UI_NODE_PATH (node_modules with playwright-core) is not set")
	}
	home := t.TempDir()
	source, err := sprite.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Web: &enabled, Sprite: source})
	shots := os.Getenv("P2_SHOTS")
	if shots == "" {
		shots = t.TempDir()
	}
	command := exec.Command("node", "ui/sprite.mjs", "http://127.0.0.1:"+strconv.Itoa(fixture.Server.Port), shots)
	command.Env = append(os.Environ(), "P2_UI_COOKIE="+fixture.Owner().Cookie)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	// The script says "STEP n" when it waits for a selection; whatever it printed before is its report of the state it checked.
	waitStep := func() {
		deadline := time.After(90 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatal("the script ended before the next step")
				}
				if strings.HasPrefix(line, "STEP ") {
					return
				}
				t.Logf("%s", line)
			case <-deadline:
				t.Fatal("the page did not reach the next step")
			}
		}
	}
	// The default sprite's page is checked first; then each choice, live.
	for _, choice := range []string{"pink", "kratos", "sheriff", "spider-ham", "mint", "darth-vader"} {
		waitStep()
		if err := piglogin.SaveVariant(home, choice); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(stdin, choice+"\n")
	}
	waitStep()
	_, _ = io.WriteString(stdin, "end\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
