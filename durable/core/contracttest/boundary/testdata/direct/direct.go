// Package direct is a deliberate violation: a core package must not import encoding/json.
package direct

import "encoding/json"

func Marshal(v any) ([]byte, error) { return json.Marshal(v) }
