package extension

import "context"

// ArgumentCompletionsFunc mirrors upstream's getArgumentCompletions
// callback on RegisteredCommand. Returns a list of completion items, or nil
// to fall through to the default provider.
//
// pig translation rule (Promise<T[] | null> → ([]T, error)): upstream returns `AutocompleteItem[] | null |
// Promise<AutocompleteItem[] | null>`. The Go signature returns the slice
// + error directly; if the host needs cancellation it wraps the call in a
// goroutine + ctx.Done() select: same shape upstream takes (the function
// has no signal/ctx parameter upstream either).
type ArgumentCompletionsFunc = func(argumentPrefix string) ([]AutocompleteItem, error)

// CommandHandler is invoked when an extension command is run. Mirrors
// upstream's RegisteredCommand.handler signature.
//
// CommandHandler receives cancellation and per-extension values through the Go
// context. Use [FromContext] to access the extension context.
type CommandHandler = func(ctx context.Context, args string) error

// RegisteredCommand mirrors upstream RegisteredCommand. The host's view of
// a command after registration.
type RegisteredCommand struct {
	Name                   string                  `json:"name"`
	SourceInfo             SourceInfo              `json:"sourceInfo"`
	Description            string                  `json:"description,omitempty"`
	GetArgumentCompletions ArgumentCompletionsFunc `json:"-"`
	Handler                CommandHandler          `json:"-"`
}

// ResolvedCommand mirrors upstream ResolvedCommand: RegisteredCommand plus
// the name under which it was actually invoked (which can differ from Name
// when the command exposes aliases).
type ResolvedCommand struct {
	RegisteredCommand
	InvocationName string `json:"invocationName"`
}

// CommandOptions is the registration payload for [API.RegisterCommand]
// : upstream's `Omit<RegisteredCommand, "name" | "sourceInfo">`.
type CommandOptions struct {
	Description            string                  `json:"description,omitempty"`
	GetArgumentCompletions ArgumentCompletionsFunc `json:"-"`
	Handler                CommandHandler          `json:"-"`
}
