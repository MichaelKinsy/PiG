package interop

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// Ports packages/protocol/src/{protocol,codec,framing}.ts and packages/protocol/src/cbor/*.ts as a differential check:
// the Go package and the pinned Node package must accept and reject the same bytes, with the same errors, and
// re-encode every accepted message to the same bytes (testdata/protocol-oracle.mjs).

const (
	serverA  = "00000000-0000-4000-8000-000000000001"
	serverB  = "ffffffff-ffff-4fff-bfff-ffffffffffff"
	attachID = "attachment-1"
)

type oracleStep struct {
	Messages []string `json:"messages"`
	Error    string   `json:"error"`
	Ended    bool     `json:"ended"`
}

type oracleReply struct {
	Steps []oracleStep `json:"steps"`
	Hex   string       `json:"hex"`
	Error string       `json:"error"`
	Value bool         `json:"value"`
}

type decodeCase struct {
	Op     string   `json:"op"`
	Chunks []string `json:"chunks"`
	End    bool     `json:"end"`
}

type encodeCase struct {
	Op   string `json:"op"`
	JSON string `json:"json"`
}

type cborCase struct {
	Op  string `json:"op"`
	Hex string `json:"hex"`
}

type valueCase struct {
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// errorName mirrors the JavaScript `${error.name}: ${error.message}` the oracle prints.
func errorName(err error) string {
	if _, ok := errors.AsType[*protocol.ProtocolValidationError](err); ok {
		return "ProtocolValidationError: " + err.Error()
	}
	if _, ok := errors.AsType[*protocol.CborError](err); ok {
		return "CborError: " + err.Error()
	}
	if _, ok := errors.AsType[*protocol.FrameError](err); ok {
		return "FrameError: " + err.Error()
	}
	if _, ok := errors.AsType[*protocol.RangeError](err); ok {
		return "RangeError: " + err.Error()
	}
	return "Go: " + err.Error()
}

func frame(payload []byte) []byte {
	out := []byte{byte(len(payload) >> 24), byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload))}
	return append(out, payload...)
}

func goEncode(kind, text string) (string, string) {
	value, err := protocol.FromJSON(json.RawMessage(text))
	if err != nil {
		return "", errorName(err)
	}
	var data []byte
	if kind == "client" {
		message, perr := protocol.ParseClientMessage(value)
		if perr != nil {
			return "", errorName(perr)
		}
		data, err = protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	} else {
		message, perr := protocol.ParseServerMessage(value)
		if perr != nil {
			return "", errorName(perr)
		}
		data, err = protocol.EncodeServerMessage(message, protocol.FrameDecoderOptions{})
	}
	if err != nil {
		return "", errorName(err)
	}
	return hex.EncodeToString(data), ""
}

func goDecode(kind string, chunks []string, end bool) []oracleStep {
	var push func([]byte) ([]string, error)
	var finish func() error
	if kind == "client" {
		decoder, _ := protocol.NewClientMessageDecoder(protocol.FrameDecoderOptions{})
		push = func(chunk []byte) ([]string, error) {
			messages, err := decoder.Push(chunk)
			var out []string
			for _, message := range messages {
				data, eerr := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
				if eerr != nil {
					return nil, eerr
				}
				out = append(out, hex.EncodeToString(data))
			}
			return out, err
		}
		finish = decoder.End
	} else {
		decoder, _ := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
		push = func(chunk []byte) ([]string, error) {
			messages, err := decoder.Push(chunk)
			var out []string
			for _, message := range messages {
				data, eerr := protocol.EncodeServerMessage(message, protocol.FrameDecoderOptions{})
				if eerr != nil {
					return nil, eerr
				}
				out = append(out, hex.EncodeToString(data))
			}
			return out, err
		}
		finish = decoder.End
	}
	var steps []oracleStep
	for _, chunk := range chunks {
		raw, _ := hex.DecodeString(chunk)
		messages, err := push(raw)
		if err != nil {
			steps = append(steps, oracleStep{Error: errorName(err)})
			continue
		}
		if messages == nil {
			messages = []string{}
		}
		steps = append(steps, oracleStep{Messages: messages})
	}
	if end {
		if err := finish(); err != nil {
			steps = append(steps, oracleStep{Error: errorName(err)})
		} else {
			steps = append(steps, oracleStep{Ended: true})
		}
	}
	return steps
}

