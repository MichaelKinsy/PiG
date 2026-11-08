package extension

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func decodeProviderHeaderEntriesV1(data []byte) ([]providerHeaderEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, err
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if key != "headers" {
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return nil, err
			}
			continue
		}
		return decodeProviderHeaderObjectV1(decoder)
	}
	return nil, nil
}

func decodeProviderHeaderObjectV1(decoder *json.Decoder) ([]providerHeaderEntry, error) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, err
	}
	var entries []providerHeaderEntry
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		entries = append(entries, providerHeaderEntry{Name: name.(string), Value: value})
	}
	_, err = decoder.Token()
	return entries, err
}

func rejectDuplicateLoginJSONFieldsV1(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // a number is a raw token, as the scan reads it: JSON.parse gives Infinity for 1e999 where a float64 decode fails
	if err := scanLoginJSONValueV1(decoder, ""); err != nil {
		return err
	}
	return requireLoginJSONEOFV1(decoder)
}

func scanLoginJSONValueV1(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode login definition: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode login definition: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("decode login definition: object key is not a string")
			}
			field := loginJSONFieldPath(path, key)
			if _, duplicate := seen[key]; duplicate {
				return loginDefinitionError(field, "field occurs more than once")
			}
			seen[key] = struct{}{}
			if err := scanLoginJSONValueV1(decoder, field); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("decode login definition: %w", err)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := scanLoginJSONValueV1(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("decode login definition: %w", err)
		}
	default:
		return fmt.Errorf("decode login definition: unexpected delimiter %q", delim)
	}
	return nil
}

func requireLoginJSONEOFV1(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode login definition: trailing JSON value")
		}
		return fmt.Errorf("decode login definition: %w", err)
	}
	return nil
}
