package experimental

// Ports packages/coding-agent/src/experimental/server.ts (internal process entry).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/ecmascript"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

type serverModelOptionsError struct{ cause error }

func (err *serverModelOptionsError) Error() string {
	return "Internal server received invalid model options"
}
func (err *serverModelOptionsError) Unwrap() error { return err.cause }

func parseServerModelOptions(value *string) (*SessionWorkerModel, error) {
	if value == nil {
		return nil, nil
	}
	invalid := errors.New("Internal server received invalid model options")
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*value), &fields); err != nil {
		if _, wrongShape := errors.AsType[*json.UnmarshalTypeError](err); wrongShape {
			return nil, invalid
		}
		return nil, &serverModelOptionsError{cause: err}
	}
	for key := range fields {
		if key != "model" && key != "provider" {
			return nil, invalid
		}
	}
	var model ecmascript.String
	if json.Unmarshal(fields["model"], &model) != nil || model == "" {
		return nil, invalid
	}
	result := &SessionWorkerModel{Model: string(model)}
	if raw, present := fields["provider"]; present {
		var provider ecmascript.String
		if json.Unmarshal(raw, &provider) != nil || provider == "" {
			return nil, invalid
		}
		result.Provider = new(string(provider))
	}
	return result, nil
}

// RunServerProcess runs an automatically activated server until its lifetime ends, a termination signal arrives, or the caller cancels. The executable owns internal-role validation; this function owns startup and joined shutdown.
func RunServerProcess(ctx context.Context, args []string) (err error) {
	if len(args) > 4 {
		return errors.New("Internal server received unexpected arguments")
	}
	if len(args) < 1 || args[0] == "" || !filepath.IsAbs(args[0]) {
		return errors.New("Internal server requires an absolute server directory")
	}
	if len(args) < 2 || !protocol.IsServerId(args[1]) {
		return errors.New("Internal server requires a canonical server ID")
	}
	if len(args) < 3 || args[2] == "" || !filepath.IsAbs(args[2]) {
		return errors.New("Internal server requires an absolute Session directory")
	}
	var serializedModel *string
	if len(args) == 4 {
		serializedModel = &args[3]
	}
	model, err := parseServerModelOptions(serializedModel)
	if err != nil {
		return err
	}
	directory, serverID, sessionDir := args[0], args[1], args[2]
	options := StartServerOptions{Directory: &directory, ServerId: &serverID, SessionDir: &sessionDir, KeepAlive: new(false)}
	if model != nil {
		options.Provider, options.Model = model.Provider, &model.Model
	}
	runtime, err := StartServer(ctx, options)
	if err != nil {
		return err
	}
	interrupts := make(chan os.Signal, 1)
	terminations := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	signal.Notify(terminations, syscall.SIGTERM)
	var closeDone chan struct{}
	defer func() {
		signal.Stop(interrupts)
		signal.Stop(terminations)
		if failure := runtime.Close(); failure != nil {
			err = failure
		}
		if closeDone != nil {
			<-closeDone
		}
	}()
	beginClose := func() {
		if closeDone != nil {
			return
		}
		closeDone = make(chan struct{})
		go func() {
			defer close(closeDone)
			// The final Close above observes the same cached error and joins this signal-owned task.
			_ = runtime.Close()
		}()
	}
	interrupt, termination, cancelled := (<-chan os.Signal)(interrupts), (<-chan os.Signal)(terminations), ctx.Done()
	for {
		select {
		case <-runtime.Closed():
			return runtime.ClosedError()
		case <-interrupt:
			// Each upstream process.once listener is removed on its own first signal.
			signal.Stop(interrupts)
			interrupt = nil
			beginClose()
		case <-termination:
			signal.Stop(terminations)
			termination = nil
			beginClose()
		case <-cancelled:
			cancelled = nil
			beginClose()
		}
	}
}
