package pico3

import (
	"context"
	"fmt"
	"maps"
	"math"
)

// jobInput is the pi.job input: a process spec plus scheduling options.
type jobInput struct {
	ProcessSpec
	Notify    bool     `json:"notify"`
	Rerun     bool     `json:"rerun"`
	Every     *float64 `json:"every,omitempty"`
	NotBefore *float64 `json:"notBefore,omitempty"`
}

func jobInputOf(task Task) jobInput {
	var input jobInput
	_ = decodeInto(task.Input, &input)
	return input
}

func jobFailed(reason, detail string) Completion {
	return Failed(JsonObject{"reason": reason, "detail": detail})
}

func jobNotice(text string, now float64) NewEntry {
	return NewEntry{Kind: "pi.notice", Model: []JsonObject{userMessage(text, now)}}
}

// jobKind runs a host process, optionally every N milliseconds, and streams
// its output into its live slot.
var jobKind = &Kind{
	Name: "pi.job",
	Slot: func(JsonValue) JsonObject { return JsonObject{} },
	Describe: func(task Task, slot JsonObject) JsonValue {
		stage := Phase(task.Checkpoint)
		if stage == "" {
			stage = "starting"
		}
		out := JsonObject{"stage": stage}
		maps.Copy(out, slot)
		return out
	},
	Inflight: []string{"spawning", "running"},
	Phases: map[string]PhaseHandler{
		"waiting": func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
			if err := rt.Sleep(ctx, numberOr(task.Checkpoint["untilMs"], 0)); err != nil {
				return Step{}, err
			}
			return spawnJob(ctx, task, int(numberOr(task.Checkpoint["occurrence"], 1)), rt)
		},
		"spawning": reconcileJob,
		"running":  reconcileJob,
	},
	Initial: func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
		if rt.ProcessHost == nil {
			return done(jobFailed("spawn", "no process host")), nil
		}
		now := rt.Now()
		input := jobInputOf(task)
		untilMs := now
		if input.NotBefore != nil {
			untilMs = *input.NotBefore
		}
		if untilMs > now {
			return Step{Next: Checkpoint{"phase": "waiting", "untilMs": untilMs, "occurrence": 1}}, nil
		}
		return spawnJob(ctx, task, 1, rt)
	},
	Abort: func(ctx context.Context, task Task, rt *Runtime) (AbortClosure, error) {
		killed := false
		phase := Phase(task.Checkpoint)
		if phase != "" && phase != "waiting" && rt.ProcessHost != nil {
			key := str(task.Checkpoint, "key")
			if err := rt.ProcessHost.Kill(ctx, key, "SIGTERM"); err != nil {
				return nil, err
			}
			if err := rt.Sleep(ctx, rt.Now()+5000); err != nil {
				return nil, err
			}
			if err := rt.ProcessHost.Kill(ctx, key, "SIGKILL"); err != nil {
				return nil, err
			}
			killed = true
		}
		return func(context.Context, *Tx, Task) (JsonValue, error) { return JsonObject{"killed": killed}, nil }, nil
	},
}

func checkpointJob(ctx context.Context, rt *Runtime, checkpoint Checkpoint) error {
	_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
		return nil, tx.Checkpoint(checkpoint)
	})
	return err
}

func spawnJob(ctx context.Context, task Task, occurrence int, rt *Runtime) (Step, error) {
	key := fmt.Sprintf("%d:%d", task.Id, occurrence)
	if err := checkpointJob(ctx, rt, Checkpoint{"phase": "spawning", "key": key, "occurrence": occurrence}); err != nil {
		return Step{}, err
	}
	input := jobInputOf(task)
	if err := rt.ProcessHost.Start(ctx, key, input.ProcessSpec); err != nil {
		if ctx.Err() != nil {
			return Step{}, err
		}
		detail := errorString(err)
		return Step{Done: func(_ context.Context, tx *Tx, current Task) (Completion, error) {
			if input.Notify {
				if _, err := tx.Write(current.ConversationId, jobNotice(fmt.Sprintf("job %d failed to start: %s", task.Id, detail), rt.Now())); err != nil {
					return Completion{}, err
				}
			}
			return jobFailed("spawn", detail), nil
		}}, nil
	}
	if err := checkpointJob(ctx, rt, Checkpoint{"phase": "running", "key": key, "occurrence": occurrence}); err != nil {
		return Step{}, err
	}
	return pollJob(ctx, task, key, occurrence, rt)
}

