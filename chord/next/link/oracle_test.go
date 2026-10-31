package link_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// The wire formats byte for byte against Pi: testdata/link_wire_golden.json is what Pi's own binary.ts, text.ts,
// values.ts and part.ts (the pinned source, run by Node; see testdata/link_wire.mjs) write for the cases in that
// generator. Go builds each message from the case's spec and must write the same bytes with both codecs, and read
// them back to the same message.

type goldenOutput struct {
	length int
	sha    string
	hex    string
}

type goldenCase struct {
	name         string
	spec         *delta.JsonObject
	binary, text goldenOutput
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/link_wire_golden.json")
	requireNoError(t, err)
	parsed, err := chordjson.Decode(raw)
	requireNoError(t, err)
	output := func(value any) goldenOutput {
		object := value.(*delta.JsonObject)
		result := goldenOutput{length: int(object.Value("length").(float64)), sha: object.Value("sha256").(string)}
		if text, ok := object.Value("hex").(string); ok {
			result.hex = text
		}
		return result
	}
	var cases []goldenCase
	for _, item := range parsed.([]any) {
		object := item.(*delta.JsonObject)
		cases = append(cases, goldenCase{name: object.Value("name").(string), spec: object.Value("spec").(*delta.JsonObject), binary: output(object.Value("binary")), text: output(object.Value("text"))})
	}
	return cases
}

func (output goldenOutput) check(t *testing.T, what string, got []byte) {
	t.Helper()
	sum := sha256.Sum256(got)
	if len(got) != output.length || hex.EncodeToString(sum[:]) != output.sha {
		if output.hex != "" {
			t.Fatalf("%s = %x, Pi wrote %s", what, got, output.hex)
		}
		t.Fatalf("%s: %d bytes with SHA-256 %x, Pi wrote %d bytes with %s", what, len(got), sum, output.length, output.sha)
	}
}

// resolveString is a spec string: a string, or `{$repeat: [s, n]}` with an optional prefix and suffix.
func resolveString(value any) string {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return value.(string)
	}
	repeat := object.Value("$repeat").([]any)
	prefix, _ := object.Value("prefix").(string)
	suffix, _ := object.Value("suffix").(string)
	return prefix + strings.Repeat(repeat[0].(string), int(repeat[1].(float64))) + suffix
}

func hexBytes(t testing.TB, text string) []byte {
	t.Helper()
	bytes, err := hex.DecodeString(text)
	requireNoError(t, err)
	return bytes
}

// specPart is a part from its spec: text, a deflated part, received bytes, or the JSON of a string.
func specPart(t testing.TB, value any) link.Part {
	t.Helper()
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return link.Text(typed)
	case *delta.JsonObject:
		if deflated, ok := typed.Get("$d"); ok {
			return link.NewDeflatedPart(hexBytes(t, deflated.(string)))
		}
		if received, ok := typed.Get("$b"); ok {
			return link.Bytes(received.(string))
		}
		if text, ok := typed.Get("$json"); ok {
			part, err := link.JsonPart(resolveString(text))
			requireNoError(t, err)
			return part
		}
	}
	t.Fatalf("unknown part %v", value)
	return nil
}

// specValue resolves `$repeat` strings anywhere in a plain JSON value.
func specValue(value any) any {
	switch typed := value.(type) {
	case *delta.JsonObject:
		if typed.Has("$repeat") {
			return resolveString(typed)
		}
		resolved := delta.NewJsonObject(typed.Len())
		for key, child := range typed.All() {
			resolved.Set(key, specValue(child))
		}
		return resolved
	case []any:
		resolved := make([]any, len(typed))
		for index, child := range typed {
			resolved[index] = specValue(child)
		}
		return resolved
	}
	return value
}

func specInt(spec *delta.JsonObject, key string) int {
	value, _ := spec.Value(key).(float64)
	return int(value)
}

func specOptional(spec *delta.JsonObject, key string) link.Optional[int] {
	if value, ok := spec.Get(key); ok {
		return link.Some(int(value.(float64)))
	}
	return link.Optional[int]{}
}

func specStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(items))
	for index, item := range items {
		out[index] = item.(string)
	}
	return out
}

func specSegments(t testing.TB, spec *delta.JsonObject) [][]byte {
	items, ok := spec.Value("segments").([]any)
	if !ok {
		return nil
	}
	out := make([][]byte, len(items))
	for index, item := range items {
		out[index] = hexBytes(t, item.(string))
	}
	return out
}

func specFailed(value any) []link.FailedMember {
	var failed []link.FailedMember
	for _, item := range value.([]any) {
		object := item.(*delta.JsonObject)
		failed = append(failed, link.FailedMember{Member: specInt(object, "member"), Error: specValue(object.Value("error")).(*delta.JsonObject)})
	}
	return failed
}

func specError(spec *delta.JsonObject, key string) link.WireError {
	return specValue(spec.Value(key)).(*delta.JsonObject)
}

