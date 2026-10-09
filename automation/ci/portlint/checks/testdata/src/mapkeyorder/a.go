package mapkeyorder

import (
	"bytes"
	"encoding/json"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type T struct{ A int }

func bad(input map[string]any) ([]byte, error) {
	return json.Marshal(input) // want `encoding map\[string\]any sorts keys`
}

func badEncoder(input map[string]string) error {
	var b bytes.Buffer
	return json.NewEncoder(&b).Encode(input) // want `sorts keys`
}

func good(t T) ([]byte, error) { return json.Marshal(t) }

func allowed(input map[string]any) ([]byte, error) {
	//portlint:allow mapkeyorder Pi sorts this object too
	return json.Marshal(input)
}

func badSDKCodec(input map[string]any) ([]byte, error) {
	return sdkjson.Marshal(input) // want `encoding map\[string\]any sorts keys`
}
