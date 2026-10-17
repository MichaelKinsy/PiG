package subprocess

import (
	"slices"
	"testing"
	"time"
)

// The backoff between the renders a proxy sends an extension whose renders the inactivity boundary keeps abandoning starts at the boundary, doubles with each abandonment in a row up to renderRetryMaxBackoff, and starts over after an answer. Only the first abandonment in a row lets the abandoned request's settling end the backoff early.
func TestRenderRetryBackoffDoublesUpToItsCap(t *testing.T) {
	var retry renderRetry
	settled := make(chan struct{})
	var got []time.Duration
	for i := range 7 {
		_, backoff, early := retry.abandoned(5*time.Second, settled)
		got = append(got, backoff)
		if (early != nil) != (i == 0) {
			t.Fatalf("abandonment %d: settling ends the backoff early = %t, want %t", i+1, early != nil, i == 0)
		}
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute, time.Minute}
	if !slices.Equal(got, want) {
		t.Fatalf("backoffs = %v, want %v", got, want)
	}
	retry.answered()
	if _, backoff, early := retry.abandoned(5*time.Second, settled); backoff != 5*time.Second || early == nil {
		t.Fatalf("after an answer: backoff %s, early %t; want the inactivity boundary again, ended early by settling", backoff, early != nil)
	}
}
