package codingagent

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// externalEditorScenarioEnv, when set to "1", makes this test binary act as a scripted editor of TestOpenExternalEditorMatchesPi before TestMain runs any test: the first argument names what the editor does, the rest are the arguments the command gave it with the prompt path last. It is the editor that runs everywhere, Windows included, where a #! script is not a program cmd.exe can start. It does what the .sh editors of the same test do, in Go.
const externalEditorScenarioEnv = "PIG_TEST_EXTERNAL_EDITOR_SCENARIO"

// externalEditorReplacePrefix starts the argument of the editor that overwrites the prompt with the bytes that follow, in hex.
const externalEditorReplacePrefix = "replace:"

// runExternalEditorScenario records each argument as "[arg]" and then "content:" and the prompt as it found it, all appended to the file named by $REC, then acts as args[0] says: append "added" to the prompt, overwrite it ("changed" and exit 1, nothing at all, or the replacement bytes), delete it, empty it, exit 3, or die abruptly. It returns the exit status.
func runExternalEditorScenario(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "external editor scenario: want a mode and the prompt path, got", args)
		return 2
	}
	mode, args := args[0], args[1:]
	prompt := args[len(args)-1]
	var record bytes.Buffer
	for _, arg := range args {
		fmt.Fprintf(&record, "[%s]\n", arg)
	}
	record.WriteString("content:")
	if content, err := os.ReadFile(prompt); err == nil {
		record.Write(content)
	}
	record.WriteByte('\n')
	file, err := os.OpenFile(os.Getenv("REC"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err == nil {
		_, err = file.Write(record.Bytes())
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "external editor scenario:", err)
		return 2
	}
	switch {
	case mode == "append":
		if file, err := os.OpenFile(prompt, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666); err == nil {
			_, _ = file.WriteString("added")
			_ = file.Close()
		}
	case mode == "exit1":
		_ = os.WriteFile(prompt, []byte("changed"), 0o666)
		return 1
	case mode == "exit3":
		return 3
	case mode == "noop":
	case mode == "delete":
		_ = os.Remove(prompt)
	case mode == "empty":
		_ = os.WriteFile(prompt, nil, 0o666)
	case mode == "signal":
		terminateSelf()
		select {} // unreachable: the process is already gone
	case strings.HasPrefix(mode, externalEditorReplacePrefix):
		replacement, err := hex.DecodeString(strings.TrimPrefix(mode, externalEditorReplacePrefix))
		if err != nil {
			fmt.Fprintln(os.Stderr, "external editor scenario:", err)
			return 2
		}
		_ = os.WriteFile(prompt, replacement, 0o666)
	default:
		fmt.Fprintln(os.Stderr, "external editor scenario: unknown mode", mode)
		return 2
	}
	return 0
}
