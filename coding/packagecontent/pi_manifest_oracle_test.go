package packagecontent

import (
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/pi-manifest.ts

// readPiManifest over package.json files with byte-order marks (one, two, after whitespace), malformed JSON, non-object roots and "pi" values, resource fields that
// are not arrays of strings, duplicate members, odd whitespace and escapes. Pi reads the file with Node and PiG with encoding/json: the manifest each returns
// (null, or the resource fields it kept) must be equal.
func TestReadPiManifestMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(229))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	entry := func() string {
		return pick(`"./a.ts"`, `""`, `"é"`, `"\u00e9"`, `"a\nb"`, `"\ud83d\ude00"`, `1`, `null`, `true`, `{}`, `[]`, `"x"`)
	}
	list := func() string {
		switch rng.Intn(8) {
		case 0:
			return `null`
		case 1:
			return `"./a.ts"`
		case 2:
			return `{"0":"a"}`
		case 3:
			return `[]`
		case 4:
			return `[` + entry() + `,` + entry() + `]`
		default:
			parts := make([]string, rng.Intn(4))
			for i := range parts {
				parts[i] = pick(`"./a.ts"`, `"b"`, `"c d"`, `"é"`)
			}
			return `[` + strings.Join(parts, ",") + `]`
		}
	}
	piObject := func() string {
		var members []string
		for _, field := range []string{"extensions", "skills", "prompts", "themes", "other", "Extensions", "__proto__"} {
			if rng.Intn(2) == 0 {
				members = append(members, `"`+field+`":`+list())
			}
		}
		if rng.Intn(6) == 0 && len(members) > 0 {
			members = append(members, members[rng.Intn(len(members))])
		}
		rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
		return `{` + strings.Join(members, pick(",", " , ", ",\n", ",\t")) + `}`
	}
	var documents []string
	for range 1500 {
		pi := piObject()
		if rng.Intn(10) == 0 {
			pi = pick(`null`, `[]`, `"s"`, `1`, `true`, `[{"extensions":[]}]`)
		}
		doc := `{"name":"p","pi":` + pi + `}`
		switch rng.Intn(14) {
		case 0:
			doc = `[` + doc + `]`
		case 1:
			doc = `{"name":"p"}`
		case 2:
			doc = pick("", " ", "null", "7", `"x"`, "{", `{"pi":`, `{"pi":{}`, `{'pi':{}}`, `{"pi":{},}`, `{"pi":{"extensions":["a",]}}`)
		case 3:
			doc = "\ufeff" + doc
		case 4:
			doc = "\ufeff\ufeff" + doc
		case 5:
			doc = " \ufeff" + doc
		case 6:
			doc = pick(" ", "\n", "\t", "\r\n") + doc + pick(" ", "\n", "")
		case 7:
			doc = pick("\u00a0", "\v", "\f", "\u2028", "\u0085") + doc
		case 8:
			doc = `{"pi":` + pi + `,"pi":` + piObject() + `}`
		case 9:
			doc = `{"pi":` + pi + `,"deep":` + strings.Repeat("[", 50) + strings.Repeat("]", 50) + `}`
		}
		documents = append(documents, doc)
	}
	dir := t.TempDir()
	type probe struct {
		Path string `json:"path"`
	}
	var probes []probe
	for i, doc := range documents {
		path := filepath.Join(dir, "p"+strconv.Itoa(i)+".json")
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		probes = append(probes, probe{path})
	}
	probes = append(probes, probe{filepath.Join(dir, "missing.json")})
	// A file that is not valid UTF-8 is read with replacement characters by Node.
	badUTF8 := filepath.Join(dir, "bad.json")
	raw, _ := hex.DecodeString("7b227069223a7b22657874656e73696f6e73223a5b22ff2e2f61225d7d7d")
	if err := os.WriteFile(badUTF8, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	probes = append(probes, probe{badUTF8})
	var want []json.RawMessage
	pioracle.Run(t, `
const { readPiManifest } = await load("pi-coding-agent/core/pi-manifest.js");
emit(input.map(({ path }) => readPiManifest(path)));`, probes, &want)
	nonNull := map[bool]int{}
	failures := 0
	for i, p := range probes {
		got := ReadPiManifest(p.Path)
		var gotJSON any
		if got != nil {
			fields := map[string]any{}
			for name, entries := range map[string][]string{"extensions": got.Extensions, "skills": got.Skills, "prompts": got.Prompts, "themes": got.Themes} {
				if entries != nil {
					fields[name] = entries
				}
			}
			gotJSON = fields
		}
		var wantJSON any
		if err := json.Unmarshal(want[i], &wantJSON); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(gotJSON)
		var gotRound any
		_ = json.Unmarshal(encoded, &gotRound)
		nonNull[wantJSON != nil]++
		if !reflect.DeepEqual(gotRound, wantJSON) {
			if failures++; failures <= 8 {
				body, _ := os.ReadFile(p.Path)
				t.Errorf("%q: PiG %s, Pi %s", body, encoded, want[i])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d files differ", failures, len(probes))
	}
	if nonNull[true] < 300 || nonNull[false] < 100 {
		t.Fatalf("manifests: %v", nonNull)
	}
}
