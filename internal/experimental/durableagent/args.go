package durableagent

// Ports packages/coding-agent/src/experimental/durable/main.ts (parseArgs) and packages/coding-agent/src/experimental/vacation/main.ts

import "errors"

// ParseArgs reads the command line of the durable and vacation programs: --continue (-c) opens the newest session of the directory; anything else is an error.
func ParseArgs(argv []string) (OpenDurableOptions, error) {
	options := OpenDurableOptions{}
	for _, arg := range argv {
		if arg != "--continue" && arg != "-c" {
			return OpenDurableOptions{}, errors.New("Unknown argument: " + arg)
		}
		options.ContinueSession = true
	}
	return options, nil
}
