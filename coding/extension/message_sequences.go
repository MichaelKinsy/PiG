package extension

import "context"

type messageSequencesKey struct{}

// WithMessageSequences names the messages an event carries by the sequence the host gave each, in the order the event holds them: message_start and message_end have their message, turn_end its message and then its tool results, agent_end its messages. A sequence is 0 for a message that is none Pi shares with another payload. An extension process that holds the objects of earlier events uses the numbers to hand a handler the object Pi's handler holds (and to match a session entry to it).
//
// sequences is called when a Node extension handler is about to receive the event, and not for an event nobody of that kind receives: numbering a message costs allocations a session with no Node extension has no use for. A nil sequences names no message.
func WithMessageSequences(ctx context.Context, sequences func() []uint64) context.Context {
	return context.WithValue(ctx, messageSequencesKey{}, sequences)
}

// MessageSequences returns the sequences WithMessageSequences gave, or nil.
func MessageSequences(ctx context.Context) []uint64 {
	sequences, _ := ctx.Value(messageSequencesKey{}).(func() []uint64)
	if sequences == nil {
		return nil
	}
	return sequences()
}
