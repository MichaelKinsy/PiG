package hosts

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
)

// Ports the command parsing of apps/p2/src/runtime/dev-protocol.ts (`parseDevCommand`) and apps/p2/src/hosts/dev-channel.ts
// (`readDevCommand`) behind `/__p2_dev`. Dev mode itself (the watcher, the supervisor, hot reload) is designed out (D473:
// compiled-in plugins), so every command answers as a server started without `P2_DEV=1` does: "Dev mode is not enabled".

// DevCommand is `{action: "reload", plugins?}` or `{action: "restart"}`.
type DevCommand struct {
	Action  string
	Plugins []string
}

var devPluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)

// ParseDevCommand validates a decoded command (`parseDevCommand`); nil plugins for a reload of everything.
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
	plugins, present := object["plugins"]
	if !present || plugins == nil {
		return DevCommand{Action: "reload"}, nil
	}
	list, ok := plugins.([]any)
	if !ok || len(list) > 100 {
		return DevCommand{}, errors.New("Invalid plugin names")
	}
	names := make([]string, 0, len(list))
	for _, entry := range list {
		name, ok := entry.(string)
		if !ok || !devPluginName.MatchString(name) {
			return DevCommand{}, errors.New("Invalid plugin names")
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return DevCommand{Action: "reload", Plugins: names}, nil
}

// maxDevCommand is the largest command body (`readDevCommand`): 16 KiB.
const maxDevCommand = 16 * 1024

// ReadDevCommand reads and validates a request's command (`readDevCommand`).
func ReadDevCommand(request *http.Request) (DevCommand, error) {
	body, err := io.ReadAll(io.LimitReader(request.Body, maxDevCommand+1))
	if err != nil {
		return DevCommand{}, err
	}
	if len(body) > maxDevCommand {
		return DevCommand{}, errors.New("Dev command too large")
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return DevCommand{}, errors.New("Invalid dev command JSON")
	}
	return ParseDevCommand(value)
}

// DevResult is the JSON answer of a dev command (`DevResult`): field order as Pi writes it.
type DevResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
