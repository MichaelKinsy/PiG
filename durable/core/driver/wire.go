// SPDX-License-Identifier: MIT

package driver

import (
	"encoding/json"
	"errors"
)

// wireError is the JSON an effect completion carries for a thrown error: {name, message}.
func wireError(err error) json.RawMessage {
	name := "Error"
	if named, ok := errors.AsType[interface {
		error
		ErrorName() string
	}](err); ok {
		name = named.ErrorName()
	}
	body, _ := json.Marshal(struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}{name, err.Error()})
	return body
}