func goCbor(text string) (string, string) {
	raw, _ := hex.DecodeString(text)
	value, err := protocol.DecodeCbor(raw, protocol.CborOptions{})
	if err != nil {
		return "", errorName(err)
	}
	encoded, err := protocol.EncodeCbor(value, protocol.CborOptions{})
	if err != nil {
		return "", errorName(err)
	}
	return hex.EncodeToString(encoded), ""
}

// canonicalSteps re-expresses Node's re-encoded messages in Go's schema key order: Node re-emits the key order it received,
// a typed Go message always emits schema order. Both are the same CBOR map to every decoder.
func canonicalSteps(kind string, steps []oracleStep) []oracleStep {
	out := make([]oracleStep, len(steps))
	for i, step := range steps {
		out[i] = step
		out[i].Messages = nil
		for _, message := range step.Messages {
			out[i].Messages = append(out[i].Messages, goNormalize(kind, message))
		}
		if step.Messages != nil && out[i].Messages == nil {
			out[i].Messages = []string{}
		}
	}
	return out
}

func sameSteps(a, b []oracleStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Error != b[i].Error || a[i].Ended != b[i].Ended || strings.Join(a[i].Messages, ",") != strings.Join(b[i].Messages, ",") {
			return false
		}
	}
	return true
}

func target(session bool) string {
	if session {
		return fmt.Sprintf(`{"serverId":%q,"sessionId":"session-1","attachmentId":%q}`, serverA, attachID)
	}
	return fmt.Sprintf(`{"serverId":%q}`, serverB)
}

