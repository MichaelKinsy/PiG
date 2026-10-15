package daemon

// Ports packages/env/daemon/src/watch.rs
//
// `watch` runs Durable's NodeFileWatcher next to the files, so a scan costs no round trips. Native events only trigger
// a debounced rescan; changes are the difference between snapshots plus the event paths. Events go out as `change`
// events of the request, which lasts until it is cancelled. The Rust daemon re-implements the watcher; this daemon
// runs durable/env/node's.

import (
	"context"
	"errors"
	"math"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
)

func parseWatchTargets(request Object) ([]durableenv.WatchTarget, *Failure) {
	invalid := func() *Failure { return newFailure("EINVAL", "watch needs targets") }
	items, ok := request["targets"].([]any)
	if !ok {
		return nil, invalid()
	}
	targets := make([]durableenv.WatchTarget, 0, len(items))
	for _, item := range items {
		target, ok := item.(Object)
		if !ok {
			return nil, invalid()
		}
		path, ok := target["path"].(string)
		if !ok {
			return nil, invalid()
		}
		recursive, _ := target["recursive"].(bool)
		exclude := &durableenv.WatchExclude{}
		if object, ok := target["exclude"].(Object); ok {
			exclude.Hidden, _ = object["hidden"].(bool)
			if names, ok := object["names"].([]any); ok {
				for _, name := range names {
					if text, ok := name.(string); ok {
						exclude.Names = append(exclude.Names, text)
					}
				}
			}
		}
		targets = append(targets, durableenv.WatchTarget{Path: path, Recursive: recursive, Exclude: exclude})
	}
	return targets, nil
}

// watchFailure is the error of a watch that could not establish coverage, as an error frame.
func watchFailure(err error) *Failure {
	fileError, ok := errors.AsType[*durableenv.FileError](err)
	if !ok {
		return newFailure("unknown", err.Error())
	}
	code := "UNKNOWN"
	if fileError.Cause != nil {
		code = errorCode(fileError.Cause)
	}
	if code == "UNKNOWN" {
		switch fileError.Code {
		case durableenv.FileErrorInvalid:
			code = "EINVAL"
		case durableenv.FileErrorPermissionDenied:
			code = "EACCES"
		case durableenv.FileErrorNotFound:
			code = "ENOENT"
		case durableenv.FileErrorNotDirectory:
			code = "ENOTDIR"
		case durableenv.FileErrorAborted:
			code = "aborted"
		}
	}
	failure := newFailure(code, fileError.Message)
	if fileError.Path != "" {
		failure.Path = fileError.Path
	}
	return failure
}

// watchEvent is the event frame of one change. The watcher reports overflow only when it switches to polling, so an overflow is sent with the mode, as watch.rs switch_to_polling sends it (watch.rs:583), also when the switch happens while the watch opens.
func watchEvent(change durableenv.WatchChange) Object {
	switch typed := change.(type) {
	case durableenv.WatchChangePaths:
		return Object{"kind": "change", "paths": typed.Paths}
	case durableenv.WatchChangeOverflow:
		return Object{"kind": "change", "overflow": true, "mode": "polling"}
	case durableenv.WatchChangeError:
		return Object{"kind": "error", "code": string(typed.Error.Code), "message": typed.Error.Message}
	}
	return nil
}

// runWatch watches until cancelled: a `ready` event once coverage is established, then `change` events, or an `error`
// event after which nothing follows.
func (s *server) runWatch(id uint32, request Object, ctl *control) (Object, *Failure) {
	targets, failure := parseWatchTargets(request)
	if failure != nil {
		return nil, failure
	}
	options := node.NodeWatchOptions{}
	switch mode, _ := request["mode"].(string); mode {
	case "native":
		options.Mode = durableenv.WatchNative
	case "polling":
		options.Mode = durableenv.WatchPolling
	}
	if interval, ok := unsignedNumber(request["pollIntervalMs"]); ok {
		options.PollIntervalMs = new(int(min(interval, math.MaxInt32)))
	}
	if limit, ok := unsignedNumber(request["maxDirectories"]); ok {
		options.MaxDirectories = new(int(min(limit, 1<<31)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl.attach(cancel)

	send := func(event Object) { s.out.sendControl(&Frame{Kind: FrameEvent, ID: id, JSON: event}) }
	onChange := func(change durableenv.WatchChange) {
		if event := watchEvent(change); event != nil {
			send(event)
		}
	}
	environment := node.NewNodeExecutionEnv(node.NodeExecutionEnvOptions{Watch: options})
	opened, err := environment.Watch(ctx, targets, onChange)
	if err != nil {
		return nil, watchFailure(err)
	}
	defer func() { _ = opened.Close(context.Background()) }()
	send(Object{"kind": "ready", "mode": string(opened.Mode())})
	<-ctx.Done()
	return Object{}, nil
}
