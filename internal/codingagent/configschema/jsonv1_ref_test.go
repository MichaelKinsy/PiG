package configschema

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
)

func parseJSONNodeV1(data []byte) (jsonNode, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return parseJSONValueV1(decoder)
}

func parseJSONValueV1(decoder *json.Decoder) (jsonNode, error) {
	token, err := decoder.Token()
	if err != nil {
		return jsonNode{}, err
	}
	switch value := token.(type) {
	case json.Delim:
		if value == '[' {
			node := jsonNode{kind: jsonArray}
			for decoder.More() {
				item, err := parseJSONValueV1(decoder)
				if err != nil {
					return jsonNode{}, err
				}
				node.items = append(node.items, item)
			}
			_, err := decoder.Token()
			return node, err
		}
		node := jsonNode{kind: jsonObject, fields: map[string]jsonNode{}}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return jsonNode{}, err
			}
			key := keyToken.(string)
			item, err := parseJSONValueV1(decoder)
			if err != nil {
				return jsonNode{}, err
			}
			if _, seen := node.fields[key]; !seen {
				node.keys = append(node.keys, key)
			}
			node.fields[key] = item
		}
		if _, err := decoder.Token(); err != nil {
			return jsonNode{}, err
		}
		node.keys = jsKeyOrder(node.keys)
		return node, nil
	case string:
		return jsonNode{kind: jsonString, text: value}, nil
	case json.Number:
		number, err := strconv.ParseFloat(string(value), 64)
		if err != nil && !math.IsInf(number, 0) {
			return jsonNode{}, err
		}
		return jsonNode{kind: jsonNumber, number: number}, nil
	case bool:
		return jsonNode{kind: jsonBool, number: map[bool]float64{true: 1}[value]}, nil
	}
	return jsonNode{kind: jsonNull}, nil
}
