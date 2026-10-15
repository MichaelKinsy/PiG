package chordctx

import "testing"

// context/index.ts:58-59 createContextKey(description): the key's token is Symbol(description), so the description survives on the key and
// two keys with the same description are still distinct identities.
// mutation-checked: a NewKey that drops the description fails the first check.
func TestNewKeyKeepsItsDescriptionAndStaysDistinctFromEqualDescriptions(t *testing.T) {
	first, second := NewKey[int]("request-id"), NewKey[int]("request-id")
	if first.Description() != "request-id" || second.Description() != "request-id" {
		t.Fatalf("descriptions = %q, %q; want request-id", first.Description(), second.Description())
	}
	ctx := WithValue(t.Context(), first, 7)
	if _, ok := Value(ctx, second); ok {
		t.Fatal("a key with an equal description read another key's value")
	}
}
