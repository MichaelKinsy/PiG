package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// print-mode.ts:146 console.error(assistantMsg.errorMessage) and :151 writeRawStdout(`${content.text}\n`) write a JavaScript string, so Node sends a lone
// surrogate as U+FFFD (`node -e "console.error('e\ud83d')"` prints 65 ef bf bd 0a). PiG carries the unit as WTF-8 (ED A0 BD), as a String.slice of an error
// body leaves it (ai TruncateErrorText, the Anthropic token-exchange body), and text mode must convert it as Node does.
func TestPrintModeResultWritesALoneSurrogateAsNodeDoes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	failed := []agent.AgentMessage{{Assistant: &agent.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: "e\xed\xa0\xbd"}}}
	if err := writePrintModeResult(failed, &stdout, &stderr); !errors.Is(err, errPrintModeHandled) {
		t.Fatalf("err = %v, want errPrintModeHandled", err)
	}
	if got, want := stderr.String(), "e\xef\xbf\xbd\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	stdout.Reset()
	answered := []agent.AgentMessage{{Assistant: &agent.AssistantMessage{StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "t\xed\xa0\xbd"}}}}}
	if err := writePrintModeResult(answered, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "t\xef\xbf\xbd\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
