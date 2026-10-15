package ai

// pi: packages/ai/src/auth/oauth/device-code.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type devicePollStep struct {
	Cost            float64 `json:"cost"`
	Status          string  `json:"status"`
	IntervalSeconds any     `json:"intervalSeconds,omitempty"`
	Message         string  `json:"message,omitempty"`
	Value           string  `json:"value,omitempty"`
}

type devicePollProbe struct {
	IntervalSeconds     any              `json:"intervalSeconds,omitempty"`
	ExpiresInSeconds    any              `json:"expiresInSeconds,omitempty"`
	WaitBeforeFirstPoll bool             `json:"waitBeforeFirstPoll,omitempty"`
	AbortAt             *float64         `json:"abortAt,omitempty"`
	Script              []devicePollStep `json:"script"`
}

// devicePollNumber decodes a probe number: a JSON number or "NaN", "Infinity" or "-Infinity".
func devicePollNumber(v any) *float64 {
	switch n := v.(type) {
	case nil:
		return nil
	case float64:
		return &n
	case string:
		f := map[string]float64{"NaN": math.NaN(), "Infinity": math.Inf(1), "-Infinity": math.Inf(-1)}[n]
		return &f
	}
	panic("bad probe number")
}

func devicePollProbes() []devicePollProbe {
	random := rand.New(rand.NewSource(20261021))
	pick := func(options ...any) any { return options[random.Intn(len(options))] }
	numbers := []any{nil, nil, 1.2345, 6.0007, 0.0, 0.5, 1.0, 1.999, 2.0, 5.0, 30.0, -3.0, "NaN", "Infinity", "-Infinity", 1e300, 3e6, 7.25}
	expiries := []any{nil, nil, 0.0, -1.0, 1.0, 2.5, 7.0, 12.0, 60.0, 600.0, "NaN", "Infinity", 1e10, 1e300, 0.0004}
	var probes []devicePollProbe
	for range 4000 {
		probe := devicePollProbe{IntervalSeconds: pick(numbers...), ExpiresInSeconds: pick(expiries...), WaitBeforeFirstPoll: random.Intn(2) == 0}
		if random.Intn(3) == 0 {
			abort := pick(-1.0, 0.0, 500.0, 1000.0, 1500.0, 2999.0, 5000.0, 6000.0, 12000.0, 30000.0).(float64)
			probe.AbortAt = &abort
		}
		probe.Script = []devicePollStep{}
		for range random.Intn(14) {
			step := devicePollStep{Cost: pick(0.0, 0.0, 0.0, 300.0, 1000.0, 5000.0).(float64)}
			switch random.Intn(12) {
			case 0, 1, 2, 3, 4:
				step.Status = "pending"
			case 5, 6, 7:
				step.Status = "slow_down"
				step.IntervalSeconds = pick(nil, nil, 0.0, -1.0, "NaN", "Infinity", 2.5, 3.3333, 10.0, 0.4)
			case 8:
				step.Status = "failed"
				step.Message = "device failed"
			case 9:
				step.Status = "throw"
				step.Message = "poll boom"
			default:
				step.Status = "complete"
				step.Value = "done"
			}
			probe.Script = append(probe.Script, step)
		}
		probes = append(probes, probe)
	}
	return probes
}

type devicePollEvents struct {
	Events [][]any `json:"events"`
	Result struct {
		Value *string `json:"value"`
		Err   *string `json:"err"`
	} `json:"result"`
}

