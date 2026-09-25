package ai

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestPollDeviceCode_ImmediateComplete(t *testing.T) {
	calls := 0
	val, err := PollOAuthDeviceCodeFlow(context.Background(), DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(2)),
		ExpiresInSeconds: new(float64(30)),
		Poll: func() (DeviceCodePollResult[string], error) {
			calls++
			return DeviceCodePollResult[string]{Status: DevicePollComplete, Value: "token"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if val != "token" {
		t.Fatalf("val = %q, want token", val)
	}
	if calls != 1 {
		t.Fatalf("polled %d times, want 1 (no sleep before first poll)", calls)
	}
}

func TestPollDeviceCode_PendingThenComplete(t *testing.T) {
	calls := 0
	start := time.Now()
	// interval 0 clamps to the 1000ms minimum, so one sleep elapses.
	val, err := PollOAuthDeviceCodeFlow(context.Background(), DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(0)),
		ExpiresInSeconds: new(float64(30)),
		Poll: func() (DeviceCodePollResult[string], error) {
			calls++
			if calls == 1 {
				return DeviceCodePollResult[string]{Status: DevicePollPending}, nil
			}
			return DeviceCodePollResult[string]{Status: DevicePollComplete, Value: "tok2"}, nil
		},
	})
	if err != nil || val != "tok2" || calls != 2 {
		t.Fatalf("val=%q err=%v calls=%d", val, err, calls)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("expected a >=1s clamped interval between polls, got %v", elapsed)
	}
}

func TestPollDeviceCode_CancelInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(5)),
		ExpiresInSeconds: new(float64(30)),
		Poll: func() (DeviceCodePollResult[string], error) {
			return DeviceCodePollResult[string]{Status: DevicePollPending}, nil
		},
	})
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("err = %v, want Login cancelled", err)
	}
}

func TestPollDeviceCode_PreCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(5)),
		ExpiresInSeconds: new(float64(30)),
		Poll: func() (DeviceCodePollResult[string], error) {
			t.Fatal("poll should not run when ctx is already cancelled")
			return DeviceCodePollResult[string]{}, nil
		},
	})
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("err = %v, want Login cancelled", err)
	}
}

func TestPollDeviceCode_Failed(t *testing.T) {
	_, err := PollOAuthDeviceCodeFlow(context.Background(), DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(2)),
		ExpiresInSeconds: new(float64(30)),
		Poll: func() (DeviceCodePollResult[string], error) {
			return DeviceCodePollResult[string]{Status: DevicePollFailed, Message: "boom"}, nil
		},
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestPollDeviceCode_PollError(t *testing.T) {
	sentinel := errors.New("network down")
	_, err := PollOAuthDeviceCodeFlow(context.Background(), DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(2)),
		ExpiresInSeconds: new(float64(30)),
		Poll: func() (DeviceCodePollResult[string], error) {
			return DeviceCodePollResult[string]{}, sentinel
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestPollDeviceCode_Timeout(t *testing.T) {
	_, err := PollOAuthDeviceCodeFlow(context.Background(), DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(0)),
		ExpiresInSeconds: new(0.4),
		Poll: func() (DeviceCodePollResult[string], error) {
			return DeviceCodePollResult[string]{Status: DevicePollPending}, nil
		},
	})
	if err == nil || err.Error() != "Device flow timed out" {
		t.Fatalf("err = %v, want Device flow timed out", err)
	}
}

func TestPollDeviceCode_SlowDownTimeoutMessage(t *testing.T) {
	_, err := PollOAuthDeviceCodeFlow(context.Background(), DeviceCodePollOptions[string]{
		IntervalSeconds:  new(float64(0)),
		ExpiresInSeconds: new(0.4),
		Poll: func() (DeviceCodePollResult[string], error) {
			return DeviceCodePollResult[string]{Status: DevicePollSlowDown}, nil
		},
	})
	if err == nil || err.Error() != deviceCodeSlowDownMessage {
		t.Fatalf("err = %v, want slow-down timeout message", err)
	}
}

// pollTimesUnderFakeClock runs the poll loop inside a synctest bubble, whose
// clock only advances while every goroutine waits, and records each poll's
// offset from the start.
func pollTimesUnderFakeClock(t *testing.T, opts DeviceCodePollOptions[string], results []DeviceCodePollResult[string]) ([]time.Duration, string, error) {
	t.Helper()
	var offsets []time.Duration
	var value string
	var err error
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		opts.Poll = func() (DeviceCodePollResult[string], error) {
			offsets = append(offsets, time.Since(start))
			if len(results) == 0 {
				t.Fatal("unexpected extra poll")
			}
			next := results[0]
			results = results[1:]
			return next, nil
		}
		value, err = PollOAuthDeviceCodeFlow(context.Background(), opts)
	})
	return offsets, value, err
}

