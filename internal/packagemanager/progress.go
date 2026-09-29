package packagemanager

// ProgressEvent describes one package operation. Completion omits Message.
type ProgressEvent struct {
	Type    string
	Action  string
	Source  string
	Message *string
}

// ProgressCallback runs synchronously before an operation starts and before its result returns.
type ProgressCallback func(ProgressEvent)

func EmitProgress(callback ProgressCallback, event ProgressEvent) {
	if callback != nil {
		callback(event)
	}
}

func FinishPackageProgress(callback ProgressCallback, action, source string, err error) error {
	event := ProgressEvent{Type: "complete", Action: action, Source: source}
	if err != nil {
		event.Type = "error"
		event.Message = new(err.Error())
	}
	EmitProgress(callback, event)
	return err
}

func WithProgress(callback ProgressCallback, action, source, message string, operation func() error) error {
	EmitProgress(callback, ProgressEvent{Type: "start", Action: action, Source: source, Message: &message})
	return FinishPackageProgress(callback, action, source, operation())
}
