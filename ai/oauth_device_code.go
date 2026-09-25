package ai

// Mirrors upstream .upstream/current/packages/ai/src/utils/oauth/device-code.ts.

import (
	"context"
	"errors"
	"math"
	"time"
)

const (
	deviceCodeCancelMessage   = "Login cancelled"
	deviceCodeTimeoutMessage  = "Device flow timed out"
	deviceCodeSlowDownMessage = "Device flow timed out after one or more slow_down responses. " +
		"This is often caused by clock drift in WSL or VM environments. " +
		"Please sync or restart the VM clock and try again."
	deviceCodeMinIntervalMS = 1000
	// RFC 8628 section 3.2: if the server omits `interval`, use 5 seconds.
	deviceCodeDefaultPollIntervalSeconds = 5
	// RFC 8628 section 3.5: `slow_down` means increase the interval by 5 seconds.
	deviceCodeSlowDownIncrementMS = 5000
)

// DeviceCodePollStatus is the outcome of one device-code poll attempt.
type DeviceCodePollStatus int

const (
	// DevicePollPending: keep polling.
	DevicePollPending DeviceCodePollStatus = iota
	// DevicePollSlowDown: keep polling but widen the interval (RFC 8628 §3.5).
	DevicePollSlowDown
	// DevicePollFailed: stop with Message as the error.
	DevicePollFailed
	// DevicePollComplete: stop and return Value.
	DevicePollComplete
)

// DeviceCodePollResult is one poll attempt's result. Mirrors upstream
// OAuthDeviceCodePollResult.
type DeviceCodePollResult[T any] struct {
	Status  DeviceCodePollStatus
	Value   T
	Message string
	// IntervalSeconds is the server-provided polling interval on a slow_down
	// result. Nil (or a non-positive value) applies the RFC 8628 +5s rule.
	IntervalSeconds *float64
}

// DeviceCodePollOptions configures PollOAuthDeviceCodeFlow. A nil
// IntervalSeconds defaults to 5s (RFC 8628 §3.2); a nil ExpiresInSeconds
// means no deadline. WaitBeforeFirstPoll sleeps one interval before the first
// poll. The Go API carries the upstream AbortSignal through ctx.
type DeviceCodePollOptions[T any] struct {
	IntervalSeconds     *float64
	ExpiresInSeconds    *float64
	WaitBeforeFirstPoll bool
	Poll                func() (DeviceCodePollResult[T], error)
}

// abortableDeviceSleep sleeps for d unless ctx is cancelled first, in which
// case it returns the cancel error. Mirrors upstream abortableSleep.
func abortableDeviceSleep(ctx context.Context, d time.Duration) error {
	if ctx.Err() != nil {
		return errors.New(deviceCodeCancelMessage)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return errors.New(deviceCodeCancelMessage)
	}
}

// PollOAuthDeviceCodeFlow runs the RFC 8628 polling loop: it calls Poll,
// honoring pending/slow_down/failed/complete, sleeping the (possibly widened)
// interval between attempts, until Poll completes or the deadline/ctx fires.
// Mirrors upstream pollOAuthDeviceCodeFlow.
func PollOAuthDeviceCodeFlow[T any](ctx context.Context, opts DeviceCodePollOptions[T]) (T, error) {
	var zero T

	deadline := time.Time{} // zero => no deadline
	if opts.ExpiresInSeconds != nil {
		deadline = time.Now().Add(time.Duration(*opts.ExpiresInSeconds * float64(time.Second)))
	}

	intervalSeconds := float64(deviceCodeDefaultPollIntervalSeconds)
	if opts.IntervalSeconds != nil {
		intervalSeconds = *opts.IntervalSeconds
	}
	intervalMS := math.Max(deviceCodeMinIntervalMS, math.Floor(intervalSeconds*1000))

	slowDownResponses := 0
	if opts.WaitBeforeFirstPoll {
		if _, err := sleepBeforeNextDevicePoll(ctx, deadline, intervalMS); err != nil {
			return zero, err
		}
	}

	for deadline.IsZero() || time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return zero, errors.New(deviceCodeCancelMessage)
		}

		result, err := opts.Poll()
		if err != nil {
			return zero, err
		}
		switch result.Status {
		case DevicePollComplete:
			return result.Value, nil
		case DevicePollFailed:
			return zero, errors.New(result.Message)
		case DevicePollSlowDown:
			slowDownResponses++
			intervalMS = slowDownDeviceInterval(intervalMS, result.IntervalSeconds)
		case DevicePollPending:
		}

		slept, err := sleepBeforeNextDevicePoll(ctx, deadline, intervalMS)
		if err != nil {
			return zero, err
		}
		if !slept {
			break
		}
	}

	if slowDownResponses > 0 {
		return zero, errors.New(deviceCodeSlowDownMessage)
	}
	return zero, errors.New(deviceCodeTimeoutMessage)
}

// slowDownDeviceInterval uses the server-provided interval when given (GitHub
// reports the new required minimum in `interval`); trusting only a
// client-tracked value risks polling early forever under WSL/VM clock drift.
// Otherwise it applies RFC 8628 section 3.5 to this and all later requests.
func slowDownDeviceInterval(intervalMS float64, serverSeconds *float64) float64 {
	if serverSeconds != nil && !math.IsInf(*serverSeconds, 0) && !math.IsNaN(*serverSeconds) && *serverSeconds > 0 {
		return math.Max(deviceCodeMinIntervalMS, math.Floor(*serverSeconds*1000))
	}
	return math.Max(deviceCodeMinIntervalMS, intervalMS+deviceCodeSlowDownIncrementMS)
}

// sleepBeforeNextDevicePoll waits one interval, clamped to the deadline. It
// reports false without sleeping when the deadline has already passed.
func sleepBeforeNextDevicePoll(ctx context.Context, deadline time.Time, intervalMS float64) (bool, error) {
	sleepMS := intervalMS
	if !deadline.IsZero() {
		remainingMS := math.Floor(float64(time.Until(deadline)) / float64(time.Millisecond))
		if remainingMS <= 0 {
			return false, nil
		}
		sleepMS = math.Min(intervalMS, remainingMS)
	}
	return true, abortableDeviceSleep(ctx, time.Duration(sleepMS)*time.Millisecond)
}