func TestPollDeviceCode_PollsImmediatelyThenAfterInterval(t *testing.T) {
	offsets, value, err := pollTimesUnderFakeClock(t, DeviceCodePollOptions[string]{
		IntervalSeconds: new(float64(2)), ExpiresInSeconds: new(float64(30)),
	}, []DeviceCodePollResult[string]{{Status: DevicePollPending}, {Status: DevicePollComplete, Value: "token"}})
	if err != nil || value != "token" || !slices.Equal(offsets, []time.Duration{0, 2 * time.Second}) {
		t.Fatalf("value=%q err=%v offsets=%v, want polls at 0s and 2s", value, err, offsets)
	}
}

func TestPollDeviceCode_CanWaitBeforeFirstPoll(t *testing.T) {
	offsets, value, err := pollTimesUnderFakeClock(t, DeviceCodePollOptions[string]{
		IntervalSeconds: new(float64(2)), ExpiresInSeconds: new(float64(30)), WaitBeforeFirstPoll: true,
	}, []DeviceCodePollResult[string]{{Status: DevicePollComplete, Value: "token"}})
	if err != nil || value != "token" || !slices.Equal(offsets, []time.Duration{2 * time.Second}) {
		t.Fatalf("value=%q err=%v offsets=%v, want one poll at 2s", value, err, offsets)
	}
}

func TestPollDeviceCode_SlowDownWithoutServerIntervalAddsFiveSeconds(t *testing.T) {
	offsets, value, err := pollTimesUnderFakeClock(t, DeviceCodePollOptions[string]{
		IntervalSeconds: new(float64(2)), ExpiresInSeconds: new(float64(900)),
	}, []DeviceCodePollResult[string]{{Status: DevicePollSlowDown}, {Status: DevicePollComplete, Value: "token"}})
	if err != nil || value != "token" || !slices.Equal(offsets, []time.Duration{0, 7 * time.Second}) {
		t.Fatalf("value=%q err=%v offsets=%v, want polls at 0s and 7s", value, err, offsets)
	}
}

func TestPollDeviceCode_HonorsServerSlowDownInterval(t *testing.T) {
	offsets, value, err := pollTimesUnderFakeClock(t, DeviceCodePollOptions[string]{
		IntervalSeconds: new(float64(2)), ExpiresInSeconds: new(float64(900)),
	}, []DeviceCodePollResult[string]{{Status: DevicePollSlowDown, IntervalSeconds: new(float64(30))}, {Status: DevicePollComplete, Value: "token"}})
	if err != nil || value != "token" || !slices.Equal(offsets, []time.Duration{0, 30 * time.Second}) {
		t.Fatalf("value=%q err=%v offsets=%v, want polls at 0s and 30s", value, err, offsets)
	}
}

// abortInFlight cancels the owning operation after one synthetic second and
// holds the request until its context ends, like an aborted in-flight fetch.
// The request context ends only when the owner's cancellation reaches it;
// otherwise it runs to the 30s request timeout.
func abortInFlight(request *http.Request, cancel context.CancelFunc) (*http.Response, error) {
	time.AfterFunc(time.Second, cancel)
	<-request.Context().Done()
	return nil, request.Context().Err()
}
