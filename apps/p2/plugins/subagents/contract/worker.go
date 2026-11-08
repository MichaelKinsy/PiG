package contract

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the documents of apps/p2/plugins/subagents/contract.worker.ts: the session's subagent registry (a Durable
// document other worker plugins read: one-shot agents, search, PowerShell) and the location a one-shot agent inherits.

// ThinkingLevels are the thinking levels a subagent may be given (`THINKING`).
var ThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// TeamTool is the name of the subagent tool (`TEAM_TOOL`).
const TeamTool = "team"

// LocationValue is a stored location: the cwd, on the environment Env (nil: this machine).
type LocationValue struct {
	Cwd string  `json:"cwd"`
	Env *string `json:"env"`
}

// Subagent is one entry of the registry.
type Subagent struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	Model          string                 `json:"model"`
	ThinkingLevel  string                 `json:"thinkingLevel"`
	Tools          []string               `json:"tools"`
	// Location is its own location, chosen at spawn (e.g. a git worktree on the caller's environment) and kept when the
	// session moves; nil: it follows the session.
	Location *LocationValue `json:"location,omitempty"`
	// UnplacedCwd is an own cwd from version 1, which did not record the environment: the worker places it on the
	// environment it was spawned on before the Harness is live, so no one else reads it.
	UnplacedCwd string `json:"unplacedCwd,omitempty"`
	// Parent is the top-level conversation that last spawned it or sent it a message: where `team send to: "parent"`
	// goes. Zero (spawned before it was recorded): the current main conversation.
	Parent durable.ConversationId `json:"parent,omitempty"`
	// Reported are the answers already reported: several messages can end in one answer, reported once.
	Reported []durable.EntryId `json:"reported"`
	// Archived is hidden from the sidebar's list and `status`; still usable, and `send` restores it.
	Archived bool `json:"archived,omitempty"`
}

// OneShot is a code mode script's one-shot agent (plugins/agent), listed for inspection from the script's tool card.
type OneShot struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	Label          string                 `json:"label"`
	Model          string                 `json:"model"`
	ThinkingLevel  string                 `json:"thinkingLevel"`
	Tools          []string               `json:"tools"`
}

// SubagentState is the `p2.subagents` document's value.
type SubagentState struct {
	Agents    map[string]Subagent       `json:"agents"`
	Reporters map[string]durable.TaskId `json:"reporters"`
	// OneShots are the one-shot agents by the nested call ID that ran them (`<script call ID>/c<n>`).
	OneShots map[string]OneShot `json:"oneShots,omitempty"`
	// Order is the agents' names in the document's key order (insertion: a spawn adds at the end), set by readers that
	// care about it (Pi lists them with Object.entries). Not part of the stored value.
	Order []string `json:"-"`
}

// Names are the agents' names in the document's key order, else alphabetical.
func (s SubagentState) Names() []string {
	if len(s.Order) == len(s.Agents) {
		return s.Order
	}
	names := make([]string, 0, len(s.Agents))
	for name := range s.Agents {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// SubagentsKind is the kind of the registry document.
const SubagentsKind = "p2.subagents"

// subagentV1 is a version 1 entry: an own cwd instead of an own location.
type subagentV1 struct {
	Subagent
	Cwd string `json:"cwd,omitempty"`
}

type subagentStateV1 struct {
	Agents    map[string]subagentV1     `json:"agents"`
	Reporters map[string]durable.TaskId `json:"reporters"`
	OneShots  map[string]OneShot        `json:"oneShots,omitempty"`
}

// SubagentsDoc is the session's subagent registry. Kept on conversation 1 for every top-level conversation (the main
// conversation and its `/tree` branches): subagents are session-wide and outlive branch switches.
var SubagentsDoc = durable.DefineDoc(durable.DocDefinition[SubagentState]{
	CommonDocDefinition: durable.CommonDocDefinition[SubagentState]{
		Kind: SubagentsKind,
		// 2: an own location (environment and cwd) instead of an own cwd (version 1's is `unplacedCwd` until placed).
		Version: 2,
		Initial: func() SubagentState {
			return SubagentState{Agents: map[string]Subagent{}, Reporters: map[string]durable.TaskId{}}
		},
		Migrate: func(value durable.JsonObject, fromVersion int) (SubagentState, error) {
			if fromVersion != 1 {
				return SubagentState{}, fmt.Errorf("Unknown p2.subagents version %d", fromVersion)
			}
			old, err := durable.FromJsonValue[subagentStateV1](value)
			if err != nil {
				return SubagentState{}, err
			}
			state := SubagentState{Agents: make(map[string]Subagent, len(old.Agents)), Reporters: old.Reporters, OneShots: old.OneShots}
			for name, agent := range old.Agents {
				entry := agent.Subagent
				if agent.Cwd != "" {
					entry.UnplacedCwd = agent.Cwd
				}
				state.Agents[name] = entry
			}
			return state, nil
		},
		CheckpointWhen: func(_ SubagentState, _ []durable.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 64
		},
	},
	DocumentSemantics: durable.NewLatestConversationSemantics(durable.ForkInitial).DocumentSemantics,
})

// InheritedLocationState is the `p2.inherited-location` document's value.
type InheritedLocationState struct {
	Location *LocationValue `json:"location,omitempty"`
}

// InheritedLocationDoc is the own location a one-shot agent inherited from the conversation whose script started it (a
// subagent with its own location, or another one-shot agent of one); absent: it follows the session like its caller.
var InheritedLocationDoc = durable.DefineDoc(durable.DocDefinition[InheritedLocationState]{
	CommonDocDefinition: durable.CommonDocDefinition[InheritedLocationState]{
		Kind:           "p2.inherited-location",
		Version:        1,
		Initial:        func() InheritedLocationState { return InheritedLocationState{} },
		CheckpointWhen: func(InheritedLocationState, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.NewLatestConversationSemantics(durable.ForkInitial).DocumentSemantics,
})
