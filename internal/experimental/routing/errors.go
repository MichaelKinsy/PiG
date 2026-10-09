package routing

// Ports packages/server/src/errors.ts

const InternalServerErrorMessage = "Internal server error"

// ServerOperationErrorCode is the code of a [ServerError]: one of the chord remote service error codes or a server lifecycle code
// (errors.ts:3-8). Go has no union of named types, so a chord code converts with ServerOperationErrorCode(code).
type ServerOperationErrorCode string

// The server lifecycle codes of ServerOperationErrorCode.
const (
	ServerErrorWrongServer        ServerOperationErrorCode = "wrong_server"
	ServerErrorSessionNotFound    ServerOperationErrorCode = "session_not_found"
	ServerErrorSessionAmbiguous   ServerOperationErrorCode = "session_ambiguous"
	ServerErrorSessionNotAttached ServerOperationErrorCode = "session_not_attached"
	ServerErrorServerDraining     ServerOperationErrorCode = "server_draining"
)

// ServerError is a host or lifecycle failure safe to expose across the protocol boundary.
type ServerError struct {
	Code    ServerOperationErrorCode
	Message string
}

func (e *ServerError) Error() string { return e.Message }

// Name is the upstream error's `name`.
func (*ServerError) Name() string { return "ServerError" }

// NewServerError is `new ServerError(code, message)`.
func NewServerError(code ServerOperationErrorCode, message string) *ServerError {
	return &ServerError{Code: code, Message: message}
}

type WrongServerError struct{ *ServerError }

func NewWrongServerError() *WrongServerError {
	return &WrongServerError{NewServerError(ServerErrorWrongServer, "Request was addressed to another server")}
}
func (e *WrongServerError) Unwrap() error { return e.ServerError }

// Name is the upstream subclass's `name`.
func (*WrongServerError) Name() string { return "WrongServerError" }

type SessionNotFoundError struct{ *ServerError }

func NewSessionNotFoundError(messages ...string) *SessionNotFoundError {
	message := "Session was not found"
	if len(messages) != 0 {
		message = messages[0]
	}
	return &SessionNotFoundError{NewServerError(ServerErrorSessionNotFound, message)}
}

func (e *SessionNotFoundError) Unwrap() error { return e.ServerError }

// Name is the upstream subclass's `name`.
func (*SessionNotFoundError) Name() string { return "SessionNotFoundError" }

type SessionAmbiguousError struct{ *ServerError }

func NewSessionAmbiguousError() *SessionAmbiguousError {
	return &SessionAmbiguousError{NewServerError(ServerErrorSessionAmbiguous, "Session ID matches more than one session")}
}
func (e *SessionAmbiguousError) Unwrap() error { return e.ServerError }

// Name is the upstream subclass's `name`.
func (*SessionAmbiguousError) Name() string { return "SessionAmbiguousError" }

type SessionNotAttachedError struct{ *ServerError }

func NewSessionNotAttachedError() *SessionNotAttachedError {
	return &SessionNotAttachedError{NewServerError(ServerErrorSessionNotAttached, "Session is not attached to this client")}
}
func (e *SessionNotAttachedError) Unwrap() error { return e.ServerError }

// Name is the upstream subclass's `name`.
func (*SessionNotAttachedError) Name() string { return "SessionNotAttachedError" }

type ServerDrainingError struct{ *ServerError }

func NewServerDrainingError() *ServerDrainingError {
	return &ServerDrainingError{NewServerError(ServerErrorServerDraining, "Server is draining")}
}
func (e *ServerDrainingError) Unwrap() error { return e.ServerError }

// Name is the upstream subclass's `name`.
func (*ServerDrainingError) Name() string { return "ServerDrainingError" }

type aggregateError struct {
	message string
	causes  []error
}

func (e *aggregateError) Error() string   { return e.message }
func (e *aggregateError) Unwrap() []error { return e.causes }
func routingErrors(message string, failures []error, single bool) error {
	var errors []error
	for _, err := range failures {
		if err != nil {
			errors = append(errors, err)
		}
	}
	if len(errors) == 0 {
		return nil
	}
	if single && len(errors) == 1 {
		return errors[0]
	}
	return &aggregateError{message, errors}
}
