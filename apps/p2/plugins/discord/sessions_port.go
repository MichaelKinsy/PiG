package discord

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/discord/contract"
	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// sessionsPort is what the relay needs of a session: the catalog and its worker's `DiscordIngress`.
type sessionsPort struct {
	row     func(session string) *sessioncontract.Session
	locks   *host.Reference
	natives func() (*p2runtime.ServerParts, error)
	configs func() *delta.JsonObject
}

// Usable is why the session cannot take input: it is gone, archived, or does not work in a sandbox.
func (p *sessionsPort) Usable(_ context.Context, session string) (string, error) {
	found := p.row(session)
	switch {
	case found == nil:
		return "its p2 session is gone.", nil
	case found.ArchivedAt != nil:
		return "its p2 session is archived.", nil
	case found.Env == nil || *found.Env == "" || sandboxConfig(p.configs(), *found.Env) == nil:
		return "its p2 session does not work in a sandbox.", nil
	}
	return "", nil
}

func (p *sessionsPort) Lock(session string) *p2runtime.SessionLock {
	value, _ := p.locks.Member("locks").Value()
	all := objectOf(value)
	if all == nil || all.Value(session) == nil {
		return nil
	}
	var lock p2runtime.SessionLock
	if roundTrip(all.Value(session), &lock) != nil {
		return nil
	}
	return &lock
}

// call is one call of session's worker (started if needed) as the server, in a scope of its own: the scope releases the
// worker's reference when the call ends.
func (p *sessionsPort) call(ctx context.Context, session, method string, args delta.JsonValue) (delta.JsonValue, error) {
	parts, err := p.natives()
	if err != nil {
		return nil, err
	}
	result, err := parts.Root(func(root host.Ctx) (any, error) {
		worker, err := root.Get(contract.DiscordIngress, delta.JsonObjectOf("session", session), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return worker.Call(method, args, root)
	}, ctx)
	return result, err
}

func (p *sessionsPort) Deliver(ctx context.Context, session string, input contract.Delivery) (contract.Delivered, error) {
	args := delta.JsonObjectOf("requestId", input.RequestID, "author", delta.JsonObjectOf("id", input.Author.ID, "name", input.Author.Name), "text", input.Text)
	if input.Conversation != nil {
		args.Set("conversation", float64(*input.Conversation))
	}
	where := delta.JsonObjectOf("channel", input.Context.Channel, "guild", input.Context.Guild, "messages", float64(input.Context.Messages))
	if input.Context.Thread != nil {
		where.Set("thread", *input.Context.Thread)
	}
	args.Set("context", where)
	files := make([]any, len(input.Files))
	for i, file := range input.Files {
		files[i] = delta.JsonObjectOf("url", file.URL, "name", file.Name, "size", file.Size, "key", file.Key, "ref", file.Ref)
	}
	args.Set("files", files)
	var lock delta.JsonValue
	if input.Lock != nil {
		object, err := objectFrom(input.Lock)
		if err != nil {
			return contract.Delivered{}, err
		}
		lock = object
	}
	args.Set("lock", lock)
	result, err := p.call(ctx, session, "deliver", args)
	if err != nil {
		return contract.Delivered{}, err
	}
	out := argsOf(result)
	conversation, _ := out.Value("conversation").(float64)
	submission, _ := out.Value("submissionId").(float64)
	return contract.Delivered{Conversation: int(conversation), SubmissionID: submission}, nil
}

func (p *sessionsPort) Turn(ctx context.Context, session string, submissionID float64) (contract.TurnState, error) {
	result, err := p.call(ctx, session, "turn", delta.JsonObjectOf("submissionId", submissionID))
	if err != nil {
		return contract.TurnState{}, err
	}
	out := argsOf(result)
	turn := contract.TurnState{State: str(out, "state"), Text: str(out, "text"), Reason: str(out, "reason"), Files: []contract.OfferedFile{}}
	turn.Answer, _ = out.Value("answer").(float64)
	files, _ := out.Value("files").([]any)
	for _, each := range files {
		file := argsOf(each)
		size, _ := file.Value("size").(float64)
		turn.Files = append(turn.Files, contract.OfferedFile{ID: str(file, "id"), Name: str(file, "name"), Size: size})
	}
	return turn, nil
}

func (p *sessionsPort) Thread(ctx context.Context, session, name string, before float64) (int, error) {
	result, err := p.call(ctx, session, "thread", delta.JsonObjectOf("name", name, "before", before))
	if err != nil {
		return 0, err
	}
	conversation, _ := argsOf(result).Value("conversation").(float64)
	return int(conversation), nil
}

func (p *sessionsPort) Read(ctx context.Context, session, id string, offset, length int) ([]byte, error) {
	result, err := p.call(ctx, session, "read", delta.JsonObjectOf("id", id, "offset", float64(offset), "length", float64(length)))
	if err != nil {
		return nil, err
	}
	bytes, ok := result.([]byte)
	if !ok {
		return nil, errors.New("The worker answered no bytes")
	}
	return bytes, nil
}

func (p *sessionsPort) Command(ctx context.Context, session string, conversation *int, command string) (string, error) {
	args := delta.JsonObjectOf("command", command)
	if conversation != nil {
		args.Set("conversation", float64(*conversation))
	}
	result, err := p.call(ctx, session, "command", args)
	if err != nil {
		return "", err
	}
	return str(argsOf(result), "text"), nil
}
