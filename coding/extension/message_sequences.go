package extension

import "context"

type messageSequencesKey struct{}

// WithMessageSequences names the messages an event carries by the sequence the host gave each, in the order the event holds them: message_start and message_end have their message, turn_end its message and then its tool results, agent_end its messages. A sequence is 0 for a message that is none Pi shares with another payload. An extension process that holds the objects of earlier events uses the numbers to hand a handler the object Pi's handler holds (and to match a session entry to it).
func WithMessageSequences(ctx context.Context, sequences []uint64) context.Context {
	return context.WithValue(ctx, messageSequencesKey{}, sequences)
}

// MessageSequences returns the sequences WithMessageSequences set, or nil.
func MessageSequences(ctx context.Context) []uint64 {
	sequences, _ := ctx.Value(messageSequencesKey{}).([]uint64)
	return sequences
}
