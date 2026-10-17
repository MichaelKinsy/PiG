package hotpathjson

import "encoding/json"

type event struct{ Type string }

// A hot path marshals and decodes by reflection and builds maps per call.
//
//pig:hotpath
func hot(e event, raw []byte) ([]byte, error) {
	out, err := json.Marshal(e) // want `json.Marshal on a hot path walks the value by reflection`
	if err != nil {
		return nil, err
	}
	var back event
	_ = json.Unmarshal(raw, &back)           // want `json.Unmarshal on a hot path`
	fields := map[string]any{"type": e.Type} // want `a map\[string\]any literal on a hot path`
	_ = fields
	scratch := make(map[string]any) // want `make\(map\[string\]any\) on a hot path`
	_ = scratch
	return out, nil
}

// A closure built on the hot path runs on it.
//
//pig:hotpath
func hotClosure(e event) func() ([]byte, error) {
	return func() ([]byte, error) { return json.Marshal(e) } // want `json.Marshal on a hot path`
}

// The same calls off the hot path are the cold paths' business.
func cold(e event) ([]byte, error) {
	_ = map[string]any{"type": e.Type}
	return json.Marshal(e)
}

// A typed map or a hand-appended encoding is the fix.
//
//pig:hotpath
func hotGood(e event, dst []byte) []byte {
	typed := map[string]string{"type": e.Type}
	_ = typed
	dst = append(dst, `{"type":"`...)
	dst = append(dst, e.Type...)
	return append(dst, `"}`...)
}
