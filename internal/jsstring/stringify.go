package jsstring

import (
	"bytes"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// MarshalJSON encodes value as JSON.stringify(value) does (numbers too: a non-finite number is null and negative zero is 0), retaining unmatched UTF-16 units as surrogate escapes and writing <, >, & and the line and paragraph separators literally even inside custom marshalers. Escaped backslashes remain escaped.
func MarshalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetJSNumbers(true)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	input := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	output := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		if input[i] == '\\' && i+1 < len(input) {
			if i+6 <= len(input) {
				var replacement string
				switch string(input[i : i+6]) {
				case `\u003c`, `\u003C`:
					replacement = "<"
				case `\u003e`, `\u003E`:
					replacement = ">"
				case `\u0026`:
					replacement = "&"
				case `\u2028`:
					replacement = "\u2028"
				case `\u2029`:
					replacement = "\u2029"
				}
				if replacement != "" {
					output = append(output, replacement...)
					i += 5
					continue
				}
			}
			output = append(output, input[i], input[i+1])
			i++
			continue
		}
		output = append(output, input[i])
	}
	return output, nil
}