func reconcileJob(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	if rt.ProcessHost == nil {
		return done(jobFailed("interrupted", "no process host after restart")), nil
	}
	key := str(task.Checkpoint, "key")
	occurrence := int(numberOr(task.Checkpoint["occurrence"], 1))
	status, err := rt.ProcessHost.Status(ctx, key)
	if err != nil {
		return done(jobFailed("interrupted", "host status failed: "+errorString(err))), nil
	}
	if status.Status == "unknown" {
		if !jobInputOf(task).Rerun {
			return done(jobFailed("interrupted", "process outcome unknown")), nil
		}
		return spawnJob(ctx, task, occurrence, rt)
	}
	if Phase(task.Checkpoint) == "spawning" {
		if err := checkpointJob(ctx, rt, Checkpoint{"phase": "running", "key": key, "occurrence": occurrence}); err != nil {
			return Step{}, err
		}
	}
	return pollJob(ctx, task, key, occurrence, rt)
}

func pollJob(ctx context.Context, task Task, key string, occurrence int, rt *Runtime) (Step, error) {
	for attempt := 0; ; attempt++ {
		status, err := rt.ProcessHost.Status(ctx, key)
		if err != nil {
			return done(jobFailed("interrupted", "host status failed: "+errorString(err))), nil
		}
		if status.Status == "unknown" {
			if !jobInputOf(task).Rerun {
				return done(jobFailed("interrupted", "process outcome unknown")), nil
			}
			return spawnJob(ctx, task, occurrence, rt)
		}
		if err := recordJobOutput(ctx, rt, status); err != nil {
			return Step{}, err
		}
		if status.Status == "exited" {
			return exitedJob(task, status, occurrence, rt), nil
		}
		delay := math.Min(1000, 100*math.Pow(2, math.Min(float64(attempt), 4)))
		if err := rt.Sleep(ctx, rt.Now()+delay); err != nil {
			return Step{}, err
		}
	}
}

func recordJobOutput(ctx context.Context, rt *Runtime, status ProcessStatus) error {
	_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
		output, err := tx.Slot(TaskRef{Id: current.Id, Kind: rt.Kind})
		if err != nil {
			return nil, err
		}
		output.Set("stdout", status.Stdout)
		output.Set("stderr", status.Stderr)
		output.Set("droppedStdout", status.DroppedStdout)
		output.Set("droppedStderr", status.DroppedStderr)
		if status.Status == "exited" {
			output.Set("exitCode", status.ExitCode)
		}
		return nil, nil
	})
	return err
}

func exitedJob(task Task, status ProcessStatus, occurrence int, rt *Runtime) Step {
	input := jobInputOf(task)
	if input.Every == nil {
		return Step{Done: func(_ context.Context, tx *Tx, current Task) (Completion, error) {
			if input.Notify {
				if _, err := tx.Write(current.ConversationId, jobNotice(fmt.Sprintf("job %d exited with code %d", task.Id, status.ExitCode), rt.Now())); err != nil {
					return Completion{}, err
				}
			}
			return Completed(JsonObject{"exitCode": status.ExitCode, "occurrences": occurrence, "stdout": status.Stdout, "stderr": status.Stderr}), nil
		}}
	}
	untilMs := rt.Now() + *input.Every
	return Step{Build: func(_ context.Context, tx *Tx, current Task) (Transition, error) {
		if input.Notify {
			if _, err := tx.Write(current.ConversationId, jobNotice(fmt.Sprintf("job %d occurrence %d exited with code %d", task.Id, occurrence, status.ExitCode), rt.Now())); err != nil {
				return Transition{}, err
			}
		}
		output, err := tx.Slot(TaskRef{Id: current.Id, Kind: rt.Kind})
		if err != nil {
			return Transition{}, err
		}
		output.Set("stdout", "")
		output.Set("stderr", "")
		output.Set("droppedStdout", 0)
		output.Set("droppedStderr", 0)
		output.Delete("exitCode")
		output.Set("occurrence", occurrence+1)
		return Transition{Checkpoint: Checkpoint{"phase": "waiting", "untilMs": untilMs, "occurrence": occurrence + 1}}, nil
	}}
}
