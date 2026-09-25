package harness

import (
	"encoding/json"
)

// TaggedError is an expected harness failure returned in a result's error
// channel. Its JSON form is {"_tag", "message", "name", ...fields}, matching
// the upstream tagged-error toJSON.
type TaggedError interface {
	error
	Tag() string
}

// taggedFields marshals a tagged error: _tag, message, name, then the
// variant's own fields in declaration order. The name is the tag unless the
// variant carries its own name field, which upstream assigns over it.
func taggedFields(tag, message string, fields any) ([]byte, error) {
	return namedTaggedFields(tag, tag, message, fields)
}

func namedTaggedFields(tag, name, message string, fields any) ([]byte, error) {
	head, err := json.Marshal(struct {
		Tag     string `json:"_tag"`
		Message string `json:"message"`
		Name    string `json:"name"`
	}{tag, message, name})
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if len(body) <= 2 {
		return head, nil
	}
	head = append(head[:len(head)-1], ',')
	return append(head, body[1:]...), nil
}

// LaneBusy reports that a lane already has a current operation.
type LaneBusy struct {
	Lane          string `json:"lane"`
	OperationID   string `json:"operationId"`
	OperationKind string `json:"operationKind"`
	Message       string `json:"-"`
}

func (err *LaneBusy) Error() string { return err.Message }
func (*LaneBusy) Tag() string       { return "LaneBusy" }
func (err *LaneBusy) MarshalJSON() ([]byte, error) {
	type fields LaneBusy
	return taggedFields(err.Tag(), err.Message, (*fields)(err))
}

// OperationMismatch reports an expected operation id that is not current.
type OperationMismatch struct {
	Lane                string  `json:"lane"`
	ExpectedOperationID string  `json:"expectedOperationId"`
	CurrentOperationID  *string `json:"currentOperationId,omitempty"`
	LastOperationID     *string `json:"lastOperationId,omitempty"`
	Message             string  `json:"-"`
}

func (err *OperationMismatch) Error() string { return err.Message }
func (*OperationMismatch) Tag() string       { return "OperationMismatch" }
func (err *OperationMismatch) MarshalJSON() ([]byte, error) {
	type fields OperationMismatch
	return taggedFields(err.Tag(), err.Message, (*fields)(err))
}

// laneError is the shared {lane, message} payload.
type laneError struct {
	Lane    string `json:"lane"`
	Message string `json:"-"`
}

// NoActiveRun reports a lane without a current run.
type NoActiveRun laneError

func (err *NoActiveRun) Error() string { return err.Message }
func (*NoActiveRun) Tag() string       { return "NoActiveRun" }
func (err *NoActiveRun) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, (*laneError)(err))
}

// NoActiveOperation reports a lane without a current operation.
type NoActiveOperation laneError

func (err *NoActiveOperation) Error() string { return err.Message }
func (*NoActiveOperation) Tag() string       { return "NoActiveOperation" }
func (err *NoActiveOperation) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, (*laneError)(err))
}

// NothingToResume reports a lane without an operation to resume.
type NothingToResume laneError

func (err *NothingToResume) Error() string { return err.Message }
func (*NothingToResume) Tag() string       { return "NothingToResume" }
func (err *NothingToResume) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, (*laneError)(err))
}

// NothingToCompact reports a lane whose transcript has nothing to compact.
type NothingToCompact laneError

func (err *NothingToCompact) Error() string { return err.Message }
func (*NothingToCompact) Tag() string       { return "NothingToCompact" }
func (err *NothingToCompact) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, (*laneError)(err))
}

// laneReasonError is the shared {lane, reason, message} payload.
type laneReasonError struct {
	Lane    string `json:"lane"`
	Reason  string `json:"reason"`
	Message string `json:"-"`
}

// InvalidMessage reports an empty or invalid request message.
type InvalidMessage laneReasonError

func (err *InvalidMessage) Error() string { return err.Message }
func (*InvalidMessage) Tag() string       { return "InvalidMessage" }
func (err *InvalidMessage) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, (*laneReasonError)(err))
}

// InvalidNavigation reports an invalid navigation request.
type InvalidNavigation laneReasonError

func (err *InvalidNavigation) Error() string { return err.Message }
func (*InvalidNavigation) Tag() string       { return "InvalidNavigation" }
func (err *InvalidNavigation) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, (*laneReasonError)(err))
}

// InvalidLane reports an invalid lane name or lane creation target.
type InvalidLane laneReasonError

func (err *InvalidLane) Error() string { return err.Message }
func (*InvalidLane) Tag() string       { return "InvalidLane" }
func (err *InvalidLane) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, (*laneReasonError)(err))
}

// namedError is the shared {name, message} payload; Name is emitted as the
// JSON name property.
type namedError struct {
	Name    string `json:"-"`
	Message string `json:"-"`
}

// UnknownSkill reports a skill name absent from the harness resources.
type UnknownSkill namedError

func (err *UnknownSkill) Error() string { return err.Message }
func (*UnknownSkill) Tag() string       { return "UnknownSkill" }
func (err *UnknownSkill) MarshalJSON() ([]byte, error) {
	return namedTaggedFields(err.Tag(), err.Name, err.Message, struct{}{})
}

// UnknownTemplate reports a prompt template name absent from the resources.
type UnknownTemplate namedError

func (err *UnknownTemplate) Error() string { return err.Message }
func (*UnknownTemplate) Tag() string       { return "UnknownTemplate" }
func (err *UnknownTemplate) MarshalJSON() ([]byte, error) {
	return namedTaggedFields(err.Tag(), err.Name, err.Message, struct{}{})
}

// UnknownTarget reports a target entry id absent from the session.
type UnknownTarget struct {
	TargetID string `json:"targetId"`
	Message  string `json:"-"`
}

func (err *UnknownTarget) Error() string { return err.Message }
func (*UnknownTarget) Tag() string       { return "UnknownTarget" }
func (err *UnknownTarget) MarshalJSON() ([]byte, error) {
	type fields UnknownTarget
	return taggedFields(err.Tag(), err.Message, (*fields)(err))
}

// Closed reports a call made after the harness closed.
type Closed struct {
	Message string `json:"-"`
}

func (err *Closed) Error() string { return err.Message }
func (*Closed) Tag() string       { return "Closed" }
func (err *Closed) MarshalJSON() ([]byte, error) {
	return taggedFields(err.Tag(), err.Message, struct{}{})
}

// HarnessFault closes the harness after a failed admitted storage commit or a
// throwing trusted computation.
type HarnessFault struct {
	Message string
	Cause   error
}

func (err *HarnessFault) Error() string { return err.Message }
func (err *HarnessFault) Unwrap() error { return err.Cause }

// HarnessClosed rejects work that was active when the harness closed.
type HarnessClosed struct{}

func (*HarnessClosed) Error() string {
	return "AgentHarness was closed while the operation was active"
}
