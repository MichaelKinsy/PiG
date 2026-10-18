package session

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// TaskInspection is one live task with the state the scheduler derives for it (types.ts TaskInspection).
type TaskInspection struct {
	Record *jv.Object
	// Kind is "running", "ready", "waiting", "sleeping", "completing" or "blocked".
	Kind     string
	Until    float64 // sleeping
	Migrates bool   // ready
	On       []ID   // waiting
	Reason   string // blocked: missing_task, task_too_old or migration_failed
	Err      error  // blocked with migration_failed: why, when known
}

// Inspect is scheduler.ts inspect: the scheduling state and every live task with its derived state. It runs no task
// code: a pending migration shows as ready with Migrates, and only a migration the scheduler already tried, or one that
// cannot exist, shows as failed.
func (s *Scheduler) Inspect(snapshot Snapshot) (scheduling string, tasks []TaskInspection, err error) {
	if _, err = s.loadScopes(false); err != nil {
		return "", nil, err
	}
	for _, rec := range s.live.values() {
		tasks = append(tasks, s.inspectTask(rec, snapshot))
	}
	switch {
	case s.closing:
		scheduling = "closing"
	case s.enabled:
		scheduling = "running"
	default:
		scheduling = "paused"
	}
	return scheduling, tasks, nil
}

func (s *Scheduler) inspectTask(rec *jv.Object, snapshot Snapshot) TaskInspection {
	out := TaskInspection{Record: rec}
	id := mustInt(rec, "id")
	if _, running := s.invocations[id]; running {
		out.Kind = "running"
		return out
	}
	if taskStatus(rec) == StatusCompleting {
		out.Kind = "completing"
		return out
	}
	if on := s.waitingOn(rec, false); len(on) > 0 {
		out.Kind, out.On = "waiting", on
		return out
	}
	if s.notDue(rec) {
		out.Kind = "sleeping"
		out.Until, _ = getNum(getObj(rec, "state"), "until")
		return out
	}
	kind, _ := getStr(rec, "kind")
	task, reason, migrates := s.fit(rec, snapshot.Task(kind))
	if reason != "" {
		out.Kind, out.Reason = "blocked", reason
		if failed, ok := s.failedMigrations[id]; ok && reason == "migration_failed" {
			out.Err = failed.err
		}
		return out
	}
	if migrates && task.Migrate == nil && task.MigrateAsync == nil {
		out.Kind, out.Reason, out.Err = "blocked", "migration_failed", missingMigration(rec, task)
		return out
	}
	out.Kind, out.Migrates = "ready", migrates
	return out
}
