package sdk

// runContextEvent is the work of a context event in handleArmedRequest, past the frame read: decode the payload, run the handler, make the result and
// encode the reply.
func runContextEvent(args []byte, handler func(data map[string]any) any) ([]byte, error) {
	data, order, _ := unmarshalFrameObject(args)
	snapshot := snapshotContextMessages(EventContext, data)
	result := contextEventResult(data, snapshot, handler(data), func() []any {
		var master map[string]any
		if unmarshalFrameMember(args, &master) != nil {
			return nil
		}
		messages, _ := master["messages"].([]any)
		return messages
	})
	return encodeResponse("id", handedBack(EventContext, result, order), nil)
}
