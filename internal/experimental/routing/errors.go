package routing

// Ports packages/server/src/errors.ts

const InternalServerErrorMessage = "Internal server error"

// ServerError is a host or lifecycle failure safe to expose across the protocol boundary.
type ServerError struct {
	Code    string
	Message string
}

func (e *ServerError) Error() string { return e.Message }

type WrongServerError struct{ *ServerError }

func NewWrongServerError() *WrongServerError {
	return &WrongServerError{&ServerError{"wrong_server", "Request was addressed to another server"}}
}
func (e *WrongServerError) Unwrap() error { return e.ServerError }

type SessionNotFoundError struct{ *ServerError }

func NewSessionNotFoundError(messages ...string) *SessionNotFoundError {
	message := "Session was not found"
	if len(messages) != 0 {
		message = messages[0]
	}
	return &SessionNotFoundError{&ServerError{"session_not_found", message}}
}
func (e *SessionNotFoundError) Unwrap() error { return e.ServerError }

type SessionAmbiguousError struct{ *ServerError }

func NewSessionAmbiguousError() *SessionAmbiguousError {
	return &SessionAmbiguousError{&ServerError{"session_ambiguous", "Session ID matches more than one session"}}
}
func (e *SessionAmbiguousError) Unwrap() error { return e.ServerError }

type SessionNotAttachedError struct{ *ServerError }

func NewSessionNotAttachedError() *SessionNotAttachedError {
	return &SessionNotAttachedError{&ServerError{"session_not_attached", "Session is not attached to this client"}}
}
func (e *SessionNotAttachedError) Unwrap() error { return e.ServerError }

type ServerDrainingError struct{ *ServerError }

func NewServerDrainingError() *ServerDrainingError {
	return &ServerDrainingError{&ServerError{"server_draining", "Server is draining"}}
}
func (e *ServerDrainingError) Unwrap() error { return e.ServerError }

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
