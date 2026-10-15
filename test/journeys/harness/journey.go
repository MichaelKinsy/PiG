package harness

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// Env is the per-product configuration of a journey's sandbox and launch.
type Env struct {
	Settings      map[string]any
	Files         map[string]string
	ExtraEnv      []string
	Args          []string // arguments after the base arguments
	Extensions    bool     // keep extension discovery on (the base arguments pass --no-extensions otherwise)
	Reasoning     bool
	Images        bool
	Bin           map[string]string // executable stubs that lead PATH
	AgentDirFiles map[string]string // files under the agent directory (prompts, skills, context files)
	ContextWindow int
	Width, Height int
	NoModelArg    bool // launch without --model
	Pipe          bool // drive the process over standard input and output instead of a terminal (RPC and print modes)
	// KeepHeader keeps the startup header. By default settings.json carries quietStartup, which both products honour, so frames are not shifted by the two headers' different heights (D2); the startup journey keeps it and compares the banner region on its own.
	KeepHeader bool
}

// Step is one scripted interaction. It runs against one product through Ctx and must behave identically for both.
type Step struct {
	Name string
	Run  func(c *Ctx)
}

// Action is a step that may run in any order relative to the other actions of its journey, subject to Needs. Every action begins and ends at an idle prompt, so the plan generator may place it anywhere.
type Action struct {
	Step
	// Needs names actions that must run earlier; an action whose need is absent from the plan is dropped with it.
	Needs []string
}

// Normalizer rewrites rendered frames before comparison. Every rule states why PiG and Pi legitimately differ.
type Normalizer struct {
	Pattern string // regexp (RE2)
	With    string
	Reason  string // required: the numbered divergence or the Pi source that explains the difference
	Kinds   []Kind // restrict to products; empty means both
}

// Journey is one long end-to-end session over a feature cluster.
type Journey struct {
	Name string
	Doc  string
	// Touches lists module-relative path prefixes; a change under one selects the journey in lane gates.
	Touches []string
	// PigOnly journeys cover PiG-only features and have no Pi run.
	PigOnly bool
	Script  Script
	Env     func(Kind) Env
	// Backbone runs first, in order; the first step normally launches.
	Backbone []Step
	// Pool actions run in seeded random order between Backbone and Tail.
	Pool []Action
	Tail []Step
	// MemMaxMB bounds PiG's resident set (process tree) at any checkpoint of the plain build; the instrumented build scales it.
	MemMaxMB int
	// ExitCode is the expected final exit status of every launched process (default 0).
	ExitCode int
	// Normalizers extend the shared frame normalizers.
	Normalizers []Normalizer
	// SkipFrames lists frame names (substring match) that are recorded but not compared: the frame is a PiG-only surface or a timing-dependent picture with its own assertions in the step.
	SkipFrames []string
	// AgentFiles are paths under the agent directory (settings.json, ...) whose final content is compared as JSON after both products exit.
	AgentFiles []string
	// SkipSession and SkipRequests record why the comparison is not meaningful for this journey.
	SkipSession, SkipRequests bool
}

// Plan is the replayable order of one run.
type Plan struct {
	Seed int64
	Pool []string
}

func (p Plan) String() string {
	return fmt.Sprintf("seed=%d pool=[%s]", p.Seed, strings.Join(p.Pool, " "))
}

// MakePlan orders j's pool from seed: a random topological order that respects Needs.
func MakePlan(j *Journey, seed int64) Plan {
	rng := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	done := map[string]bool{}
	remaining := slices.Clone(j.Pool)
	var order []string
	for len(remaining) > 0 {
		var ready []int
		for i, a := range remaining {
			ok := true
			for _, n := range a.Needs {
				if !done[n] {
					ok = false
				}
			}
			if ok {
				ready = append(ready, i)
			}
		}
		if len(ready) == 0 {
			panic(fmt.Sprintf("journey %s: unsatisfiable Needs among %v", j.Name, order))
		}
		pick := ready[rng.IntN(len(ready))]
		order = append(order, remaining[pick].Name)
		done[remaining[pick].Name] = true
		remaining = slices.Delete(remaining, pick, pick+1)
	}
	return Plan{Seed: seed, Pool: order}
}

// Prune removes the named actions and every action that needs one of them.
func (j *Journey) Prune(p Plan, drop ...string) Plan {
	gone := map[string]bool{}
	for _, d := range drop {
		gone[d] = true
	}
	for changed := true; changed; {
		changed = false
		for _, a := range j.Pool {
			if gone[a.Name] {
				continue
			}
			for _, n := range a.Needs {
				if gone[n] {
					gone[a.Name] = true
					changed = true
				}
			}
		}
	}
	out := Plan{Seed: p.Seed}
	for _, n := range p.Pool {
		if !gone[n] {
			out.Pool = append(out.Pool, n)
		}
	}
	return out
}

func (j *Journey) action(name string) (Action, bool) {
	for _, a := range j.Pool {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

// stepRand derives the random source of a step from the seed and the step's name, so removing other steps never changes it, and both products draw the same values.
func stepRand(seed int64, name string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return rand.New(rand.NewPCG(uint64(seed), h.Sum64()))
}

// StepTiming is the wall time of one step.
type StepTiming struct {
	Name string
	Dur  time.Duration
}

// Ctx is what a step sees: one product instance of the journey.
type Ctx = Run
