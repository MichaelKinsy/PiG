package host

// pi: packages/chord/src/publish.ts
// Pi's queue is one module-level FIFO on one thread. Go hosts have loops of their own, but what one call stack
// enqueues, holds and delivers keeps Pi's order whichever queues (the shared one, one loop's, several loops') the
// tasks belong to: programs of enqueues and holds, each task owned by a host, run through Pi's publish.ts and through
// enqueue and holdPublications on each arrangement must log the same order.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type publishAction struct {
	Log     string       `json:"log,omitempty"`
	Enqueue *int         `json:"enqueue,omitempty"`
	Hold    *publishHold `json:"hold,omitempty"`
}

type publishHold struct {
	Host    int             `json:"host"`
	Actions []publishAction `json:"actions"`
}

// publishProgram is a set of tasks, each owned by a host, and the top-level actions that start them.
type publishProgram struct {
	Hosts []int             `json:"hosts"`
	Tasks [][]publishAction `json:"tasks"`
	Top   []publishAction   `json:"top"`
}

const publishHosts = 3

// publishActions makes up to three actions of task (negative: the top level). A task only enqueues later tasks, so a
// program ends; holds nest to a depth of two.
func publishActions(random *rand.Rand, name string, task, tasks, depth int) []publishAction {
	var actions []publishAction
	for index := range 1 + random.Intn(3) {
		label := fmt.Sprintf("%s.%d", name, index)
		switch kind := random.Intn(6); {
		case kind < 2 || (kind == 2 && task+1 >= tasks):
			actions = append(actions, publishAction{Log: label})
		case kind < 5 && task+1 < tasks:
			target := task + 1 + random.Intn(tasks-task-1)
			actions = append(actions, publishAction{Enqueue: &target})
		case depth < 2:
			actions = append(actions, publishAction{Hold: &publishHold{Host: random.Intn(publishHosts), Actions: publishActions(random, label, task, tasks, depth+1)}})
		default:
			actions = append(actions, publishAction{Log: label})
		}
	}
	return actions
}

func publishPrograms(count int) []publishProgram {
	random := rand.New(rand.NewSource(20260101))
	programs := make([]publishProgram, count)
	for index := range programs {
		tasks := 2 + random.Intn(6)
		program := publishProgram{Hosts: make([]int, tasks), Tasks: make([][]publishAction, tasks)}
		for task := range tasks {
			program.Hosts[task] = random.Intn(publishHosts)
			program.Tasks[task] = publishActions(random, fmt.Sprintf("t%d", task), task, tasks, 0)
		}
		program.Top = publishActions(random, "top", -1, tasks, 0)
		programs[index] = program
	}
	return programs
}

// run is the program on the queues loopOf names: loopOf(nil) is a host's loop, nil the shared queue.
func (program *publishProgram) run(loopOf func(host int) *loop) []string {
	log := []string{}
	var run func(actions []publishAction)
	run = func(actions []publishAction) {
		for _, action := range actions {
			switch {
			case action.Enqueue != nil:
				task := *action.Enqueue
				enqueue(loopOf(program.Hosts[task]), func() { run(program.Tasks[task]) })
			case action.Hold != nil:
				holdPublications(loopOf(action.Hold.Host), func() struct{} {
					run(action.Hold.Actions)
					return struct{}{}
				})
			default:
				log = append(log, action.Log)
			}
		}
	}
	run(program.Top)
	return log
}

func TestPublicationsMatchPiAcrossQueues(t *testing.T) {
	programs := publishPrograms(600)
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/publish_oracle.mjs", filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(programs) {
		t.Fatalf("Pi ran %d programs, want %d", len(expected), len(programs))
	}
	arrangements := []struct {
		name   string
		loopOf func() func(host int) *loop
	}{
		{"the shared queue", func() func(int) *loop { return func(int) *loop { return nil } }},
		{"one loop", func() func(int) *loop { one := &loop{}; return func(int) *loop { return one } }},
		{"a loop per host", func() func(int) *loop {
			loops := make([]*loop, publishHosts)
			for host := range loops {
				loops[host] = &loop{}
			}
			return func(host int) *loop { return loops[host] }
		}},
	}
	crossing := 0
	for index := range programs {
		program := &programs[index]
		if slices.ContainsFunc(program.Top, func(action publishAction) bool { return action.Enqueue != nil }) && slices.Max(program.Hosts) != slices.Min(program.Hosts) {
			crossing++
		}
		for _, arrangement := range arrangements {
			if got := program.run(arrangement.loopOf()); !slices.Equal(got, expected[index]) {
				encoded, _ := json.Marshal(program)
				t.Fatalf("on %s program %d logged\n%v\nPi logged\n%v\nprogram %s", arrangement.name, index, got, expected[index], encoded)
			}
		}
	}
	if crossing < len(programs)/4 {
		t.Fatalf("only %d of %d programs start tasks of several hosts", crossing, len(programs))
	}
}
