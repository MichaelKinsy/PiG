// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Envs builds and keeps the environments of tasks. The core asks for one with an env effect before a task's sections and
// tools run; the section executor reads it back by task.
type Envs struct {
	// Build is HarnessOptions.Env; nil means no environment.
	Build func(ctx context.Context, target harness.EnvTarget) (env.ExecutionEnv, error)
	// Reader returns the committed document reads an environment builder may use.
	Reader func() durable.DocumentReader

	mu   sync.Mutex
	envs map[durable.TaskId]env.ExecutionEnv
}

// Get returns the environment built for a task; nil when none.
func (e *Envs) Get(task durable.TaskId) env.ExecutionEnv {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.envs[task]
}

// Handler returns the sqlhost handler of env effects.
func (e *Envs) Handler() sqlhost.Handler { return e.run }

func (e *Envs) run(ctx context.Context, call *sqlhost.Call) error {
	var p struct {
		ConversationID int64   `json:"conversationId"`
		TaskID         *int64  `json:"taskId"`
		Cwd            *string `json:"cwd"`
	}
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("env payload: %w", err)
	}
	var built env.ExecutionEnv
	var err error
	if e.Build != nil {
		built, err = e.Build(ctx, harness.EnvTarget{ConversationId: durable.ConversationId(p.ConversationID), Cwd: p.Cwd, Read: e.Reader()})
	}
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	if p.TaskID != nil {
		e.mu.Lock()
		if e.envs == nil {
			e.envs = map[durable.TaskId]env.ExecutionEnv{}
		}
		if built == nil {
			delete(e.envs, durable.TaskId(*p.TaskID))
		} else {
			e.envs[durable.TaskId(*p.TaskID)] = built
		}
		e.mu.Unlock()
	}
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: []byte("null")})
	return nil
}

// SectionBackend is what rendering a section reads from the session.
type SectionBackend interface {
	durable.DocumentReader
	// Agent is the conversation's agent as the task resolved it; its Sections are the effect's section indexes.
	Agent(ctx context.Context) (durable.Agent, error)
}

// Sections runs section effects.
type Sections struct {
	Backend func(task durable.TaskId, conversation durable.ConversationId) SectionBackend
	Envs    *Envs
}

// Handler returns the sqlhost handler of section effects.
func (s *Sections) Handler() sqlhost.Handler { return s.run }

func (s *Sections) run(ctx context.Context, call *sqlhost.Call) error {
	var p payload.SectionEffect
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("section payload: %w", err)
	}
	var in struct {
		Shown map[string]string `json:"shown"`
	}
	if len(p.Input) > 0 {
		if err := json.Unmarshal(p.Input, &in); err != nil {
			return fmt.Errorf("section input: %w", err)
		}
	}
	task, conversation := durable.TaskId(p.TaskID), durable.ConversationId(p.ConversationID)
	backend := s.Backend(task, conversation)
	text, err := func() (*string, error) {
		agent, err := backend.Agent(ctx)
		if err != nil {
			return nil, err
		}
		if p.Section < 0 || p.Section >= len(agent.Sections) {
			return nil, &protocolError{fmt.Errorf("section effect %d names section %d of %d", call.ID, p.Section, len(agent.Sections))}
		}
		var environment env.ExecutionEnv
		if s.Envs != nil {
			environment = s.Envs.Get(task)
		}
		return agent.Sections[p.Section].Render(ctx, durable.PromptInput{
			ConversationId: conversation, Agent: agent, Env: environment, Shown: in.Shown, Read: backend,
		})
	}()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		if _, ok := errors.AsType[*protocolError](err); ok {
			return err
		}
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	body, _ := json.Marshal(text)
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}
