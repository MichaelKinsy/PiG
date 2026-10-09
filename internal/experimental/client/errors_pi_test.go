package client

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// The golden file holds Pi's packages/client/src/errors.ts classes (ServerError, DisconnectedError, ClientDisposedError,
// toDisconnectedError) run on a fixed corpus by testdata/errors-oracle.mjs. Regenerate it from the repository root with:
//
//	node internal/experimental/client/testdata/errors-oracle.mjs > internal/experimental/client/testdata/errors-golden.json
type errorsGoldenError struct {
	Name         string  `json:"name"`
	Message      string  `json:"message"`
	Text         string  `json:"text"`
	IsError      bool    `json:"isError"`
	HasCause     bool    `json:"hasCause"`
	CauseMessage *string `json:"causeMessage"`
	Code         *string `json:"code"`
}

type errorsGolden struct {
	Server []struct {
		InCode    string `json:"inCode"`
		InMessage string `json:"inMessage"`
		errorsGoldenError
	} `json:"server"`
	Disconnected []struct {
		InMessage *string `json:"inMessage"`
		InCause   *string `json:"inCause"`
		errorsGoldenError
	} `json:"disconnected"`
	Disposed errorsGoldenError `json:"disposed"`
	To       []struct {
		Input       string `json:"input"`
		Passthrough bool   `json:"passthrough"`
		errorsGoldenError
	} `json:"to"`
}

// jsString is Error.prototype.toString for an error with a name and a message.
func jsString(name, message string) string {
	if message == "" {
		return name
	}
	return name + ": " + message
}

func causeOf(err error) *string {
	if cause := errors.Unwrap(err); cause != nil {
		message := cause.Error()
		return &message
	}
	return nil
}

func compareError(t *testing.T, label string, want errorsGoldenError, name, message string, err error) {
	t.Helper()
	if name != want.Name || message != want.Message || jsString(name, message) != want.Text {
		t.Errorf("%s = %q %q (%q), Pi %q %q (%q)", label, name, message, jsString(name, message), want.Name, want.Message, want.Text)
	}
	if !want.IsError || err == nil {
		t.Errorf("%s: Pi isError=%v, Go error=%v", label, want.IsError, err)
	}
	got := causeOf(err)
	switch {
	case want.CauseMessage == nil && got != nil:
		t.Errorf("%s has cause %q, Pi has none", label, *got)
	case want.CauseMessage != nil && (got == nil || *got != *want.CauseMessage):
		t.Errorf("%s cause = %v, Pi %q", label, got, *want.CauseMessage)
	}
}

// errors.ts:3-25 and 27-34: the constructors set name, message, code and cause as Pi's do, a DisconnectedError built without a message says
// "Client is disconnected", and toDisconnectedError wraps any other error with itself as the cause but returns a DisconnectedError unchanged.
func TestClientErrorsMatchPi(t *testing.T) {
	data, err := os.ReadFile("testdata/errors-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden errorsGolden
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Server) == 0 || len(golden.Disconnected) == 0 || len(golden.To) == 0 {
		t.Fatal("the golden file is empty; regenerate it with testdata/errors-oracle.mjs")
	}
	for _, c := range golden.Server {
		server := NewServerError(protocol.ProtocolError{Code: c.InCode, Message: c.InMessage})
		compareError(t, "ServerError("+c.InCode+")", c.errorsGoldenError, server.Name(), server.Error(), server)
		if c.Code == nil || server.Code != *c.Code {
			t.Errorf("ServerError(%s).Code = %q, Pi %v", c.InCode, server.Code, c.Code)
		}
	}
	for _, c := range golden.Disconnected {
		var cause error
		if c.InCause != nil {
			cause = errors.New(*c.InCause)
		}
		var disconnected *DisconnectedError
		if c.InMessage == nil {
			// new DisconnectedError() or (undefined, cause): the default message of an omitted one.
			disconnected = NewDisconnectedError(disconnectedError().Error(), cause)
		} else {
			disconnected = NewDisconnectedError(*c.InMessage, cause)
		}
		compareError(t, "DisconnectedError", c.errorsGoldenError, disconnected.Name(), disconnected.Error(), disconnected)
		if c.HasCause != (c.InCause != nil) {
			t.Errorf("DisconnectedError case %v/%v: Pi hasCause=%v", c.InMessage, c.InCause, c.HasCause)
		}
	}
	disposed := NewClientDisposedError()
	compareError(t, "ClientDisposedError", golden.Disposed, disposed.Name(), disposed.Error(), disposed)
	if golden.Disposed.HasCause || disposed.Cause() != nil {
		t.Errorf("ClientDisposedError has a cause: Pi %v, Go %v", golden.Disposed.HasCause, disposed.Cause())
	}
	for _, c := range golden.To {
		input := errors.New(c.Input)
		if c.Passthrough {
			input = NewDisconnectedError(c.Input, errors.New(*c.CauseMessage))
		}
		got := toDisconnectedError(input)
		//nolint:errorlint // Pi checks identity: toDisconnectedError returns its input itself when it is already a DisconnectedError
		if (got == input) != c.Passthrough {
			t.Errorf("toDisconnectedError(%q) returned its input = %v, Pi %v", c.Input, got == input, c.Passthrough) //nolint:errorlint // identity, as above
		}
		var disconnected *DisconnectedError
		if !errors.As(got, &disconnected) {
			t.Errorf("toDisconnectedError(%q) = %T, want a DisconnectedError", c.Input, got)
			continue
		}
		compareError(t, "toDisconnectedError("+c.Input+")", c.errorsGoldenError, disconnected.Name(), disconnected.Error(), got)
	}
}