// buildMessage is a message from its spec: the message of the TypeScript case.
func buildMessage(t testing.TB, spec *delta.JsonObject) link.Message {
	t.Helper()
	origin := specOptional(spec, "origin")
	switch kind := spec.Value("kind").(string); kind {
	case "hello":
		return &link.HelloMessage{Protocol: specInt(spec, "protocol"), Incarnation: spec.Value("incarnation").(string), Calls: specInt(spec, "calls")}
	case "hold":
		address := spec.Value("address").(*delta.JsonObject)
		message := &link.HoldMessage{
			Origin: origin, Hold: specInt(spec, "hold"), Type: resolveString(spec.Value("type")), Version: specInt(spec, "version"),
			Fingerprint: spec.Value("fingerprint").(string), Params: specPart(t, spec.Value("params")),
			Address: link.WireAddress{Kind: address.Value("kind").(string), Params: specValue(address.Value("params"))}, Limit: specOptional(spec, "limit"),
		}
		if principal, ok := spec.Value("principal").(*delta.JsonObject); ok {
			message.Principal = principal
		}
		message.Incarnation, _ = spec.Value("incarnation").(string)
		if chain, ok := spec.Get("chain"); ok {
			message.Chain = specStrings(chain)
			if message.Chain == nil {
				message.Chain = []string{}
			}
		}
		return message
	case "release":
		return &link.ReleaseMessage{Origin: origin, Hold: specInt(spec, "hold")}
	case "call":
		return &link.CallMessage{Origin: origin, Call: specInt(spec, "call"), Hold: specInt(spec, "hold"), Method: specInt(spec, "method"), Args: specPart(t, spec.Value("args")), Segments: specSegments(t, spec)}
	case "abort":
		return &link.AbortMessage{Origin: origin, Call: specInt(spec, "call")}
	case "retry":
		return &link.RetryMessage{Origin: origin, Hold: specInt(spec, "hold"), Hops: specInt(spec, "hops")}
	case "bound":
		message := &link.BoundMessage{Origin: origin}
		for _, item := range spec.Value("holds").([]any) {
			entry := item.(*delta.JsonObject)
			bound := link.BoundEntry{Hold: specInt(entry, "hold"), Variant: specInt(entry, "variant"), Failed: specFailed(entry.Value("failed"))}
			for _, member := range entry.Value("members").([]any) {
				object := member.(*delta.JsonObject)
				bound.Members = append(bound.Members, link.BoundMember{Member: specInt(object, "member"), Value: specPart(t, object.Value("value"))})
			}
			message.Holds = append(message.Holds, bound)
		}
		return message
	case "state":
		message := &link.StateMessage{Origin: origin}
		for _, item := range spec.Value("holds").([]any) {
			entry := item.(*delta.JsonObject)
			state := link.StateEntry{Hold: specInt(entry, "hold"), Failed: specFailed(entry.Value("failed")), Retired: link.NoRetired}
			for _, member := range entry.Value("members").([]any) {
				object := member.(*delta.JsonObject)
				state.Members = append(state.Members, link.StateMember{Member: specInt(object, "member"), Ops: specPart(t, object.Value("ops"))})
			}
			if retired := entry.Value("retired").([]any); len(retired) > 0 {
				state.Retired = nil
				for _, member := range retired {
					state.Retired = append(state.Retired, int(member.(float64)))
				}
			}
			message.Holds = append(message.Holds, state)
		}
		return message
	case "ack":
		return &link.AckMessage{Origin: origin}
	case "closed":
		return &link.ClosedMessage{Origin: origin.Value}
	case "revoke":
		return &link.RevokeMessage{Revocation: specValue(spec.Value("revocation"))}
	case "ping":
		return &link.PingMessage{}
	case "pong":
		return &link.PongMessage{}
	case "failed":
		cause := spec.Value("cause").(*delta.JsonObject)
		return &link.FailedMessage{Origin: origin, Hold: specInt(spec, "hold"), Cause: link.WireCause{Kind: cause.Value("kind").(string), Error: specError(cause, "error")}, RetryIn: specOptional(spec, "retryIn")}
	case "loading":
		return &link.LoadingMessage{Origin: origin, Hold: specInt(spec, "hold")}
	case "result":
		return &link.ResultMessage{Origin: origin, Call: specInt(spec, "call"), Value: specPart(t, spec.Value("value")), Segments: specSegments(t, spec)}
	case "error":
		return &link.ErrorMessage{Origin: origin, Call: specInt(spec, "call"), Error: specError(spec, "error")}
	case "gone":
		return &link.GoneMessage{Origin: origin, Hold: specInt(spec, "hold"), Error: specError(spec, "error")}
	default:
		t.Fatalf("unknown kind %q", kind)
		return nil
	}
}

func TestWireFormatsMatchPi(t *testing.T) {
	cases := loadGolden(t)
	if len(cases) < 80 {
		t.Fatalf("golden has %d cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			message := buildMessage(t, c.spec)
			binary := encode(t, link.BinaryCodec, message)
			c.binary.check(t, "binary", binary)
			text := encode(t, link.TextCodec, message)
			c.text.check(t, "text", text)
			requireCanon(t, decode(t, link.BinaryCodec, binary), message)
			requireCanon(t, decode(t, link.TextCodec, text), message)
		})
	}
}
