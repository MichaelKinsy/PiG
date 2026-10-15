package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

var clipboardTempName = regexp.MustCompile(`pi-wsl-clip-[0-9a-f-]{36}\.txt`)

type clipboardCopyScenario struct {
	Env  map[string]string `json:"env"`
	Text string            `json:"text"`
}

type clipboardCopyOutcome struct {
	Stdout   string  `json:"stdout"`
	Thrown   *string `json:"thrown"`
	Recorded *string `json:"recorded"`
}

// copyToClipboard (utils/clipboard.ts) against pinned Pi on Linux with scripted wl-copy, xclip, xsel, termux-clipboard-set, wslpath and powershell.exe on PATH that record their arguments and input and succeed or fail: every combination of DISPLAY, WAYLAND_DISPLAY, TERMUX_VERSION, SSH and Mosh variables, WSL and Windows Terminal markers, missing and failing tools, and texts from empty to over the OSC 52 size limit. Compared: the OSC 52 bytes written to stdout, the error message, and each tool's recorded invocation.
func TestCopyToClipboardMatchesPi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the scripted tools and the Linux branch of copyToClipboard")
	}
	root := t.TempDir()
	rng := rand.New(rand.NewPCG(17, 5))
	tools := []string{"wl-copy", "xclip", "xsel", "termux-clipboard-set", "wslpath", "powershell.exe"}
	makeBin := func(name string, tool map[string]string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, toolName := range tools {
			mode, ok := tool[toolName]
			if !ok {
				continue
			}
			body := "#!/bin/sh\nprintf '%s' \"" + toolName + "\" >> \"$REC\"\nfor a; do printf ' [%s]' \"$a\" >> \"$REC\"; done\nprintf ' <' >> \"$REC\"\n/bin/cat >> \"$REC\" 2>/dev/null\nprintf '>\\n' >> \"$REC\"\n"
			if toolName == "wslpath" {
				body = "#!/bin/sh\nprintf 'wslpath [%s] [%s]\\n' \"$1\" \"$2\" >> \"$REC\"\nprintf 'C:\\\\Temp\\\\clip.txt\\n'\n"
			}
			if mode == "fail" {
				body += "exit 1\n"
			}
			if err := os.WriteFile(filepath.Join(dir, toolName), []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	modes := []string{"ok", "fail", "absent"}
	var scenarios []clipboardCopyScenario
	var names []string
	texts := []string{"hello", "", "Ünï 日本語 😀", "line1\nline2", "\u2028 sep", strings.Repeat("a", 74000), strings.Repeat("a", 75000), strings.Repeat("a", 74997), strings.Repeat("a", 75001), strings.Repeat("é", 40000), "it's \"quoted\""}
	for i := range 700 {
		tool := map[string]string{}
		for _, name := range tools {
			if mode := modes[rng.IntN(3)]; mode != "absent" {
				tool[name] = mode
			}
		}
		binName := fmt.Sprintf("bin%d", i)
		makeBin(binName, tool)
		env := map[string]string{"PATH": filepath.Join(root, binName), "REC": filepath.Join(root, fmt.Sprintf("pi-%d.rec", i))}
		for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "TERMUX_VERSION", "SSH_CONNECTION", "SSH_CLIENT", "MOSH_CONNECTION", "WSL_DISTRO_NAME", "WSLENV", "WT_SESSION", "PI_CLIPBOARD_OSC52"} {
			if rng.IntN(4) == 0 {
				env[key] = ":0"
			}
		}
		scenarios = append(scenarios, clipboardCopyScenario{Env: env, Text: texts[rng.IntN(len(texts))]})
		names = append(names, binName)
	}
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/clipboard_copy.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []clipboardCopyOutcome
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	managed := []string{"DISPLAY", "WAYLAND_DISPLAY", "TERMUX_VERSION", "SSH_CONNECTION", "SSH_CLIENT", "MOSH_CONNECTION", "WSL_DISTRO_NAME", "WSLENV", "WT_SESSION", "PI_CLIPBOARD_OSC52"}
	mismatches, copied, oscs := 0, 0, 0
	for i, scenario := range scenarios {
		for _, key := range managed {
			t.Setenv(key, "")
			_ = os.Unsetenv(key)
		}
		for key, value := range scenario.Env {
			if key == "REC" {
				value = filepath.Join(root, fmt.Sprintf("go-%d.rec", i))
			}
			t.Setenv(key, value)
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		// The OSC 52 sequence can exceed the pipe's buffer, so it is read while the copy runs.
		var stdout bytes.Buffer
		drained := make(chan struct{})
		go func() {
			_, _ = stdout.ReadFrom(reader)
			close(drained)
		}()
		savedStdout := os.Stdout
		os.Stdout = writer
		copyErr := CopyToClipboard(scenario.Text)
		os.Stdout = savedStdout
		_ = writer.Close()
		<-drained
		_ = reader.Close()
		recorded, readErr := os.ReadFile(filepath.Join(root, fmt.Sprintf("go-%d.rec", i)))
		w := want[i]
		var problems []string
		if (copyErr == nil) != (w.Thrown == nil) || (copyErr != nil && copyErr.Error() != *w.Thrown) {
			problems = append(problems, fmt.Sprintf("error %v, Pi %v", copyErr, w.Thrown))
		}
		if stdout.String() != w.Stdout {
			problems = append(problems, fmt.Sprintf("stdout %.60q, Pi %.60q", stdout.String(), w.Stdout))
		}
		goRecorded := strings.ReplaceAll(string(recorded), filepath.Join(root, fmt.Sprintf("go-%d.rec", i)), "<rec>")
		piRecorded := ""
		if w.Recorded != nil {
			piRecorded = strings.ReplaceAll(*w.Recorded, filepath.Join(root, fmt.Sprintf("pi-%d.rec", i)), "<rec>")
		}
		// The WSL route names a fresh temporary file after a random UUID.
		maskUUID := func(text string) string { return clipboardTempName.ReplaceAllString(text, "pi-wsl-clip-<uuid>.txt") }
		goRecorded, piRecorded = maskUUID(goRecorded), maskUUID(piRecorded)
		if (readErr == nil) != (w.Recorded != nil) || goRecorded != piRecorded {
			problems = append(problems, fmt.Sprintf("tools ran %.200q, Pi %.200q", goRecorded, piRecorded))
		}
		if w.Thrown == nil {
			copied++
		}
		if strings.Contains(w.Stdout, "\x1b]52;c;") {
			oscs++
		}
		if len(problems) > 0 {
			if mismatches++; mismatches <= 6 {
				t.Errorf("scenario %d (%s) env %v text %.20q: %s", i, names[i], scenario.Env, scenario.Text, strings.Join(problems, "; "))
			}
		}
	}
	if mismatches > 6 {
		t.Errorf("%d of %d scenarios differ from Pi", mismatches, len(scenarios))
	}
	if copied < 200 || oscs < 100 {
		t.Errorf("thin coverage: %d copied, %d OSC 52 writes", copied, oscs)
	}
}
