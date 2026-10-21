package boundarygoroutine

// The #249 shape: the constructor of the IPC channel starts its reader before the caller registers a "message" listener, so a message that arrives first has no one to receive it and is dropped.

type nodeChannel struct {
	messages chan []byte
	listener func(msg []byte)
}

func newNodeChannel() *nodeChannel {
	channel := &nodeChannel{messages: make(chan []byte, 8)}
	go channel.read() // want `go statement in constructor newNodeChannel starts work before the caller can register its consumer`
	return channel
}

func (c *nodeChannel) read() {
	for msg := range c.messages {
		if c.listener != nil {
			c.listener(msg)
		}
	}
}

// The repair starts the reader once the consumer exists, and hands messages to the consumer's channel, so no callback runs on the reader's goroutine.
type startedChannel struct {
	messages chan []byte
	consumer chan []byte
}

func startNodeChannel(consumer chan []byte) *startedChannel {
	channel := &startedChannel{messages: make(chan []byte, 8), consumer: consumer}
	channel.start()
	return channel
}

func (c *startedChannel) start() { go c.read() }

func (c *startedChannel) read() {
	for msg := range c.messages {
		c.consumer <- msg
	}
}