// clientJSON and serverJSON are valid and invalid protocol messages. The invalid ones probe every schema constraint.
func clientJSON() []string {
	call := `{"serviceId":"pi.session-management","member":"attach","args":["session-1"]}`
	rich := `{"serviceId":"s","member":"m","args":[null,true,false,0,-0,1,-1,1.5,-2.25,9007199254740991,-9007199254740991,1e21,1e-7,"é😀\u0000\u001f","",[],{},{"b":1,"a":2,"10":3,"2":4,"__proto__":5}],"instance":"i"}`
	return []string{
		`{"type":"hello","version":8}`, `{"type":"hello","version":0}`, `{"type":"hello","version":9}`, `{"type":"hello","version":4294967295}`,
		`{"type":"hello","version":9007199254740991}`, `{"type":"hello","version":8.0}`, `{"type":"hello","version":-1}`, `{"type":"hello","version":1.5}`,
		`{"type":"hello","version":"8"}`, `{"type":"hello"}`, `{"type":"hello","version":8,"extra":1}`, `{"version":8,"type":"hello"}`,
		`{"type":"request","id":"r1","target":` + target(false) + `,"call":` + call + `}`,
		`{"type":"request","id":"r2","target":` + target(true) + `,"call":` + rich + `}`,
		`{"type":"request","id":"r3","target":` + target(true) + `,"call":null}`,
		`{"type":"request","id":"r3","target":` + target(true) + `,"call":[1,2,3]}`,
		`{"type":"request","id":"r3","target":` + target(true) + `,"call":"x"}`,
		`{"type":"request","id":"","target":` + target(false) + `,"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"00000000-0000-1000-8000-000000000001"},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"00000000-0000-4000-c000-000000000001"},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"00000000-0000-4000-8000-00000000000G"},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"00000000-0000-4000-8000-00000000000A"},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"` + serverA + `","sessionId":"s"},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"` + serverA + `","sessionId":"","attachmentId":"a"},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"` + serverA + `","sessionId":"s","attachmentId":""},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"` + serverA + `","sessionId":"s","attachmentId":"a","x":1},"call":1}`,
		`{"type":"request","id":"r","target":{"serverId":"` + serverA + `","x":1},"call":1}`,
		`{"type":"request","id":"r","target":{},"call":1}`, `{"type":"request","id":"r","target":null,"call":1}`, `{"type":"request","id":"r","target":[],"call":1}`,
		`{"type":"request","id":"r","target":` + target(false) + `}`, `{"type":"request","id":"r","target":` + target(false) + `,"call":1,"extra":true}`,
		`{"type":"request","id":1,"target":` + target(false) + `,"call":1}`,
		`{"type":"cancel","id":"r1","target":` + target(false) + `}`, `{"type":"cancel","id":"r1","target":` + target(true) + `}`,
		`{"type":"cancel","id":"","target":` + target(true) + `}`, `{"type":"cancel","id":"r1","target":` + target(true) + `,"call":1}`,
		`{"type":"cancel","target":` + target(true) + `}`, `{"type":"nope"}`, `{"type":1}`, `{}`, `[]`, `null`, `1`, `"hello"`, `true`,
		`{"type":"request","id":"\ud800","target":` + target(false) + `,"call":1}`,
		`{"type":"request","id":"r","target":` + target(false) + `,"call":"\ud800"}`,
		`{"type":"request","id":"` + strings.Repeat("x", 70000) + `","target":` + target(false) + `,"call":1}`,
		`{"type":"request","id":"r","target":` + target(false) + `,"call":` + strings.Repeat("[", 70) + strings.Repeat("]", 70) + `}`,
		`{"type":"request","id":"r","target":` + target(false) + `,"call":` + strings.Repeat("[", 63) + strings.Repeat("]", 63) + `}`,
		`{"type":"request","id":"r","target":` + target(false) + `,"call":` + strings.Repeat("[", 62) + strings.Repeat("]", 62) + `}`,
		`{"type":"request","id":"r","target":` + target(false) + `,"call":{"é":1,"😀":2,"\u00e9":3,"a\u0000b":4}}`,
		`{"type":"request","id":"r","target":` + target(false) + `,"call":[1e308,-1e308,5e-324,0.1,123456789012345680000,4294967296,2147483648,65536,256,255,24,23,-24,-25,-256,-257,-65537,-4294967297]}`,
	}
}

func serverJSON() []string {
	return []string{
		`{"type":"hello","version":8,"serverId":"` + serverA + `"}`, `{"type":"hello","version":7,"serverId":"` + serverA + `"}`,
		`{"type":"hello","version":8,"serverId":"nope"}`, `{"type":"hello","version":8}`, `{"type":"hello","version":8,"serverId":"` + serverA + `","x":1}`,
		`{"type":"hello_error","error":{"code":"unsupported_version","message":"no"}}`, `{"type":"hello_error","error":{"code":"","message":"no"}}`,
		`{"type":"hello_error","error":{"code":"c","message":""}}`, `{"type":"hello_error","error":{"code":"c"}}`, `{"type":"hello_error","error":{"code":"c","message":"m","x":1}}`,
		`{"type":"hello_error"}`, `{"type":"hello_error","error":null}`,
		`{"type":"response","id":"r","ok":true}`, `{"type":"response","id":"r","ok":true,"result":null}`, `{"type":"response","id":"r","ok":true,"result":{"a":[1,2,{"b":null}]}}`,
		`{"type":"response","id":"r","ok":true,"result":"é😀"}`, `{"type":"response","id":"r","ok":true,"result":-0}`, `{"type":"response","id":"r","ok":true,"result":1e300}`,
		`{"type":"response","id":"r","ok":false,"error":{"code":"internal","message":"m"}}`, `{"type":"response","id":"r","ok":false}`,
		`{"type":"response","id":"r","ok":false,"error":{"code":"internal","message":"m"},"result":1}`, `{"type":"response","id":"r","ok":true,"error":{"code":"c","message":"m"}}`,
		`{"type":"response","id":"","ok":true}`, `{"type":"response","id":"r","ok":"true"}`, `{"type":"response","id":"r"}`, `{"type":"response","ok":true}`,
		`{"ok":true,"id":"r","type":"response"}`,
		`{"type":"service_update","subscriptionId":"service-1","update":{"kind":"x"}}`, `{"type":"service_update","subscriptionId":"service-1","update":null}`,
		`{"type":"service_update","subscriptionId":"","update":1}`, `{"type":"service_update","subscriptionId":"s"}`, `{"type":"service_update","subscriptionId":"s","update":1,"x":1}`,
		`{"type":"attachment","attachment":null}`, `{"type":"attachment","attachment":` + target(true) + `}`, `{"type":"attachment","attachment":` + target(false) + `}`,
		`{"type":"attachment"}`, `{"type":"attachment","attachment":{}}`, `{"type":"attachment","attachment":null,"x":1}`, `{"type":"nope"}`, `{}`, `[]`, `null`,
	}
}

