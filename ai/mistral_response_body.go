//go:build !pig_strip_mistral_conversations

package ai

// Ports packages/ai/src/api/mistral-conversations.ts (readMistralEvents).

import (
	"context"
	"io"
	"sync"
)

// mistralResponseBody preserves the abort reason when closing a blocked read yields EOF or a transport-specific error.
type mistralResponseBody struct {
	reader    io.Reader
	closeBody func()
	request   context.Context
}

func (body *mistralResponseBody) Read(p []byte) (int, error) {
	if err := context.Cause(body.request); err != nil {
		return 0, err
	}
	n, err := body.reader.Read(p)
	if cause := context.Cause(body.request); cause != nil {
		return 0, cause
	}
	return n, err
}

func (body *mistralResponseBody) Close() error {
	body.closeBody()
	return nil
}

// mistralCloseOnce forwards the first Close to the transport body and returns its result to every later Close.
type mistralCloseOnce struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (body *mistralCloseOnce) Close() error {
	body.once.Do(func() { body.err = body.ReadCloser.Close() })
	return body.err
}
