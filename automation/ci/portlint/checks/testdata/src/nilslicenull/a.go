package nilslicenull

import (
	"encoding/json"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func bad(active []string) ([]byte, error) {
	return json.Marshal(active) // want `a nil slice becomes null`
}

func good(active []string) ([]byte, error) {
	return json.Marshal(struct{ A []string }{active})
}

func raw(b []byte) ([]byte, error) { return json.Marshal(b) }

func badSDKCodec(active []string) ([]byte, error) {
	return sdkjson.Marshal(active) // want `a nil slice becomes null`
}
