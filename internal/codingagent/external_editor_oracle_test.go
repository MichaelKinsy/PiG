package codingagent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type externalEditorScenario struct {
	Command string `json:"command"`
	Content string `json:"content"`
	Rec     string `json:"rec"`
}

type externalEditorOutcome struct {
	Result struct {
		Status  string `json:"status"`
		Content string `json:"content"`
		Thrown  string `json:"thrown"`
	} `json:"result"`
	Message  string  `json:"message"`
	Recorded *string `json:"recorded"`
	Leaked   int     `json:"leaked"`
}

func octal(data []byte) string {
	var b strings.Builder
	for _, c := range data {
		fmt.Fprintf(&b, "\\%03o", c)
	}
	return b.String()
}

// externalEditorKit makes the scripted editors of one run of the comparison.
type externalEditorKit struct {
	// fixed maps each editor that does not vary (append, exit1, exit3, noop, delete, empty, signal, missing) to its command.
	fixed map[string]string
	// replace is the command of scenario i's editor, which overwrites the prompt with payload.
	replace func(i int, payload []byte) string
}

// shellScriptEditors are #! scripts that Pi and PiG start directly, as upstream does outside Windows. A script is no program cmd.exe can start.
func shellScriptEditors(t *testing.T, dir string) externalEditorKit {
	script := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nfor a; do last=\"$a\"; printf '%s\\n' \"[$a]\" >> \"$REC\"; done\nprintf 'content:' >> \"$REC\"; cat \"$last\" >> \"$REC\"; printf '\\n' >> \"$REC\"\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return externalEditorKit{
		fixed: map[string]string{
			"append":  script("append.sh", `printf 'added' >> "$last"`),
			"exit1":   script("exit1.sh", `printf 'changed' > "$last"; exit 1`),
			"exit3":   script("exit3.sh", `exit 3`),
			"noop":    script("noop.sh", `:`),
			"delete":  script("delete.sh", `rm -f "$last"`),
			"empty":   script("empty.sh", `: > "$last"`),
			"signal":  script("signal.sh", `kill -9 $$`),
			"missing": filepath.Join(dir, "does-not-exist"),
		},
		replace: func(i int, payload []byte) string {
			return script(fmt.Sprintf("replace%d.sh", i), fmt.Sprintf("printf '%s' > \"$last\"", octal(payload)))
		},
	}
}

// testBinaryEditors are this test binary, which runs the same scripts in Go (runExternalEditorScenario) under any command shell. Pi and PiG name it through PATH, so a command line stays free of the spaces that Pi's split on single spaces would cut.
func testBinaryEditors(t *testing.T, dir string) externalEditorKit {
	name := editorHelperName(t)
	t.Setenv(externalEditorScenarioEnv, "1")
	editor := func(mode string) string { return name + " " + mode }
	return externalEditorKit{
		fixed: map[string]string{
			"append":  editor("append"),
			"exit1":   editor("exit1"),
			"exit3":   editor("exit3"),
			"noop":    editor("noop"),
			"delete":  editor("delete"),
			"empty":   editor("empty"),
			"signal":  editor("signal"),
			"missing": filepath.Join(dir, "does-not-exist"),
		},
		replace: func(_ int, payload []byte) string {
			return editor(externalEditorReplacePrefix + hex.EncodeToString(payload))
		},
	}
}

// editInExternalEditor (modes/interactive/external-editor.ts) against pinned Pi with scripted editors: the arguments the editor receives (the command split on single spaces, empty arguments kept, the file last), the content it finds, what it writes back (appended text, replacement bytes including invalid UTF-8, a byte-order mark, trailing newlines, CRLF, an empty file, a deleted file), nonzero exit, death by signal, a missing binary, a command with a leading space (an empty file, which spawn rejects), and initial text with BOM, CRLF, tabs, emoji and NULs. Compared: success or failure, the returned text, the launch message and the leftover directories. The editors are #! scripts outside Windows, where upstream starts them without a shell, and this test binary everywhere, which is what runs on Windows, where upstream goes through cmd.exe: the command line the shell gets, and so what the editor receives, must be Node's.
func TestOpenExternalEditorMatchesPi(t *testing.T) {
	t.Run("shell scripts", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a .sh file is no program cmd.exe can start: shell: true hands it to whatever the file association is, so no shell-script editor exists on Windows; the test binary editors compare the same scenarios there")
		}
		compareExternalEditorWithPi(t, shellScriptEditors)
	})
	t.Run("test binary", func(t *testing.T) {
		compareExternalEditorWithPi(t, testBinaryEditors)
	})
}