// vectors are raw CBOR payloads (hex) exercising every major type, argument width, indefinite length, tag, float width and limit.
func vectors() []string {
	return []string{
		"00", "01", "17", "1818", "18ff", "190100", "19ffff", "1a00010000", "1affffffff", "1b0000000100000000", "1b001fffffffffffff", "1b0020000000000000", "1bffffffffffffffff",
		"1800", "1900ff", "1a0000ffff", "1b00000000ffffffff", "20", "37", "3818", "38ff", "390100", "3a00010000", "3b001fffffffffffff", "3b0020000000000000", "3bffffffffffffffff",
		"40", "4100", "5800", "5801ff", "60", "6161", "62c3a9", "64f09f9880", "78026162", "7800", "62c328", "61ff", "63e28028", "62eda080", "63eda080", "6c68656c6c6f", "6ff09f", "62c080", "63e08080",
		"80", "8100", "820102", "9800", "980101", "99000100", "9f00ff", "9fff", "7f6161ff", "5f4100ff", "bf6161ffff",
		"a0", "a1616101", "a2616101616202", "a2616101616101", "a1010101", "a10001", "a1f601", "a1f4f5", "a1613101", "a3613001613101613201", "a262313001613202",
		"f4", "f5", "f6", "f7", "f0", "f8ff", "f820", "f800", "e0", "ff", "fc",
		"f90000", "f98000", "f93c00", "f97c00", "f9fc00", "f97e00", "f97bff", "f90001", "f903ff", "f90400", "f9c400",
		"fa00000000", "fa80000000", "fa3f800000", "fa7f800000", "fa7fc00000", "faff800000", "fa7f7fffff", "fa00000001", "fa47c35000", "fa3dcccccd",
		"fb0000000000000000", "fb8000000000000000", "fb3ff0000000000000", "fb3ff8000000000000", "fb7ff0000000000000", "fb7ff8000000000000", "fbfff0000000000000", "fb7fefffffffffffff", "fb0000000000000001", "fb4415af1d78b58c40", "fb3fb999999999999a", "fb4340000000000000", "fbc340000000000000", "fb433fffffffffffff", "fb4330000000000000",
		"c000", "c1fb41d0000000000000", "d8186161", "d9d9f700", "dbffffffffffffffff00", "c200", "c24100",
		"8301020304", "81", "a1", "78", "5a", "1a", "1b00", "9b0000000000000001", "bb0000000000000001", "5bffffffffffffffff", "7bffffffffffffffff", "9affffffff", "baffffffff", "5affffffff", "7affffffff", "9a000f4240", "9a000f4241", "ba000f4240", "ba000f4241",
		"0000", "00ff", "8100ff", "a1616101ff",
		"6161" + "00", "6161" + "6162",
		strings.Repeat("81", 63) + "00", strings.Repeat("81", 64) + "00", strings.Repeat("81", 65) + "00", strings.Repeat("a16161", 64) + "00", strings.Repeat("a16161", 65) + "00",
		"a2" + "6161" + "00" + "6161" + "01", "a2" + "6161" + "01" + "6162" + "02", "a2" + "6162" + "01" + "6161" + "02", "a2" + "613a" + "01" + "6141" + "02",
		"a2" + "6a5f5f70726f746f5f5f" + "01" + "6161" + "02", "a1" + "6a5f5f70726f746f5f5f" + "a0", "a1" + "6b636f6e7374727563746f72" + "01", "a2" + "613130" + "01" + "613200" + "02",
		"a3" + "623130" + "01" + "62" + "3039" + "02" + "6234" + "3239" + "03",
		"a2" + "6a34323934393637323935" + "01" + "6a34323934393637323934" + "02", "a2" + "6b34323934393637323936" + "01" + "6a34323934393637323935" + "02",
		"a2" + "6130" + "01" + "623031" + "02", "a2" + "613101" + "6130" + "02",
		"a3" + "623130" + "01" + "6132" + "02" + "6161" + "03",
	}
}

