package main

import (
	"go/types"
	"testing"
)

// TestObjectLiteralMethodMemberNamesItsIdentifier: the member `release(context: Context): void` of an upstream object literal declares
// release, not the text up to the first colon inside its parameter list, so it agrees with an interface method Release; a missing
// method still leaves the row undecided.
func TestObjectLiteralMethodMemberNamesItsIdentifier(t *testing.T) {
	gt := goTypes(t, `
type Attachment interface {
	InvokeService() error
	Release() error
}
type Missing interface{ InvokeService() error }
var (
	AV Attachment
	MV Missing
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "server"}
	up := `{ invokeService: TestHarness["invokeService"]; release(context: Context): void; }`
	if got := c.literalAgainstInterface(up, gt["AV"].Underlying().(*types.Interface)); got.ok != yes {
		t.Errorf("method member vs Release = %v (%s), want yes", got.ok, got.why)
	}
	if got := c.literalAgainstInterface(up, gt["MV"].Underlying().(*types.Interface)); got.ok == yes {
		t.Errorf("method member with no Go method = yes, want not yes")
	}
	for in, want := range map[string]string{"release(context: Context): void": "release", "a?: string": "a", "m<T>(x: T): T": "m", " plain": "plain"} {
		if got := memberName(in); got != want {
			t.Errorf("memberName(%q) = %q, want %q", in, got, want)
		}
	}
}
