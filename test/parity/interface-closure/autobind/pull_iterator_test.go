package main

import "testing"

// T14p: an AsyncIterable<T> is the Go pull iterator Next(ctx) (T, bool, error) with Close() error. Every part of the pair is
// required, and the element type is judged against Pi's.
func TestT14pPullIteratorIsTheGoFormOfAnAsyncIterable(t *testing.T) {
	gt := goTypes(t, `
import "context"
type Good struct{}
func (*Good) Next(ctx context.Context) (string, bool, error) { return "", false, nil }
func (*Good) Close() error { return nil }
type GoodInterface interface {
	Next(ctx context.Context) (string, bool, error)
	Close() error
}
type NoClose struct{}
func (*NoClose) Next(ctx context.Context) (string, bool, error) { return "", false, nil }
type NoNext struct{}
func (*NoNext) Close() error { return nil }
type CloseNoError struct{}
func (*CloseNoError) Next(ctx context.Context) (string, bool, error) { return "", false, nil }
func (*CloseNoError) Close() {}
type NoContext struct{}
func (*NoContext) Next() (string, bool, error) { return "", false, nil }
func (*NoContext) Close() error { return nil }
type NotBool struct{}
func (*NotBool) Next(ctx context.Context) (string, int, error) { return "", 0, nil }
func (*NotBool) Close() error { return nil }
type NoErrorResult struct{}
func (*NoErrorResult) Next(ctx context.Context) (string, bool) { return "", false }
func (*NoErrorResult) Close() error { return nil }
type WrongParam struct{}
func (*WrongParam) Next(n int) (string, bool, error) { return "", false, nil }
func (*WrongParam) Close() error { return nil }
type Unexported struct{}
func (*Unexported) next(ctx context.Context) (string, bool, error) { return "", false, nil }
func (*Unexported) close() error { return nil }
`)
	c := &checker{pkg: "p", renames: map[string]string{}, generics: map[string]bool{}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"AsyncIterable<string>", "Good", yes},
		{"AsyncIterable<string>", "GoodInterface", yes},
		{"AsyncIterable<number>", "Good", no}, // the element type is judged
		{"AsyncIterable<string>", "NoClose", unknown},
		{"AsyncIterable<string>", "NoNext", unknown},
		{"AsyncIterable<string>", "CloseNoError", unknown},
		{"AsyncIterable<string>", "NoContext", unknown},
		{"AsyncIterable<string>", "NotBool", unknown},
		{"AsyncIterable<string>", "NoErrorResult", unknown},
		{"AsyncIterable<string>", "WrongParam", unknown},
		{"AsyncIterable<string>", "Unexported", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// T12c: a chord Context member of an options object is the context.Context of the call that takes it, only inside such a call,
// only for the chord Context, and only when the Go struct has no field of its own for it.
func TestT12cContextMemberOfAnOptionsObjectIsTheCallsContext(t *testing.T) {
	gt := goTypes(t, `
import "context"
type Options struct{ ID string }
type WithField struct {
	ID      string
	Context context.Context
}
type Missing struct{ Other string }
`)
	const up = "{ id: string; context: Context }"
	chord := func() *checker {
		return &checker{pkg: "chord", renames: map[string]string{}, generics: map[string]bool{}}
	}
	inCall := chord()
	inCall.ctxInScope = true
	if got := inCall.agree(up, gt["Options"]); got.ok != yes {
		t.Errorf("inside a call with a context: %v (%s), want yes", got.ok, got.why)
	}
	if got := chord().agree(up, gt["Options"]); got.ok == yes {
		t.Errorf("outside a call that takes a context.Context the member has no Go form, got %v", got.ok)
	}
	plain := chord()
	plain.ctxInScope, plain.plainContext = true, true
	if got := plain.agree(up, gt["Options"]); got.ok == yes {
		t.Errorf("a Context read as an ordinary value is no call context: got %v", got.ok)
	}
	other := &checker{pkg: "p", renames: map[string]string{}, generics: map[string]bool{}, ctxInScope: true}
	if got := other.agree(up, gt["Options"]); got.ok == yes {
		t.Errorf("a package whose Context is not chord's invocation Context: got %v", got.ok)
	}
	if got := inCall.agree("{ id: string; context: Signal }", gt["Options"]); got.ok == yes {
		t.Errorf("a member that is not the Context type is no call context: got %v", got.ok)
	}
	if got := inCall.agree(up, gt["Missing"]); got.ok == yes {
		t.Errorf("the other members are still judged: got %v", got.ok)
	}
	if got := inCall.agree(up, gt["WithField"]); got.ok != yes {
		t.Errorf("a Go field for the member is judged by its type: %v (%s), want yes", got.ok, got.why)
	}
}
