package chordjson

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type copyCase struct {
	Kind       string      `json:"kind"`
	Text       string      `json:"text"`
	Parsed     string      `json:"parsed"`
	Copied     string      `json:"copied"`
	Properties [][2]string `json:"properties"`
	Message    string      `json:"message"`
}

// Pi packages/chord/src/json.ts copyJson against the installed chord 1.1.0 dist, by testdata/copy_differential.mjs, on seeded random cases:
//
//   - JSON text whose objects list keys in random order, with repeats, array-index keys ("2", "10", "4294967294") and keys that only look
//     like indexes ("01", "-1", "1.5", "4294967295"). Decode then Marshal must give JSON.stringify(JSON.parse(text)), and Copy must give
//     JSON.stringify(copyJson(JSON.parse(text))) byte for byte. The texts hold no character encoding/json escapes differently.
//   - Objects built by assigning invalid and valid properties in random order: Copy must throw Pi's message for the first invalid
//     property in own-key order. A Go func stands for a function, a Go struct for a Date or a Map.
func TestCopyMatchesPiOnRandomCases(t *testing.T) {
	const count, seed = 3000, 20261008
	cmd := exec.CommandContext(t.Context(), "node", "testdata/copy_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(count), strconv.Itoa(seed))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var cases []copyCase
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != count {
		t.Fatalf("Pi oracle returned %d cases, want %d", len(cases), count)
	}
	values := map[string]any{"number": 1.0, "nan": math.NaN(), "infinity": math.Inf(-1), "function": func() {}, "date": time.Time{}, "map": struct{}{}, "string": "s"}
	texts, properties := 0, 0
	for index, c := range cases {
		switch c.Kind {
		case "text":
			texts++
			decoded, err := Decode([]byte(c.Text))
			if err != nil {
				t.Fatalf("case %d: Decode(%s): %v", index, c.Text, err)
			}
			if got, want := mustJSONText(t, decoded), c.Parsed; got != want {
				t.Fatalf("case %d: Decode(%s) = %s, Pi JSON.parse gives %s", index, c.Text, got, want)
			}
			copied, err := Copy(decoded)
			if err != nil {
				t.Fatalf("case %d: Copy: %v", index, err)
			}
			if got, want := mustJSONText(t, copied), c.Copied; got != want {
				t.Fatalf("case %d: Copy(%s) = %s, Pi copyJson gives %s", index, c.Text, got, want)
			}
		case "properties":
			properties++
			object := NewObject(len(c.Properties))
			for _, property := range c.Properties {
				object.Set(property[0], values[property[1]])
			}
			message := ""
			if _, err := Copy(object); err != nil {
				message = err.Error()
			}
			if message != c.Message {
				t.Fatalf("case %d: Copy(%v) error %q, Pi throws %q", index, c.Properties, message, c.Message)
			}
		}
	}
	if texts < count/4 || properties < count/4 {
		t.Fatalf("Pi oracle gave %d text and %d property cases", texts, properties)
	}
}
