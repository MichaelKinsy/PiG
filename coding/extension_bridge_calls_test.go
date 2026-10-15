package coding

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi packages/coding-agent/src/core/extensions/wrapper.ts wrapRegisteredTool and tool-definition-wrapper.ts wrapToolDefinition: the wrapped tool declares the schema the
// extension gave (the validation schema first), runs the definition's prepareArguments when it has one and passes the arguments through when it has not, reserves a call-order
// place only when the definition reserves one, and gives a definition that keeps a call order the order of the call being run.
// mutation-checked: swapping the schema fallbacks, the prepareArguments nil check, the reservation nil checks, or the call-order hand-off fails it.
func TestWrappedRegisteredToolSchemaPrepareAndReservationFollowTheDefinition(t *testing.T) {
	wrap := func(t *testing.T, definition extension.ToolDefinition) agent.AgentTool {
		t.Helper()
		definition.Name = "wrapped"
		tool, err := WrapRegisteredTool(extension.RegisteredTool{Definition: definition}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return tool
	}
	schemaOf := func(tool agent.AgentTool) string {
		provider, ok := tool.(interface{ ArgumentSchema() json.RawMessage })
		if !ok {
			t.Fatal("the wrapped tool has no ArgumentSchema")
		}
		return string(provider.ArgumentSchema())
	}
	t.Run("argument schema prefers the validation schema, then the parameters, then an empty object", func(t *testing.T) {
		if got := schemaOf(wrap(t, extension.ToolDefinition{Parameters: json.RawMessage(`{"type":"object","properties":{"a":{}}}`), ValidationParameters: json.RawMessage(`{"type":"object","properties":{"b":{}}}`)})); got != `{"type":"object","properties":{"b":{}}}` {
			t.Errorf("with both schemas: %s", got)
		}
		if got := schemaOf(wrap(t, extension.ToolDefinition{Parameters: json.RawMessage(`{"type":"object","properties":{"a":{}}}`)})); got != `{"type":"object","properties":{"a":{}}}` {
			t.Errorf("with parameters only: %s", got)
		}
		if got := schemaOf(wrap(t, extension.ToolDefinition{})); got != `{}` {
			t.Errorf("with no schema: %s", got)
		}
	})
	t.Run("prepareArguments runs when the definition has it and is the identity when it has not", func(t *testing.T) {
		preparer := func(tool agent.AgentTool) agent.ArgumentPreparer {
			prepared, ok := tool.(agent.ArgumentPreparer)
			if !ok {
				t.Fatal("the wrapped tool is not an ArgumentPreparer")
			}
			return prepared
		}
		if got, err := preparer(wrap(t, extension.ToolDefinition{})).PrepareArguments(json.RawMessage(`{"x":1}`)); err != nil || string(got) != `{"x":1}` {
			t.Errorf("without prepareArguments: %s, %v", got, err)
		}
		def := extension.ToolDefinition{PrepareArguments: func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{"prepared":true}`), nil }}
		if got, err := preparer(wrap(t, def)).PrepareArguments(json.RawMessage(`{"x":1}`)); err != nil || string(got) != `{"prepared":true}` {
			t.Errorf("with prepareArguments: %s, %v", got, err)
		}
		failure := errors.New("cannot prepare")
		def.PrepareArguments = func(json.RawMessage) (json.RawMessage, error) { return nil, failure }
		if _, err := preparer(wrap(t, def)).PrepareArguments(json.RawMessage(`{}`)); !errors.Is(err, failure) {
			t.Errorf("prepareArguments error = %v, want %v", err, failure)
		}
	})
	t.Run("a mutation place is reserved only when the definition reserves one", func(t *testing.T) {
		orderable := func(tool agent.AgentTool) agent.QueueOrderable {
			reserved, ok := tool.(agent.QueueOrderable)
			if !ok {
				t.Fatal("the wrapped tool is not QueueOrderable")
			}
			return reserved
		}
		if ticket, ok := orderable(wrap(t, extension.ToolDefinition{})).ReserveMutationOrder(json.RawMessage(`{}`)); ok || ticket != nil {
			t.Errorf("a definition without ReserveCallOrder reserved %v, %v", ticket, ok)
		}
		none := extension.ToolDefinition{ReserveCallOrder: func(json.RawMessage) *extension.CallOrder { return nil }}
		if ticket, ok := orderable(wrap(t, none)).ReserveMutationOrder(json.RawMessage(`{}`)); ok || ticket != nil {
			t.Errorf("a definition that reserved no order reserved %v, %v", ticket, ok)
		}
		waited, released := 0, 0
		some := extension.ToolDefinition{ReserveCallOrder: func(json.RawMessage) *extension.CallOrder {
			return &extension.CallOrder{Wait: func() { waited++ }, Release: func() { released++ }}
		}}
		ticket, ok := orderable(wrap(t, some)).ReserveMutationOrder(json.RawMessage(`{}`))
		if !ok || ticket == nil {
			t.Fatal("a definition that reserved an order did not reserve a ticket")
		}
		ticket.Wait()
		ticket.Release()
		if waited != 1 || released != 1 {
			t.Errorf("ticket wait/release reached the order %d/%d times, want 1/1", waited, released)
		}
	})
	t.Run("a definition that keeps a call order runs with the call's order and releases it afterwards", func(t *testing.T) {
		waited, released := 0, 0
		var sawOrder bool
		def := extension.ToolDefinition{
			ReserveCallOrder: func(json.RawMessage) *extension.CallOrder { return nil },
			Execute: func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				order, ok := extension.CallOrderFromContext(ctx)
				sawOrder = ok && order != nil
				if sawOrder {
					order.Wait()
				}
				return extension.AgentToolResult{}, nil
			},
		}
		ctx := agent.WithMutationTicket(context.Background(), &agent.MutationTicket{Wait: func() { waited++ }, Release: func() { released++ }})
		if _, err := wrap(t, def).Execute(ctx, "call-1", json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
		if !sawOrder || waited != 1 || released != 1 {
			t.Errorf("order seen = %v, waited %d, released %d, want the ticket's order seen, waited once and released once", sawOrder, waited, released)
		}
		// Without a ReserveCallOrder the definition runs without a call order.
		def.ReserveCallOrder = nil
		sawOrder = true
		def.Execute = func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			_, sawOrder = extension.CallOrderFromContext(ctx)
			return extension.AgentToolResult{}, nil
		}
		if _, err := wrap(t, def).Execute(ctx, "call-2", json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
		if sawOrder {
			t.Error("a definition without ReserveCallOrder ran with a call order")
		}
	})
}
