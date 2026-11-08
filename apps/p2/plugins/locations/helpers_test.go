package locations

import (
	"testing"

	envspkg "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

type jsonObject = delta.JsonObject

func mustObject(t *testing.T, text string) *jsonObject {
	t.Helper()
	value, err := chordjson.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*jsonObject)
}

func marshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func revisionOf(t *testing.T, configs *jsonObject, name string) string {
	t.Helper()
	return envspkg.EnvRevision(configs.Value(name).(*chordjson.Object))
}
