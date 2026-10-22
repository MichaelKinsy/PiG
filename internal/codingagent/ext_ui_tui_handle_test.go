package codingagent

import (
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// D193: a click on an OSC 8 link of a claimed scheme goes to the extension and opens nothing; any other address
// reaches the opener as before.
func TestLinkSchemesTakeTheClicksOnAClaimedScheme(t *testing.T) {
	var s linkSchemes
	var got []string
	release, err := s.register("stretchtext", "stretchtext", func(url string) { got = append(got, url) })
	if err != nil {
		t.Fatal(err)
	}
	if !s.dispatch("stretchtext:7") {
		t.Fatal("a click on a claimed scheme was not taken")
	}
	if !s.dispatch("STRETCHTEXT:8") {
		t.Fatal("the scheme is not case sensitive in an address")
	}
	for _, url := range []string{"https://example.com/", "file:///etc/hosts", "mailto:a@b", "plain", ":x", ""} {
		if s.dispatch(url) {
			t.Fatalf("%q was taken without a claim", url)
		}
	}
	if want := []string{"stretchtext:7", "STRETCHTEXT:8"}; !slices.Equal(got, want) {
		t.Fatalf("handler got %q, want %q", got, want)
	}
	release()
	if s.dispatch("stretchtext:9") {
		t.Fatal("a released scheme still took a click")
	}
	release()
}

func TestLinkSchemesRefuseASchemeAnotherOwnerHolds(t *testing.T) {
	var s linkSchemes
	if _, err := s.register("a", "x-scheme", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.register("b", "x-scheme", func(string) {}); !errors.Is(err, extension.ErrLinkSchemeInUse) {
		t.Fatalf("second owner's claim = %v, want ErrLinkSchemeInUse", err)
	}
}

func TestLinkSchemesReplaceTheHandlerOfTheSameOwner(t *testing.T) {
	var s linkSchemes
	var first, second int
	releaseFirst, _ := s.register("a", "x-scheme", func(string) { first++ })
	if _, err := s.register("a", "x-scheme", func(string) { second++ }); err != nil {
		t.Fatal(err)
	}
	s.dispatch("x-scheme:1")
	if first != 0 || second != 1 {
		t.Fatalf("handlers ran %d and %d times, want 0 and 1", first, second)
	}
	// The replaced claim's release must not take the scheme away from the claim that replaced it.
	releaseFirst()
	if !s.dispatch("x-scheme:2") || second != 2 {
		t.Fatal("releasing the replaced claim dropped the new one")
	}
}

func TestLinkSchemesRefuseAReservedScheme(t *testing.T) {
	var s linkSchemes
	for _, scheme := range []string{"https", "http", "file", "mailto", "Bad", ""} {
		if _, err := s.register("a", scheme, func(string) {}); err == nil {
			t.Fatalf("%q was claimed", scheme)
		}
	}
}
