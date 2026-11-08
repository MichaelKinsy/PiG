package runtime

import (
	"errors"
	"regexp"
	"slices"
)

// Ports apps/p2/src/runtime/dev-protocol.ts: the shared development control protocol. No credentials or transport
// addresses belong in browser state. D473: a Go plugin cannot be hot swapped, so a `reload` asks the dev supervisor
// for a restart; the protocol keeps its commands, results and status as upstream.

// DevCommand is `{ action: "reload"; plugins? } | { action: "restart" }`: Action "reload" or "restart"; Plugins is nil
// for no `plugins` field.
type DevCommand struct {
	Action  string
	Plugins []string
}

// DevResult is the outcome of a dev command.
type DevResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	// Plugins is a pointer so that an explicit empty list is sent, as Pi sends it; nil omits the member.
	Plugins *[]string `json:"plugins,omitempty"`
}

// DevStatus is the dev supervisor's status (`DevStatus`).
type DevStatus struct {
	Revision int `json:"revision"`
	// Phase is "idle", "reloading", "ready" or "error".
	Phase         string   `json:"phase"`
	Plugins       []string `json:"plugins"`
	Dirty         []string `json:"dirty"`
	RestartNeeded bool     `json:"restartNeeded"`
	// RestartFiles are loaded server/worker files whose content differs from their running copy (project-relative).
	RestartFiles []string `json:"restartFiles"`
	Message      *string  `json:"message,omitempty"`
	NoticeID     *string  `json:"noticeId,omitempty"`
	// NoticeTime is the event time of the last result; unrelated dirty/revision changes do not refresh it.
	NoticeTime *float64 `json:"noticeTime,omitempty"`
	HasLogs    *bool    `json:"hasLogs,omitempty"`
}

// DevModule is a loaded module's file and content hash.
type DevModule struct {
	File string
	Hash string
}

var (
	sha256Hex  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	pluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)
)

// ParseDevModules reads the private worker-to-server metadata, separate from chord2 control/frames: a decoded JSON
// value `{ type: "dev-modules", modules: [{ file, hash }] }`, or false when it is not one.
func ParseDevModules(value any) ([]DevModule, bool) {
	object, ok := value.(map[string]any)
	if !ok || object["type"] != "dev-modules" {
		return nil, false
	}
	list, ok := object["modules"].([]any)
	if !ok {
		return nil, false
	}
	modules := make([]DevModule, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		file, fileOK := entry["file"].(string)
		hash, hashOK := entry["hash"].(string)
		if !fileOK || !hashOK || !sha256Hex.MatchString(hash) {
			return nil, false
		}
		modules = append(modules, DevModule{File: file, Hash: hash})
	}
	return modules, true
}

// ParseDevPluginFiles reads the plugin files a worker loaded through the facet loader (absolute paths), from the same
// message.
func ParseDevPluginFiles(value any) ([]string, bool) {
	object, ok := value.(map[string]any)
	if !ok || object["type"] != "dev-modules" {
		return nil, false
	}
	list, ok := object["pluginFiles"].([]any)
	if !ok {
		return nil, false
	}
	files := make([]string, 0, len(list))
	for _, item := range list {
		file, ok := item.(string)
		if !ok {
			return nil, false
		}
		files = append(files, file)
	}
	return files, true
}

// ParseDevCommand reads a dev command from a decoded JSON value.
func ParseDevCommand(value any) (DevCommand, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return DevCommand{}, errors.New("Invalid dev command")
	}
	action, present := object["action"]
	if !present {
		return DevCommand{}, errors.New("Invalid dev command")
	}
	if action == "restart" {
		return DevCommand{Action: "restart"}, nil
	}
	if action != "reload" {
		return DevCommand{}, errors.New("Invalid dev action")
	}
	raw, present := object["plugins"]
	if !present || raw == nil {
		return DevCommand{Action: "reload"}, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) > 100 {
		return DevCommand{}, errors.New("Invalid plugin names")
	}
	var names []string
	for _, item := range list {
		name, ok := item.(string)
		if !ok || !pluginName.MatchString(name) {
			return DevCommand{}, errors.New("Invalid plugin names")
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if names == nil {
		names = []string{}
	}
	return DevCommand{Action: "reload", Plugins: names}, nil
}
