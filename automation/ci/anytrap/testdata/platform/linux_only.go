//go:build linux

package platform

import "encoding/json"

// LinuxOnly is a trap that only a linux build context can see.
func LinuxOnly(data []byte) (map[string]any, error) {
	var out map[string]any
	err := json.Unmarshal(data, &out)
	return out, err
}
