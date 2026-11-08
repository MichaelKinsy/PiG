package status_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func marshal(t *testing.T, value delta.JsonValue) string {
	t.Helper()
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func asPlugin(err error, target **data.PluginError) bool { return errors.As(err, target) }
