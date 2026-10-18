// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Inspect is Harness.inspect (harness.ts inspect, a readOnLine job): the core answers the inspect query "harness" with
// {scheduling, tasks: [{record, state}], submissions}, the live tasks with the state the scheduler derives and the queued
// then placed submissions in ID order.
func (s *coreSession) Inspect(ctx context.Context) (harness.HarnessInspection, error) {
	var out harness.HarnessInspection
	release, err := s.admit()
	if err != nil {
		return out, err
	}
	defer release()
	raw, err := s.client.Inspect(ctx, map[string]any{"query": "harness"})
	if err != nil {
		return out, err
	}
	var wire struct {
		Scheduling string `json:"scheduling"`
		Tasks      []struct {
			Record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] `json:"record"`
			State  struct {
				Kind     harness.TaskInspectionKind `json:"kind"`
				Migrates bool                       `json:"migrates"`
				On       []durable.TaskId           `json:"on"`
				Until    float64                    `json:"until"`
				Reason   string                     `json:"reason"`
				Error    *CoreError                 `json:"error"`
			} `json:"state"`
		} `json:"tasks"`
		Submissions []durable.SubmissionRecord `json:"submissions"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return out, err
	}
	out.Scheduling = wire.Scheduling
	out.Submissions = wire.Submissions
	out.Tasks = make([]harness.TaskInspection, len(wire.Tasks))
	for i, t := range wire.Tasks {
		state := harness.TaskInspectionState{Kind: t.State.Kind, Migrates: t.State.Migrates, On: t.State.On, Until: t.State.Until, Reason: t.State.Reason}
		if e := t.State.Error; e != nil {
			state.Error = e
		}
		out.Tasks[i] = harness.TaskInspection{Record: t.Record, State: state}
	}
	return out, nil
}
