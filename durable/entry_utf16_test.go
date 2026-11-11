package durable

import (
	"bufio"
	"io"
	"math/rand/v2"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// JSON.stringify is well formed (ES2019): a lone UTF-16 unit is the escape \udXXX, a high unit followed by a low unit one
// character, in every string of a record: Kind, Data, keys, messages. JSON.parse reads the escape back as the unit. A Go string
// holds a lone unit as WTF-8, and two adjacent halves are one character. Every encoder of an EntryRecord and the decoder
// agree with Node on random UTF-16 text.

var lineSeparators = strings.NewReplacer("\u2028", `\u2028`, "\u2029", `\u2029`)

// nodeNormalizer answers JSON.stringify(JSON.parse(line)) for each line of JSON text.
func nodeNormalizer(t *testing.T) func(text string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "node", "-e", `
const lines = require("node:readline").createInterface({ input: process.stdin });
lines.on("line", (line) => {
  let answer;
  try { answer = JSON.stringify(JSON.parse(line)); } catch (error) { answer = "ERROR " + error.message; }
  process.stdout.write(answer + "\n");
});`)
	in, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("node is required: %v", err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = command.Wait()
	})
	reader := bufio.NewReaderSize(out, 1<<16)
	return func(text string) string {
		t.Helper()
		// readline ends a line at U+2028 and U+2029 too; their escapes read back as the same characters.
		text = lineSeparators.Replace(text)
		if _, err := io.WriteString(in, text+"\n"); err != nil {
			t.Fatal(err)
		}
		answer, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("node: %v", err)
		}
		return strings.TrimSuffix(answer, "\n")
	}
}

func TestEntryRecordJSONMatchesNodeOnEveryUTF16String(t *testing.T) {
	stringify := nodeStringifier(t)
	normalize := nodeNormalizer(t)
	generator := entryGenerator{random: rand.New(rand.NewPCG(31, 1)), surrogates: true, everywhere: true, splitPairs: true}
	for n := range 6000 {
		record, description := generator.entry()
		want := stringify(description)
		appended, err := record.AppendStringify(nil)
		if err != nil || string(appended) != want {
			t.Fatalf("record %d: AppendStringify\n got: %s (%v)\nwant: %s", n, appended, err, want)
		}
		general, err := jsstring.MarshalJSON(record)
		if err != nil || string(general) != want {
			t.Fatalf("record %d: jsstring.MarshalJSON\n got: %s (%v)\nwant: %s", n, general, err, want)
		}
		marshaled, err := record.MarshalJSON()
		if err != nil {
			t.Fatalf("record %d: MarshalJSON: %v", n, err)
		}
		if got := normalize(string(marshaled)); got != want {
			t.Fatalf("record %d: MarshalJSON read by JSON.parse\n got: %s\nwant: %s", n, got, want)
		}
		var decoded, general2 EntryRecord
		if err := decoded.UnmarshalJSON([]byte(want)); err != nil {
			t.Fatalf("record %d: decoding %s: %v", n, want, err)
		}
		if err := general2.unmarshalGeneral([]byte(want)); err != nil {
			t.Fatalf("record %d: general decoding %s: %v", n, want, err)
		}
		if decoded.Kind != jsstring.Canonical(record.Kind) || general2.Kind != decoded.Kind {
			t.Fatalf("record %d: kind %q / %q, want %q", n, decoded.Kind, general2.Kind, jsstring.Canonical(record.Kind))
		}
		for name, back := range map[string]EntryRecord{"fast": decoded, "general": general2} {
			again, err := back.AppendStringify(nil)
			if err != nil || string(again) != want {
				t.Fatalf("record %d: %s decode then write\n got: %s (%v)\nwant: %s", n, name, again, err, want)
			}
		}
	}
}
