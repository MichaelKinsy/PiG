// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strconv"
)

// Ports packages/coding-agent/src/core/settings-manager.ts (getCompactionTokenSetting).
// Authored invalid values survive loading and merging so only the getter for the selected model rejects them.
func readCompactionNumber(fields map[string]json.RawMessage, key string) *float64 {
	raw, ok := fields[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	delete(fields, key)
	return &value
}

func (s *CompactionSettingsJSON) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*s = CompactionSettingsJSON{extra: fields}
	s.ReserveTokens = readCompactionNumber(fields, "reserveTokens")
	s.KeepRecentTokens = readCompactionNumber(fields, "keepRecentTokens")
	if raw, ok := fields["enabled"]; ok {
		if err := json.Unmarshal(raw, &s.Enabled); err != nil {
			return err
		}
		delete(fields, "enabled")
	}
	if raw, ok := fields["modelOverrides"]; ok {
		if err := json.Unmarshal(raw, &s.ModelOverrides); err != nil {
			return err
		}
		delete(fields, "modelOverrides")
	}
	return nil
}
func compactionNumberFields(extra map[string]json.RawMessage, reserve, recent *float64) (map[string]json.RawMessage, error) {
	fields := maps.Clone(extra)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	for key, value := range map[string]*float64{"reserveTokens": reserve, "keepRecentTokens": recent} {
		if value != nil {
			raw, err := json.Marshal(*value)
			if err != nil {
				return nil, err
			}
			fields[key] = raw
		}
	}
	return fields, nil
}
func (s CompactionSettingsJSON) MarshalJSON() ([]byte, error) {
	fields, err := compactionNumberFields(s.extra, s.ReserveTokens, s.KeepRecentTokens)
	if err != nil {
		return nil, err
	}
	if s.Enabled != nil {
		raw, e := json.Marshal(*s.Enabled)
		if e != nil {
			return nil, e
		}
		fields["enabled"] = raw
	}
	if s.ModelOverrides != nil {
		raw, e := json.Marshal(s.ModelOverrides)
		if e != nil {
			return nil, e
		}
		fields["modelOverrides"] = raw
	}
	return json.Marshal(fields)
}
func (s *CompactionModelOverride) UnmarshalJSON(data []byte) error {
	*s = CompactionModelOverride{}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		s.invalidEntry = bytes.Clone(data)
		return nil
	}
	if err := json.Unmarshal(data, &s.extra); err != nil {
		return err
	}
	s.ReserveTokens = readCompactionNumber(s.extra, "reserveTokens")
	s.KeepRecentTokens = readCompactionNumber(s.extra, "keepRecentTokens")
	return nil
}
func (s CompactionModelOverride) MarshalJSON() ([]byte, error) {
	if s.invalidEntry != nil {
		return s.invalidEntry, nil
	}
	fields, err := compactionNumberFields(s.extra, s.ReserveTokens, s.KeepRecentTokens)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func compactionJSONText(raw json.RawMessage) string {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return jsStringValue(value)
}
func compactionNumberText(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
func validateCompactionToken(field string, value *float64, invalid json.RawMessage) error {
	if invalid != nil {
		return fmt.Errorf("Invalid %s setting: %s. Expected a non-negative safe integer.", field, compactionJSONText(invalid))
	}
	// upstream: packages/coding-agent/src/core/settings-manager.ts:getCompactionTokenSetting
	if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || math.Trunc(*value) != *value || *value < 0 || *value > (1<<53-1)) {
		return fmt.Errorf("Invalid %s setting: %s. Expected a non-negative safe integer.", field, compactionNumberText(*value))
	}
	return nil
}
func getCompactionTokenSetting(settings *CompactionSettingsJSON, field, provider, id string) (int, error) {
	result := defaultCompactionConfig.ReserveTokens
	if field == "keepRecentTokens" {
		result = defaultCompactionConfig.KeepRecentTokens
	}
	if settings == nil {
		return result, nil
	}
	ordinary := settings.ReserveTokens
	if field == "keepRecentTokens" {
		ordinary = settings.KeepRecentTokens
	}
	if err := validateCompactionToken("compaction."+field, ordinary, settings.extra[field]); err != nil {
		return 0, err
	}
	if ordinary != nil {
		result = int(*ordinary)
	}
	if provider == "" && id == "" {
		return result, nil
	}
	entry, ok := settings.ModelOverrides[provider+"/"+id]
	if !ok {
		return result, nil
	}
	path := fmt.Sprintf("compaction.modelOverrides[%q]", provider+"/"+id)
	if entry.invalidEntry != nil {
		return 0, fmt.Errorf("Invalid %s setting: %s. Expected an object.", path, compactionJSONText(entry.invalidEntry))
	}
	override := entry.ReserveTokens
	if field == "keepRecentTokens" {
		override = entry.KeepRecentTokens
	}
	if err := validateCompactionToken(path+"."+field, override, entry.extra[field]); err != nil {
		return 0, err
	}
	if override != nil {
		result = int(*override)
	}
	return result, nil
}

func mergeCompactionNumber(current **float64, extra map[string]json.RawMessage, key string, incoming *float64, incomingExtra map[string]json.RawMessage) {
	if incoming != nil {
		*current = cloneFloatPtr(incoming)
		delete(extra, key)
	} else if raw, ok := incomingExtra[key]; ok {
		*current = nil
		extra[key] = bytes.Clone(raw)
	}
}
