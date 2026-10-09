package services

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// recordingInitiator is the selected controller's admission boundary. It returns the begun invocation's error so each forwarded argument list is observable.
type recordingInitiator struct {
	AgentController
	calls []recordedCall
}

var errRecordedAdmission = errors.New("recorded admission")

func (i *recordingInitiator) admit(member string, args ...any) error {
	i.calls = append(i.calls, recordedCall{member, args})
	return errRecordedAdmission
}
func (i *recordingInitiator) BeginPrompt(_ context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error) {
	return nil, i.admit("prompt", request)
}
func (i *recordingInitiator) BeginAbort(context.Context) (*chord.ServiceInvocation, error) {
	return nil, i.admit("abort")
}
func (i *recordingInitiator) BeginSteer(_ context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error) {
	return nil, i.admit("steer", request)
}
func (i *recordingInitiator) BeginFollowUp(_ context.Context, request AgentPromptRequest) (*chord.ServiceResultInvocation[AgentQueueResponse], error) {
	return nil, i.admit("followUp", request)
}
func (i *recordingInitiator) BeginCancelQueued(_ context.Context, id string) (*chord.ServiceResultInvocation[AgentCancelQueuedResponse], error) {
	return nil, i.admit("cancelQueued", id)
}
func (i *recordingInitiator) BeginCompact(_ context.Context, request AgentCompactionRequest) (*chord.ServiceResultInvocation[AgentOperationResponse], error) {
	return nil, i.admit("compact", request)
}
func (i *recordingInitiator) BeginWaitForPrompt(_ context.Context, id string) (*chord.ServiceResultInvocation[AgentPromptResult], error) {
	return nil, i.admit("waitForPrompt", id)
}

// upstream: agent-controller.ts:38-55. The selected view's admission forwards each member's own arguments to the selected controller and returns its admission error unchanged.
func TestAgentControllerViewAdmissionForwardsEveryMemberArguments(t *testing.T) {
	prompt := AgentPromptRequest{Message: "hello", Images: []AgentPromptImage{{Type: "image", Data: "AA==", MimeType: "image/png"}}}
	compaction := AgentCompactionRequest{CustomInstructions: new("shorter")}
	selected := &recordingInitiator{}
	view := agentControllerView{resolve: func() (AgentController, error) { return selected, nil }}
	for _, test := range []struct {
		member string
		want   []any
		begin  func(context.Context) error
	}{
		{"prompt", []any{prompt}, func(ctx context.Context) error { _, err := view.BeginPrompt(ctx, prompt); return err }},
		{"abort", nil, func(ctx context.Context) error { _, err := view.BeginAbort(ctx); return err }},
		{"steer", []any{prompt}, func(ctx context.Context) error { _, err := view.BeginSteer(ctx, prompt); return err }},
		{"followUp", []any{prompt}, func(ctx context.Context) error { _, err := view.BeginFollowUp(ctx, prompt); return err }},
		{"cancelQueued", []any{"entry"}, func(ctx context.Context) error { _, err := view.BeginCancelQueued(ctx, "entry"); return err }},
		{"compact", []any{compaction}, func(ctx context.Context) error { _, err := view.BeginCompact(ctx, compaction); return err }},
		{"waitForPrompt", []any{"operation"}, func(ctx context.Context) error { _, err := view.BeginWaitForPrompt(ctx, "operation"); return err }},
	} {
		t.Run(test.member, func(t *testing.T) {
			selected.calls = nil
			if err := test.begin(t.Context()); !errors.Is(err, errRecordedAdmission) {
				t.Fatalf("admission error = %v", err)
			}
			if len(selected.calls) != 1 || selected.calls[0].member != test.member || !reflect.DeepEqual(selected.calls[0].args, test.want) {
				t.Fatalf("selected calls = %#v, want one %s%v", selected.calls, test.member, test.want)
			}
		})
	}
}

// upstream: packages/chord/src/facets/host.ts in-host loopback. A selected remote client forwards the loopback member's complete argument list into its own admission.
func TestRemoteAgentControllerBeginServiceMemberForwardsArguments(t *testing.T) {
	controller, transport := newControllerAdmission(t)
	member := controller.(interface {
		BeginServiceMember(context.Context, string, []json.RawMessage) (*chord.ServiceInvocation, error)
	})
	args := []json.RawMessage{json.RawMessage(`{"message":"hi","images":null}`), json.RawMessage(`"second"`)}
	operation, err := member.BeginServiceMember(t.Context(), "steer", args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(transport.calls) != 1 || transport.calls[0].Member != "steer" || transport.calls[0].ServiceId != AgentControllerID || !reflect.DeepEqual(transport.calls[0].Args, args) {
		t.Fatalf("remote admission = %+v, want steer with %s", transport.calls, args)
	}
}

// upstream: slash-commands.ts:22-27. register rejects an existing name; replace stages. The guarded view must reach each registry member, not its sibling.
func TestSlashCommandsViewRegisterAndReplaceReachTheirOwnMembers(t *testing.T) {
	registry := NewSlashCommandRegistry()
	view := slashCommandsView{resolve: func() (SlashCommands, error) { return registry, nil }}
	command := SlashCommandContribution{Name: "dup"}
	closeFirst, err := view.Register(command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.Register(command); err == nil || err.Error() != "Slash command /dup is already registered" {
		t.Fatalf("second Register = %v", err)
	}
	closeReplacement, err := view.Replace(command)
	if err != nil {
		t.Fatalf("Replace = %v", err)
	}
	identity := view.List()[0].RegistrationIdentity()
	closeFirst()
	if got := view.List(); len(got) != 1 || got[0].RegistrationIdentity() == identity {
		t.Fatalf("replacement did not take over after the first registration closed: %+v", got)
	}
	closeReplacement()
	if got := view.List(); len(got) != 0 {
		t.Fatalf("commands after closing both = %+v", got)
	}
}
