package chordctx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Runs Pi's createContextKey and withContextValue (context/index.ts:7-60) and PiG's NewKey, Key.Token and WithValue over the same
// descriptions: a key's token carries the description it was created with, equals only itself (two keys with one description are
// two identities, and one's value is invisible under the other), and the layered context prints `.WithValue(<token description>)`.
func TestContextKeyTokenMatchesPi(t *testing.T) {
	descriptions := []string{"request-id", "", "with space", "unicode \u00e9\U0001F600", "request-id"}
	payload, _ := json.Marshal(map[string]any{"descriptions": descriptions})
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/context_key_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi struct {
		Tokens []struct {
			Description            string `json:"description"`
			SameAsItself           bool   `json:"sameAsItself"`
			SameAsEqualDescription bool   `json:"sameAsEqualDescription"`
			ValueUnderOtherKey     any    `json:"valueUnderOtherKey"`
		} `json:"tokens"`
		Layered string `json:"layered"`
	}
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatal(err)
	}
	keys := make([]Key[int], len(descriptions))
	ctx := Background()
	for i, description := range descriptions {
		keys[i] = NewKey[int](description)
		ctx = WithValue(ctx, keys[i], i)
		again := NewKey[int](description)
		want := pi.Tokens[i]
		token := keys[i].Token()
		if token.Description() != want.Description {
			t.Errorf("%q: token description %q, Pi %q", description, token.Description(), want.Description)
		}
		if (token == keys[i].Token()) != want.SameAsItself || (token == again.Token()) != want.SameAsEqualDescription {
			t.Errorf("%q: token identity differs from Pi's (itself %v, equal description %v)", description, token == keys[i].Token(), token == again.Token())
		}
		if _, ok := Value(WithValue(Background(), keys[i], 1), again); ok != (want.ValueUnderOtherKey != nil) {
			t.Errorf("%q: a value is visible under an equal-description key: PiG %v, Pi %v", description, ok, want.ValueUnderOtherKey)
		}
	}
	if got := fmt.Sprint(ctx); got != pi.Layered {
		t.Errorf("layered context prints %q, Pi %q", got, pi.Layered)
	}
}