func compareExternalEditorWithPi(t *testing.T, newKit func(t *testing.T, dir string) externalEditorKit) {
	dir := t.TempDir()
	kit := newKit(t, dir)
	rng := rand.New(rand.NewPCG(7, 21))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	payloads := [][]byte{[]byte("plain"), []byte("two\nlines\n"), []byte("trailing\n\n"), []byte("crlf\r\n"), []byte("\xef\xbb\xbfbom start"), []byte("\xef\xbb\xbf"), []byte("inv\xffalid\xc3"), []byte("é日本😀\n"), []byte(""), []byte("\n"), []byte("a\x00b\n"), []byte("\xef\xbb\xbf\xef\xbb\xbfx"), []byte("x\n\r"), []byte("\xe2\x80\xa8\n")}
	var scenarios []externalEditorScenario
	var names []string
	for i := range 360 {
		command := ""
		kind := pick([]string{"append", "exit1", "exit3", "noop", "delete", "empty", "signal", "missing", "replace", "replace", "replace"})
		if kind == "replace" {
			command = kit.replace(i, payloads[rng.IntN(len(payloads))])
		} else {
			command = kit.fixed[kind]
		}
		command += pick([]string{"", "", " -n", " +10", " a  b", "  ", " --flag=x y", " \"quoted arg\"", " 'single'", " $HOME", " *"})
		if rng.IntN(25) == 0 {
			command = " " + command
		}
		content := pick([]string{"", "hello", "line1\nline2\n", "\xef\xbb\xbfwith bom", "crlf\r\nlines\r\n", "tabs\tand é日本😀", "a\x00b", "\n", "trailing\n\n", "ends with cr\r"})
		scenarios = append(scenarios, externalEditorScenario{Command: command, Content: content})
		names = append(names, kind)
	}
	for i := range scenarios {
		scenarios[i].Rec = filepath.Join(dir, fmt.Sprintf("pi-%d.rec", i))
	}
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	script0, err := filepath.Abs("testdata/external_editor.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script0, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []externalEditorOutcome
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	mismatches, complete, failed := 0, 0, 0
	for i, scenario := range scenarios {
		rec := filepath.Join(dir, fmt.Sprintf("go-%d.rec", i))
		t.Setenv("REC", rec)
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		// What the editor or the shell writes to stderr, such as cmd.exe's "is not recognized" for a missing editor, is kept for a report instead of filling the test log.
		errPath := filepath.Join(dir, fmt.Sprintf("go-%d.err", i))
		errFile, err := os.Create(errPath)
		if err != nil {
			t.Fatal(err)
		}
		savedStdout, savedStderr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = writer, errFile
		got, runErr := OpenExternalEditor(context.Background(), scenario.Content, scenario.Command)
		os.Stdout, os.Stderr = savedStdout, savedStderr
		_ = writer.Close()
		_ = errFile.Close()
		var message bytes.Buffer
		_, _ = message.ReadFrom(reader)
		_ = reader.Close()
		editorStderr, _ := os.ReadFile(errPath)
		recorded, readErr := os.ReadFile(rec)
		w := want[i]
		piRecorded := ""
		if w.Recorded != nil {
			piRecorded = strings.ReplaceAll(*w.Recorded, scenario.Rec, rec)
		}
		var problems []string
		switch {
		case (w.Result.Status == "complete") != (runErr == nil):
			problems = append(problems, fmt.Sprintf("status: Go error %v, Pi %+v", runErr, w.Result))
		case runErr == nil && got != w.Result.Content:
			problems = append(problems, fmt.Sprintf("content %q, Pi %q", got, w.Result.Content))
		case runErr != nil && got != scenario.Content:
			problems = append(problems, fmt.Sprintf("failed run returned %q, not the initial text", got))
		}
		if message.String() != w.Message {
			problems = append(problems, fmt.Sprintf("message %q, Pi %q", message.String(), w.Message))
		}
		// The prompt path differs per process (a fresh pi-editor directory each time), so it is masked in the record.
		maskDirs := func(text string) string {
			var out []string
			for line := range strings.SplitSeq(text, "\n") {
				if strings.HasPrefix(line, "[") && strings.Contains(line, "pi-editor-") {
					line = "[<prompt>]"
				}
				out = append(out, line)
			}
			return strings.Join(out, "\n")
		}
		if (readErr == nil) != (w.Recorded != nil) || (readErr == nil && maskDirs(string(recorded)) != maskDirs(piRecorded)) {
			problems = append(problems, fmt.Sprintf("editor saw %q, Pi %q", maskDirs(string(recorded)), maskDirs(piRecorded)))
		}
		if w.Leaked != 0 {
			problems = append(problems, "Pi leaked its directory")
		}
		if w.Result.Status == "complete" {
			complete++
		} else {
			failed++
		}
		if len(problems) > 0 {
			if len(editorStderr) > 0 {
				problems = append(problems, fmt.Sprintf("editor stderr %q", editorStderr))
			}
			if mismatches++; mismatches <= 6 {
				t.Errorf("scenario %d (%s) command %q content %q: %s", i, names[i], scenario.Command, scenario.Content, strings.Join(problems, "; "))
			}
		}
	}
	if mismatches > 6 {
		t.Errorf("%d of %d scenarios differ from Pi", mismatches, len(scenarios))
	}
	if complete < 100 || failed < 60 {
		t.Errorf("thin coverage: %d complete, %d failed", complete, failed)
	}
}
