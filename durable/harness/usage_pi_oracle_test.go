package harness

// pi: packages/durable/src/harness/usage.ts (recordUsage, addUsage, addUsageState)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type usageStep struct {
	Op     string            `json:"op"`
	Bucket string            `json:"bucket,omitempty"`
	Key    string            `json:"key,omitempty"`
	Usage  json.RawMessage   `json:"usage,omitempty"`
	Sum    json.RawMessage   `json:"sum,omitempty"`
	States []json.RawMessage `json:"states,omitempty"`
}

type usageOutcome struct {
	Index int             `json:"index"`
	OK    json.RawMessage `json:"ok"`
	Err   string          `json:"err"`
}

func runPigUsage(t *testing.T, program []usageStep) []usageOutcome {
	t.Helper()
	ctx := context.Background()
	line := session.CreateSession(storage.NewMemoryStorage())
	var conv durable.ConversationId = 1_000_000_000
	var outcomes []usageOutcome
	for index, step := range program {
		var value any
		var err error
		switch step.Op {
		case "create":
			var record any
			record, err = line.Commit(ctx, func(tx durable.Tx) (any, error) {
				created, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
				conv = created.Id
				return created.Id, err
			})
			value = record
		case "record":
			var usage ai.Usage
			if err := json.Unmarshal(step.Usage, &usage); err != nil {
				t.Fatal(err)
			}
			_, err = line.Commit(ctx, func(tx durable.Tx) (any, error) {
				return nil, RecordUsage(tx, conv, UsageBucket(step.Bucket), step.Key, usage)
			})
		case "read":
			value, err = line.Commit(ctx, func(tx durable.Tx) (any, error) {
				draft, err := docDraft(tx, UsageDoc, conv)
				if err != nil {
					return nil, err
				}
				return draft.Snapshot(), nil
			})
		case "sum":
			var sum UsageState
			if err := json.Unmarshal(step.Sum, &sum); err != nil {
				t.Fatal(err)
			}
			for _, raw := range step.States {
				var state UsageState
				if err := json.Unmarshal(raw, &state); err != nil {
					t.Fatal(err)
				}
				AddUsageState(&sum, state)
			}
			value = sum
		default:
			t.Fatalf("unknown op %s", step.Op)
		}
		outcome := usageOutcome{Index: index}
		if err != nil {
			outcome.Err = err.Error()
		} else {
			outcome.OK, _ = json.Marshal(value)
			if value == nil {
				outcome.OK = json.RawMessage("null")
			}
		}
		outcomes = append(outcomes, outcome)
	}
	if err := line.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return outcomes
}

func usageProgramFor(seed int64) []usageStep {
	random := rand.New(rand.NewSource(seed))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	count := func() string { return pick("0", "1", "7", "1000", "123456") }
	cost := func() string { return pick("0", "0.1", "0.2", "0.000003", "1.5", "12.25", "0.30000000000000004") }
	usage := func() string {
		out := fmt.Sprintf(`{"input":%s,"output":%s,"cacheRead":%s,"cacheWrite":%s,"totalTokens":%s`, count(), count(), count(), count(), count())
		if random.Intn(3) == 0 {
			out += `,"cacheWrite1h":` + count()
		}
		if random.Intn(3) == 0 {
			out += `,"reasoning":` + count()
		}
		return out + fmt.Sprintf(`,"cost":{"input":%s,"output":%s,"cacheRead":%s,"cacheWrite":%s,"total":%s}}`, cost(), cost(), cost(), cost(), cost())
	}
	keys := []string{"p/m1", "p/m2", "toString", "__proto__", "constructor", "hasOwnProperty", "bash", "a.b"}
	state := func() string {
		bucket := func() string {
			out := "{"
			for i := range random.Intn(4) {
				if i > 0 {
					out += ","
				}
				out += fmt.Sprintf("%q:%s", keys[random.Intn(len(keys))], usage())
			}
			return out + "}"
		}
		return `{"models":` + bucket() + `,"tools":` + bucket() + `}`
	}
	program := []usageStep{{Op: "create"}}
	for range 24 {
		switch random.Intn(8) {
		case 0:
			program = append(program, usageStep{Op: "read"})
		case 1:
			step := usageStep{Op: "sum", Sum: json.RawMessage(state())}
			for range 1 + random.Intn(3) {
				step.States = append(step.States, json.RawMessage(state()))
			}
			program = append(program, step)
		default:
			program = append(program, usageStep{Op: "record", Bucket: pick("models", "tools"), Key: keys[random.Intn(len(keys))], Usage: json.RawMessage(usage())})
		}
	}
	return append(program, usageStep{Op: "read"})
}

func TestUsageLedgerMatchesPiOnSeededPrograms(t *testing.T) {
	const seeded = 300
	steps := make([][]usageStep, seeded)
	programs := make([]map[string]any, seeded)
	for i := range steps {
		steps[i] = usageProgramFor(int64(5550000 + i))
		programs[i] = map[string]any{"steps": steps[i]}
	}
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/usage_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]usageOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(raw json.RawMessage) any {
		var value any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
		}
		return value
	}
	differing, refused, sums, protoKeys := 0, 0, 0, 0
	for i, program := range steps {
		got := runPigUsage(t, program)
		for k, want := range expected[i] {
			if got[k].Err != want.Err || !reflect.DeepEqual(canonical(got[k].OK), canonical(want.OK)) {
				differing++
				if differing <= 3 {
					t.Errorf("program %d step %d (%s %s %s)\n  Pi  err=%q ok=%.400s\n  Pig err=%q ok=%.400s\n  usage=%.300s", i, k, program[k].Op, program[k].Bucket, program[k].Key, want.Err, want.OK, got[k].Err, got[k].OK, program[k].Usage)
				}
			}
			if want.Err != "" {
				refused++
			}
			if program[k].Op == "sum" {
				sums++
			}
			if program[k].Op == "read" && bytes.Contains(want.OK, []byte(`"__proto__"`)) {
				protoKeys++
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d answers differ from Pi", differing)
	}
	t.Logf("%d refusals, %d sums, %d reads with a __proto__ key", refused, sums, protoKeys)
}
