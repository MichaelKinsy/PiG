package sessionentry

// MessageSeq says which session entry persisted the message a sequence numbers: the pair an extension process needs to tell which of its message payloads that entry's message is (Pi hands both the one object).
type MessageSeq struct {
	ID  string `json:"id"`
	Seq uint64 `json:"seq"`
}
