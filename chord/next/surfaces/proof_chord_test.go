package surfaces

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Proof of locale-choice.ts: a language tag may be 35 characters, not 36 (its practical bound), whatever its segments.

func TestLocaleChoiceTakesATagOf35CharactersAndRefusesOneOf36(t *testing.T) {
	facet, err := LocaleChoiceFacet()
	if err != nil {
		t.Fatal(err)
	}
	clients, closeAll := serverWith(t, ":memory:", facet, "ada")
	defer closeAll()
	choice, err := clients[0].Get(bg, LocaleChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer choice.Release()
	const at35, at36 = "abcdefgh-12345678-12345678-123456-1", "abcdefgh-12345678-12345678-1234567-1"
	if len(at35) != 35 || len(at36) != 36 {
		t.Fatalf("the fixtures are %d and %d characters", len(at35), len(at36))
	}
	if _, err := choice.Call("choose", at35, bg); err != nil {
		t.Fatalf("choose of a 35-character tag: %v", err)
	}
	if got := pickOf(t, choice); got != at35 {
		t.Fatalf("pick = %v, want %s", got, at35)
	}
	_, err = choice.Call("choose", at36, bg)
	requireInvalidArgs(t, err)
}
