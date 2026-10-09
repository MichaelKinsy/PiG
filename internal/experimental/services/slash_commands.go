// Ports packages/coding-agent/src/experimental/services/slash-commands.ts.
package services

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// SlashCommandCompletion is one argument completion. A nil description is omitted.
type SlashCommandCompletion struct {
	Value       string
	Label       string
	Description *string
}

// SlashCommandRunResult is an operation response, a queue response, or nil for no response.
type SlashCommandRunResult interface {
	isSlashCommandRunResult()
}

func (AgentOperationResponse) isSlashCommandRunResult() {}
func (AgentQueueResponse) isSlashCommandRunResult()     {}

// SlashCommandContribution supplies a command and its optional completion callback. Callbacks block until the upstream Promise settles and return its rejection as an error. Nil completions preserve the upstream null result.
type SlashCommandContribution struct {
	Name                   string
	Description            *string
	ArgumentHint           *string
	GetArgumentCompletions func(string) ([]SlashCommandCompletion, error)
	Run                    func(context.Context, string) (SlashCommandRunResult, error)
	registration           *SlashCommandRegistrationIdentity
	origin                 *SlashCommandOrigin
}

// SlashCommandRegistrationIdentity is the opaque identity of one frozen registry command object. Its nonzero size preserves distinct live Go pointer identities.
type SlashCommandRegistrationIdentity struct{ marker byte }

// SlashCommandOrigin identifies an originating callback object inside one isolated runtime, without exposing or comparing Go function pointers.
type SlashCommandOrigin struct {
	Owner  string
	Handle string
}

// RegistrationIdentity remains stable across List snapshots and changes on every registration or replacement admission.
func (command SlashCommandContribution) RegistrationIdentity() *SlashCommandRegistrationIdentity {
	return command.registration
}

// Origin returns the supplied runtime callback identity, if this contribution came from a bridge.
func (command SlashCommandContribution) Origin() (SlashCommandOrigin, bool) {
	if command.origin == nil {
		return SlashCommandOrigin{}, false
	}
	return *command.origin, true
}

// WithOrigin returns a contribution carrying the bridge's owner/handle identity. The registry still assigns a fresh command-object identity when it admits the contribution.
func (command SlashCommandContribution) WithOrigin(origin SlashCommandOrigin) (SlashCommandContribution, error) {
	if origin.Owner == "" || origin.Handle == "" {
		return command, errors.New("Slash command origin requires an owner and handle")
	}
	command.origin = &origin
	return command, nil
}

// SlashCommands owns ordered local contributions. Replace stages a generation until its predecessor retires. Listener hydration and publications are synchronous on the presentation's owner executor.
type SlashCommands interface {
	Register(SlashCommandContribution) (func(), error)
	Replace(SlashCommandContribution) (func(), error)
	List() []SlashCommandContribution
	Subscribe(func([]SlashCommandContribution)) func()
}

// SlashCommandsDefinition names the local service token; Go shares type and value names.
var SlashCommandsDefinition = chord.DefineService[SlashCommands]("pi.local.slash-commands", chord.ServiceOptions{Local: true})

func init() {
	chord.RegisterServiceView(SlashCommandsDefinition, func(resolve func() (SlashCommands, error)) SlashCommands {
		return slashCommandsView{resolve: resolve}
	})
}

type slashCommandsView struct {
	resolve func() (SlashCommands, error)
}

func (view slashCommandsView) Register(command SlashCommandContribution) (func(), error) {
	service, err := view.resolve()
	if err != nil {
		return nil, err
	}
	return service.Register(command)
}

func (view slashCommandsView) Replace(command SlashCommandContribution) (func(), error) {
	service, err := view.resolve()
	if err != nil {
		return nil, err
	}
	return service.Replace(command)
}

func (view slashCommandsView) List() []SlashCommandContribution {
	service, err := view.resolve()
	if err != nil {
		panic(err)
	}
	return service.List()
}

func (view slashCommandsView) Subscribe(listener func([]SlashCommandContribution)) func() {
	service, err := view.resolve()
	if err != nil {
		panic(err)
	}
	return service.Subscribe(listener)
}
