package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// referenceParseRPCCommand is parseRPCCommand as it was before the members were read by encoding/json/v2: the sdk decoder validates the line and
// calls RPCCommandEnvelope.UnmarshalJSON.
func referenceParseRPCCommand(line []byte) (RPCCommandEnvelope, error) {
	var env RPCCommandEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return RPCCommandEnvelope{}, err
	}
	env.Raw = append([]byte(nil), line...)
	return env, nil
}

func envelopeSeeds() []string {
	return []string{
		``, ` `, `null`, `5`, `"s"`, `[]`, `[{"type":"prompt"}]`, `true`, `{}`, `{"id":1}`, `{"type":"prompt"}`,
		`{"id":"a","type":"prompt","message":"hi"}`,
		`{"type":"a","type":"b"}`, `{"id":1,"id":{"k":[1,2]},"type":"x"}`,
		`{"Type":"prompt","TYPE":"x","ID":1}`,
		`{"type":"\ud800"}`, `{"type":"\ud800x","id":"\udc00"}`, `{"id":"\ud800\udc00","type":"é"}`,
		"{\"type\":\"a\xffb\",\"id\":\"\xed\xa0\x80\"}",
		`{"id":null,"type":{"toString":null}}`, `{"id":1e400,"type":[1,null]}`, `{"id":-0,"type":1E21}`,
		`{"\u0074ype":"esc","i\u0064":3}`, `{"type":"x"`, `{"type":"x"} {}`, `{"type":x}`, `{"type":"x",}`,
		`{"type":"x","message":"` + "tab\there" + `"}`, "{\"type\":\"nl\nx\"}", ` {"type":"ws"}  `,
		`{"id":{"a":1,"a":2},"type":"d"}`, strings.Repeat(`[`, 11000), `{"type":` + strings.Repeat(`[`, 10050) + strings.Repeat(`]`, 10050) + `}`,
		`{"type":"prompt","images":[{"type":"image","data":"` + strings.Repeat("A", 4096) + `"}]}`,
	}
}

func sameEnvelope(t testing.TB, line []byte) {
	t.Helper()
	got, gotErr := parseRPCCommand(line)
	want, wantErr := referenceParseRPCCommand(line)
	if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
		t.Fatalf("%q: error %v, reference %v", line, gotErr, wantErr)
	}
	if !reflect.DeepEqual(got, want) || !bytes.Equal(got.ID, want.ID) || !bytes.Equal(got.TypeValue, want.TypeValue) || got.Type != want.Type || !bytes.Equal(got.Raw, want.Raw) {
		t.Fatalf("%q:\n got: %#v\nwant: %#v", line, got, want)
	}
}

// parseRPCCommand reads a command line as it did before encoding/json/v2 read its members: the same envelope, and for a line that is not
// JSON the same error text, which rpcParseError reports when the V8 diagnostic has nothing to say.
func TestParseRPCCommandMatchesTheSDKDecode(t *testing.T) {
	for _, seed := range envelopeSeeds() {
		sameEnvelope(t, []byte(seed))
	}
}

// The members' own bytes are retained as copies: a reader that reuses its line buffer cannot change an envelope.
func TestParseRPCCommandKeepsLoneSurrogatesInIDAndType(t *testing.T) {
	envelope, err := parseRPCCommand([]byte(`{"id":"\ud800","type":"\udc00x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(envelope.ID) != `"\ud800"` || string(envelope.TypeValue) != `"\udc00x"` {
		t.Fatalf("id=%s type=%s", envelope.ID, envelope.TypeValue)
	}
}

func FuzzParseRPCCommand(f *testing.F) {
	for _, seed := range envelopeSeeds() {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) { sameEnvelope(t, line) })
}

// BenchmarkParseRPCCommand reads command lines: a small one and a prompt that carries an image.
func BenchmarkParseRPCCommand(b *testing.B) {
	lines := map[string][]byte{
		"abort":  []byte(`{"id":"req-1","type":"abort"}`),
		"prompt": []byte(`{"id":"req-2","type":"prompt","message":"please look at this","images":[{"type":"image","mimeType":"image/png","data":"` + strings.Repeat("QUJD", 16<<10) + `"}]}`),
	}
	for name, line := range lines {
		b.Run(name+"/sdk", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(line)))
			for b.Loop() {
				if _, err := referenceParseRPCCommand(line); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/v2", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(line)))
			for b.Loop() {
				if _, err := parseRPCCommand(line); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
