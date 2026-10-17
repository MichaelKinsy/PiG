package ai

import "testing"

// utils/overflow.ts:179-181 isRecoverableLength: `message.stopReason === "length" && desiredMaxOutput > 0 && message.usage.output < desiredMaxOutput`.
// Only a length stop counts (every other stop reason is false, however little it produced), a limit of zero or less never recovers, and the comparison is
// strict on the output tokens alone (input and cache tokens do not count).
func TestIsRecoverableLengthRequiresALengthStopAPositiveLimitAndStrictlyLessOutput(t *testing.T) {
	for _, reason := range []StopReason{StopReasonStop, StopReasonToolUse, StopReasonError, StopReasonAborted} {
		message := lengthStopMessage(1, 0, 1)
		message.StopReason = reason
		if IsRecoverableLength(message, 1000) {
			t.Errorf("stop reason %q reported recoverable", reason)
		}
	}
	short := lengthStopMessage(1, 0, 0)
	for _, limit := range []int{0, -1} {
		if IsRecoverableLength(short, limit) {
			t.Errorf("limit %d reported recoverable", limit)
		}
	}
	if !IsRecoverableLength(lengthStopMessage(5000, 5000, 9), 10) {
		t.Error("output 9 below limit 10 must be recoverable whatever the input")
	}
	if IsRecoverableLength(lengthStopMessage(1, 0, 10), 10) {
		t.Error("output equal to the limit must not be recoverable")
	}
	if IsRecoverableLength(lengthStopMessage(1, 0, 11), 10) {
		t.Error("output above the limit must not be recoverable")
	}
	if IsRecoverableLength(lengthStopMessage(1, 0, 2000), 10) {
		t.Error("an output above the limit with tiny input must not be recoverable")
	}
}
