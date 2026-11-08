package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzObjectMembersMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if !jsonseed.IsObject(data) || !json.Valid(data) {
			t.Skip("objectMembers is called with a complete object and never checked the closing token")
		}
		got, gotErr := objectMembers(data)
		want, wantErr := objectMembersV1(data)
		jsonseed.Same(t, data, "objectMembers", got, want, gotErr, wantErr)
	})
}

func BenchmarkObjectMembers(b *testing.B) {
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { _, _ = objectMembersV1(d) }, func(d []byte) { _, _ = objectMembers(d) })
}
