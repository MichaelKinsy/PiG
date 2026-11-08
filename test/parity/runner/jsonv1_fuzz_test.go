//go:build parity

package runner

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzParseJSONRecordsMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		got, gotErr := parseJSONRecords(string(data))
		want, wantErr := parseJSONRecordsV1(string(data))
		jsonseed.Same(t, data, "parseJSONRecords", got, want, gotErr, wantErr)
	})
}
