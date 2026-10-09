package jsonescape

import (
	"bytes"
	"encoding/json"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type T struct{ A string }

func bad(t T) string {
	b, _ := json.Marshal(t) // want `string\(json.Marshal\(...\)\) escapes`
	return string(b)
}

func badEncoder(t T) []byte {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(t) // want `NewEncoder without SetEscapeHTML\(false\)`
	return buf.Bytes()
}

func goodEncoder(t T) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(t)
	return buf.Bytes()
}

func bytesOnly(t T) []byte {
	b, _ := json.Marshal(t)
	return b
}

func badIndent(t T) []byte {
	b, _ := json.MarshalIndent(t, "", "  ") // want `json.MarshalIndent escapes`
	return b
}

func badSDKCodec(t T) string {
	b, _ := sdkjson.Marshal(t) // want `string\(json.Marshal\(...\)\) escapes`
	return string(b)
}

func badSDKIndent(t T) []byte {
	b, _ := sdkjson.MarshalIndent(t, "", "  ") // want `json.MarshalIndent escapes`
	return b
}

func badSDKEncoder(t T) []byte {
	var buf bytes.Buffer
	_ = sdkjson.NewEncoder(&buf).Encode(t) // want `NewEncoder without SetEscapeHTML\(false\)`
	return buf.Bytes()
}

func goodSDKEncoder(t T) []byte {
	var buf bytes.Buffer
	enc := sdkjson.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(t)
	return buf.Bytes()
}

func badIndentText(t T) string {
	b, _ := json.MarshalIndent(t, "", "  ") // want `json.MarshalIndent escapes`
	return string(b)
}
