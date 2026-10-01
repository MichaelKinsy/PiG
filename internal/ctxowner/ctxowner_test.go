package ctxowner

import (
	"context"
	"testing"
)

type key struct{ name string }

func TestWithValuesOfKeepsOwnerLifetimeAndPrefersOwnerValues(t *testing.T) {
	owner, stopOwner := context.WithCancel(context.WithValue(t.Context(), key{"shared"}, "owner"))
	call, endCall := context.WithCancel(context.WithValue(context.WithValue(t.Context(), key{"shared"}, "call"), key{"request"}, "r1"))
	joined := WithValuesOf(owner, call)

	endCall()
	if joined.Err() != nil {
		t.Fatalf("ending the call ended the joined context: %v", joined.Err())
	}
	if got := joined.Value(key{"request"}); got != "r1" {
		t.Fatalf("request value = %v, want r1", got)
	}
	if got := joined.Value(key{"shared"}); got != "owner" {
		t.Fatalf("shared value = %v, want the owner's", got)
	}
	if joined.Value(key{"absent"}) != nil {
		t.Fatal("absent key found a value")
	}
	stopOwner()
	<-joined.Done()
	if joined.Err() != context.Canceled {
		t.Fatalf("Err = %v after the owner ended, want context.Canceled", joined.Err())
	}
}