func runDevicePollOracle(t *testing.T, probes []devicePollProbe) []devicePollEvents {
	t.Helper()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/device_code_poll.mjs", filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "device-code.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var results []devicePollEvents
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

// runPigDevicePoll runs PollOAuthDeviceCodeFlow on the same virtual clock the Pi driver uses.
func runPigDevicePoll(probe devicePollProbe) devicePollEvents {
	const start = 1_700_000_000_000.0
	now := start
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aborted := false
	abortTime := math.NaN()
	if probe.AbortAt != nil {
		abortTime = start + *probe.AbortAt
		if *probe.AbortAt == -1 {
			abortTime = start
			aborted = true
			cancel()
		}
	}
	events := [][]any{}
	number := func(f float64) any {
		switch {
		case math.IsInf(f, 1):
			return "Infinity"
		case math.IsNaN(f):
			return "NaN"
		}
		return f
	}
	// advance moves the clock to t and aborts if the abort time passed on the way.
	advance := func(t float64) {
		if !aborted && !math.IsNaN(abortTime) && abortTime <= t {
			now = math.Max(now, abortTime)
			aborted = true
			cancel()
		}
		now = math.Max(now, t)
	}
	deviceCodeNow = func() float64 { return now }
	deviceCodeSleep = func(ctx context.Context, ms float64) error {
		// abortableSleep rejects before it arms a timer when the signal is already aborted.
		if ctx.Err() != nil {
			return errors.New(deviceCodeCancelMessage)
		}
		events = append(events, []any{"sleep", number(ms), now - start})
		coerced := 1.0
		if ms >= 1 && ms <= nodeTimerMaxMs {
			coerced = math.Trunc(ms)
		}
		wake := now + coerced
		// The abort wins a tie with the timer, as in the Pi driver.
		if !aborted && !math.IsNaN(abortTime) && abortTime <= wake {
			advance(abortTime)
			return errors.New(deviceCodeCancelMessage)
		}
		advance(wake)
		return nil
	}
	calls := 0
	opts := DeviceCodePollOptions[string]{
		IntervalSeconds:     devicePollNumber(probe.IntervalSeconds),
		ExpiresInSeconds:    devicePollNumber(probe.ExpiresInSeconds),
		WaitBeforeFirstPoll: probe.WaitBeforeFirstPoll,
		Poll: func() (DeviceCodePollResult[string], error) {
			events = append(events, []any{"poll", now - start})
			if calls >= len(probe.Script) {
				return DeviceCodePollResult[string]{}, errors.New("exhausted")
			}
			step := probe.Script[calls]
			calls++
			advance(now + step.Cost)
			switch step.Status {
			case "throw":
				return DeviceCodePollResult[string]{}, errors.New(step.Message)
			case "complete":
				return DeviceCodePollResult[string]{Status: DevicePollComplete, Value: step.Value}, nil
			case "failed":
				return DeviceCodePollResult[string]{Status: DevicePollFailed, Message: step.Message}, nil
			case "slow_down":
				return DeviceCodePollResult[string]{Status: DevicePollSlowDown, IntervalSeconds: devicePollNumber(step.IntervalSeconds)}, nil
			}
			return DeviceCodePollResult[string]{Status: DevicePollPending}, nil
		},
	}
	value, err := PollOAuthDeviceCodeFlow(ctx, opts)
	var out devicePollEvents
	out.Events = events
	if err != nil {
		text := err.Error()
		out.Result.Err = &text
	} else {
		out.Result.Value = &value
	}
	return out
}

func TestDeviceCodePollMatchesPi(t *testing.T) {
	probes := devicePollProbes()
	expected := runDevicePollOracle(t, probes)
	t.Cleanup(func() {
		deviceCodeNow = func() float64 { return float64(nowMillis()) }
		deviceCodeSleep = abortableDeviceSleepMs
	})
	differing := 0
	outcomes := map[string]int{}
	for i, probe := range probes {
		got := runPigDevicePoll(probe)
		want := expected[i]
		// The oracle and the Go run both encode event times as JSON numbers; compare them through the same encoding.
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		var gotAny, wantAny any
		_ = json.Unmarshal(gotJSON, &gotAny)
		_ = json.Unmarshal(wantJSON, &wantAny)
		if want.Result.Err != nil {
			outcomes[*want.Result.Err]++
		} else {
			outcomes["value"]++
		}
		if !reflect.DeepEqual(gotAny, wantAny) {
			differing++
			if differing <= 6 {
				t.Errorf("probe %d %+v\n  Pi  %s\n  Pig %s", i, probe, wantJSON, gotJSON)
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d runs differ from Pi", differing, len(probes))
	}
	t.Logf("outcomes: %v", outcomes)
	for _, outcome := range []string{"value", "Login cancelled", deviceCodeTimeoutMessage, deviceCodeSlowDownMessage, "device failed", "poll boom", "exhausted"} {
		if outcomes[outcome] < 20 {
			t.Errorf("probes rarely reach %q: %d", outcome, outcomes[outcome])
		}
	}
}

// The deadline is Date.now() + expiresInSeconds * 1000 with the product rounded first (device-code.ts:47-50). At the
// driver's start of 1700000000000, 7.000000366210937 s puts the deadline 7000.00048828125 ms ahead in Pi; a fused
// multiply-add puts it 7000.000244140625 ms ahead, which the first sleep reports.
func TestDeviceCodePollDeadlineRoundsTheProductLikePi(t *testing.T) {
	probes := []devicePollProbe{
		{IntervalSeconds: 30.0, ExpiresInSeconds: 7.000000366210937, WaitBeforeFirstPoll: true, Script: []devicePollStep{{Status: "pending"}}},
		{IntervalSeconds: 5.0, ExpiresInSeconds: 7.000000854492187, Script: []devicePollStep{{Status: "pending"}, {Status: "pending"}}},
	}
	expected := runDevicePollOracle(t, probes)
	t.Cleanup(func() {
		deviceCodeNow = func() float64 { return float64(nowMillis()) }
		deviceCodeSleep = abortableDeviceSleepMs
	})
	for i, probe := range probes {
		gotJSON, _ := json.Marshal(runPigDevicePoll(probe))
		wantJSON, _ := json.Marshal(expected[i])
		var gotAny, wantAny any
		_ = json.Unmarshal(gotJSON, &gotAny)
		_ = json.Unmarshal(wantJSON, &wantAny)
		if !reflect.DeepEqual(gotAny, wantAny) {
			t.Errorf("probe %d %+v\n  Pi  %s\n  Pig %s", i, probe, wantJSON, gotJSON)
		}
	}
}