func hexBytes(raw []byte) string { return hex.EncodeToString(raw) }

// mutations flips, truncates and extends a valid frame the way a hostile or buggy peer could.
func mutations(valid []byte) [][]byte {
	var out [][]byte
	for i := range valid {
		for _, replacement := range []byte{0x00, 0xff, valid[i] ^ 0x01, valid[i] ^ 0x80} {
			if replacement == valid[i] {
				continue
			}
			mutated := append([]byte(nil), valid...)
			mutated[i] = replacement
			out = append(out, mutated)
		}
	}
	for i := range valid {
		out = append(out, append([]byte(nil), valid[:i]...))
	}
	out = append(out, append(append([]byte(nil), valid...), 0x00), append(append([]byte(nil), valid...), valid...), append(append([]byte(nil), valid...), valid[:3]...))
	return out
}

func TestProtocolDifferential(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"client", "server"} {
		messages := clientJSON()
		if kind == "server" {
			messages = serverJSON()
		}
		t.Run("encode "+kind, func(t *testing.T) {
			t.Parallel()
			requests := make([]any, len(messages))
			for i, message := range messages {
				requests[i] = encodeCase{Op: "encode" + strings.ToUpper(kind[:1]) + kind[1:], JSON: message}
			}
			replies := oracleLines[oracleReply](t, "protocol-oracle.mjs", requests)
			var bad mismatches
			for i, message := range messages {
				gotHex, gotErr := goEncode(kind, message)
				short := message
				if len(short) > 160 {
					short = short[:160] + "…"
				}
				if gotErr == replies[i].Error && gotHex != replies[i].Hex && goNormalize(kind, replies[i].Hex) == gotHex {
					// Node re-emits an input object's own key order; a Go message is a typed value and always emits schema order.
					continue
				}
				if gotHex != replies[i].Hex || gotErr != replies[i].Error {
					bad.add("encode %s %s\n  go:   hex=%.80s err=%s\n  node: hex=%.80s err=%s", kind, short, gotHex, gotErr, replies[i].Hex, replies[i].Error)
				}
			}
			bad.report(t, len(messages))
		})
		t.Run("decode "+kind, func(t *testing.T) {
			t.Parallel()
			var cases []decodeCase
			// Base frames come from the pinned Node encoder, so they keep the input's key order.
			encodes := make([]any, len(messages))
			for i, message := range messages {
				encodes[i] = encodeCase{Op: "encode" + strings.ToUpper(kind[:1]) + kind[1:], JSON: message}
			}
			for _, reply := range oracleLines[oracleReply](t, "protocol-oracle.mjs", encodes) {
				hexFrame := reply.Hex
				if hexFrame == "" {
					continue
				}
				valid, _ := hex.DecodeString(hexFrame)
				cases = append(cases, decodeCase{Chunks: []string{hexFrame}, End: true})
				if len(valid) > 600 {
					continue
				}
				for split := 1; split < len(valid); split++ {
					cases = append(cases, decodeCase{Chunks: []string{hexBytes(valid[:split]), hexBytes(valid[split:])}, End: true})
				}
				cases = append(cases, decodeCase{Chunks: []string{hexFrame, hexFrame}}, decodeCase{Chunks: []string{hexFrame + hexFrame[:6], hexFrame[6:]}, End: true})
				for _, mutated := range mutations(valid) {
					cases = append(cases, decodeCase{Chunks: []string{hexBytes(mutated)}, End: true})
				}
			}
			for _, raw := range vectors() {
				payload, err := hex.DecodeString(raw)
				if err != nil {
					t.Fatalf("vector %q: %v", raw, err)
				}
				cases = append(cases, decodeCase{Chunks: []string{hexBytes(frame(payload))}, End: true})
			}
			// Frame-level cases independent of the payload.
			for _, raw := range []string{"", "00", "0000", "000000", "00000000", "0000000100", "00000001", "ffffffff", "01000001", "01000000", "00000000ff", "000000010000000100"} {
				cases = append(cases, decodeCase{Chunks: []string{raw}, End: true}, decodeCase{Chunks: []string{raw, raw}, End: true})
			}
			for i := range cases {
				cases[i].Op = "decode" + strings.ToUpper(kind[:1]) + kind[1:]
			}
			requests := make([]any, len(cases))
			for i := range cases {
				requests[i] = cases[i]
			}
			replies := oracleLines[oracleReply](t, "protocol-oracle.mjs", requests)
			var bad mismatches
			for i, c := range cases {
				got := goDecode(kind, c.Chunks, c.End)
				if !sameSteps(got, canonicalSteps(kind, replies[i].Steps)) {
					bad.add("decode %s chunks=%.120v\n  go:   %+v\n  node: %+v", kind, c.Chunks, got, replies[i].Steps)
				}
			}
			bad.report(t, len(cases))
		})
	}
	t.Run("cbor", func(t *testing.T) {
		t.Parallel()
		inputs := vectors()
		requests := make([]any, len(inputs))
		for i, input := range inputs {
			requests[i] = cborCase{Op: "cbor", Hex: input}
		}
		replies := oracleLines[oracleReply](t, "protocol-oracle.mjs", requests)
		var bad mismatches
		for i, input := range inputs {
			gotHex, gotErr := goCbor(input)
			if gotHex != replies[i].Hex || gotErr != replies[i].Error {
				bad.add("cbor %.100s\n  go:   hex=%.80s err=%s\n  node: hex=%.80s err=%s", input, gotHex, gotErr, replies[i].Hex, replies[i].Error)
			}
		}
		bad.report(t, len(inputs))
	})
	t.Run("predicates", func(t *testing.T) {
		t.Parallel()
		type probe struct {
			op    string
			value any
			got   bool
		}
		var probes []probe
		for _, version := range []float64{8, 0, 7, 9, 8.5, -8, 1e300} {
			probes = append(probes, probe{"version", version, protocol.IsSupportedProtocolVersion(version)})
		}
		for _, id := range []string{serverA, serverB, "", "00000000-0000-4000-8000-00000000000a", "00000000-0000-4000-8000-00000000000A", "00000000-0000-5000-8000-000000000001", serverA + "\n", "\n" + serverA, " " + serverA} {
			probes = append(probes, probe{"serverId", id, protocol.IsServerId(id)})
		}
		requests := make([]any, len(probes))
		for i, p := range probes {
			requests[i] = valueCase{Op: p.op, Value: p.value}
		}
		replies := oracleLines[oracleReply](t, "protocol-oracle.mjs", requests)
		for i, p := range probes {
			if p.got != replies[i].Value {
				t.Errorf("%s(%v): go=%v node=%v", p.op, p.value, p.got, replies[i].Value)
			}
		}
	})
}

// goNormalize decodes a frame and re-encodes it as Go does.
func goNormalize(kind, frameHex string) string {
	steps := goDecode(kind, []string{frameHex}, false)
	if len(steps) != 1 || len(steps[0].Messages) != 1 {
		return ""
	}
	return steps[0].Messages[0]
}
