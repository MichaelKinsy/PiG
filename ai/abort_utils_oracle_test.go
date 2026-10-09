package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type sleepProbe struct {
	MS           float64 `json:"ms"`
	AbortBefore  string  `json:"abortBefore,omitempty"`
	AbortAfterMs *int    `json:"abortAfterMs,omitempty"`
	AbortWith    string  `json:"abortWith,omitempty"`
}

type combineProbe struct {
	Signals []string `json:"signals"`
	Actions []string `json:"actions"`
}

// utils/sleep.ts and utils/abort-signals.ts against the pinned pi-ai: sleep fails at once on an aborted signal (a zero delay included), rejects with signal.reason when aborted during the wait, and waits as setTimeout would (a delay above 2^31-1 or below 1 waits 1ms);
// combineAbortSignals ignores undefined signals, returns a single signal as it is, aborts with the reason of the first signal to abort (an already aborted one at once), and stops observing after cleanup.
func TestAbortUtilsMatchPi(t *testing.T) {
	after := func(ms int) *int { return &ms }
	sleeps := []sleepProbe{
		{MS: 0}, {MS: 5}, {MS: -5}, {MS: 1.9}, {MS: 1e10},
		{MS: 0, AbortBefore: "early"}, {MS: 1000, AbortBefore: "early"},
		{MS: 1000, AbortAfterMs: after(10), AbortWith: "mid"},
		{MS: 1e10, AbortAfterMs: after(500), AbortWith: "too late"},
	}
	combines := []combineProbe{
		{Signals: []string{}},
		{Signals: []string{"none", "none"}},
		{Signals: []string{"live"}, Actions: []string{"0:x"}},
		{Signals: []string{"none", "live", "none"}, Actions: []string{"1:y"}},
		{Signals: []string{"aborted:solo"}},
		{Signals: []string{"live", "live"}, Actions: []string{"1:b", "0:a"}},
		{Signals: []string{"live", "aborted:pre", "live"}, Actions: []string{"0:x", "2:z"}},
		{Signals: []string{"aborted:first", "aborted:second"}},
		{Signals: []string{"live", "live"}, Actions: []string{"cleanup", "0:late", "1:later"}},
		{Signals: []string{"live", "live", "live"}, Actions: []string{"2:z", "cleanup"}},
		{Signals: []string{"live", "none", "live"}, Actions: []string{"cleanup", "2:after"}},
	}
	input, err := json.Marshal(map[string]any{"sleeps": sleeps, "combines": combines})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/abort_utils.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var want struct {
		Sleeps   []string   `json:"sleeps"`
		Combines [][]string `json:"combines"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	for i, probe := range sleeps {
		ctx, cancel := context.WithCancelCause(context.Background())
		if probe.AbortBefore != "" {
			cancel(errors.New(probe.AbortBefore))
		}
		if probe.AbortAfterMs != nil {
			time.AfterFunc(time.Duration(*probe.AbortAfterMs)*time.Millisecond, func() { cancel(errors.New(probe.AbortWith)) })
		}
		settled := make(chan error, 1)
		go func() { settled <- Sleep(ctx, probe.MS) }()
		got := "still sleeping after 3s"
		select {
		case err := <-settled:
			got = "resolved"
			if err != nil {
				got = "rejected:" + err.Error()
			}
		case <-time.After(3 * time.Second):
		}
		cancel(nil)
		if got != want.Sleeps[i] {
			t.Errorf("sleep %+v = %q, Pi %q", probe, got, want.Sleeps[i])
		}
	}

	for i, probe := range combines {
		cancels := make([]context.CancelCauseFunc, len(probe.Signals))
		inputs := make([]context.Context, len(probe.Signals))
		live := 0
		var only context.Context
		for j, spec := range probe.Signals {
			if spec == "none" {
				continue
			}
			inputs[j], cancels[j] = context.WithCancelCause(context.Background())
			if reason, ok := strings.CutPrefix(spec, "aborted:"); ok {
				cancels[j](errors.New(reason))
			}
			live++
			only = inputs[j]
		}
		// packages/ai/src/utils/abort-signals.ts:1-4 CombinedAbortSignal { signal?, cleanup }: Signal is absent without a signal.
		combined := CombineAbortSignals(inputs...)
		// observe reads the Signal member of Pi's CombinedAbortSignal (abort-signals.ts:1-4): absent, aborted with its reason, or live.
		observe := func(c CombinedAbortSignal) string {
			switch {
			case c.Signal == nil:
				return "nosignal"
			case c.Signal.Err() != nil:
				return "aborted:" + context.Cause(c.Signal).Error()
			}
			return "live"
		}
		state := func() string { return observe(combined) }
		got := []string{state(), strconv.FormatBool(live == 1 && combined.Signal == only)}
		for _, action := range probe.Actions {
			if action == "cleanup" {
				combined.Cleanup()
			} else {
				index, reason, _ := strings.Cut(action, ":")
				n, _ := strconv.Atoi(index)
				cancels[n](errors.New(reason))
			}
			// A Go context has no synchronous abort listener, so the combination follows its inputs one goroutine hop later.
			if combined.Signal != nil {
				select {
				case <-combined.Signal.Done():
				case <-time.After(30 * time.Millisecond):
				}
			}
			got = append(got, state())
		}
		combined.Cleanup()
		if !reflect.DeepEqual(got, want.Combines[i]) {
			t.Errorf("combine %+v = %q, Pi %q", probe, got, want.Combines[i])
		}
	}
}
