package codingagent

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/ai"
)

// normalizeModelInputLimitsJSON validates the upstream schema while accepting
// every JSON spelling of an integer (including 1.0 and 1e3). Null and zero
// remain distinct from an omitted optional property.
func normalizeModelInputLimitsJSON(data []byte) ([]byte, error) {
	var model map[string]json.RawMessage
	if err := json.Unmarshal(data, &model); err != nil {
		return nil, err
	}
	limits, exists := model["inputLimits"]
	if !exists {
		return data, nil
	}
	normalized, err := normalizeInputLimitsObject(limits, "inputLimits")
	if err != nil {
		return nil, err
	}
	model["inputLimits"] = normalized
	return json.Marshal(model)
}

func normalizeInputLimitsObject(data []byte, path string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", path, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	var numbers []string
	var nested string
	switch path {
	case "inputLimits":
		numbers = []string{"maxRequestBytes"}
		nested = "images"
	case "inputLimits.images":
		numbers = []string{"maxPerMessage", "maxPerRequest"}
		nested = "resize"
	case "inputLimits.images.resize":
		numbers = []string{"maxWidth", "maxHeight", "maxBytes", "jpegQuality"}
	}
	for _, name := range numbers {
		raw, exists := fields[name]
		if !exists {
			continue
		}
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil || value < 1 || math.Trunc(value) != value || value >= float64(int(^uint(0)>>1)) {
			return nil, fmt.Errorf("%s.%s must be a positive integer within the supported range", path, name)
		}
		if name == "jpegQuality" && value > 100 {
			return nil, fmt.Errorf("%s.%s must be at most 100", path, name)
		}
		normalized, err := json.Marshal(int(value))
		if err != nil {
			return nil, err
		}
		fields[name] = normalized
	}
	if raw, exists := fields[nested]; nested != "" && exists {
		normalized, err := normalizeInputLimitsObject(raw, path+"."+nested)
		if err != nil {
			return nil, err
		}
		fields[nested] = normalized
	}
	return json.Marshal(fields)
}

// mergeModelInputLimits mirrors provider-composer.ts mergeInputLimits. Validated
// positive numeric values allow zero to represent an omitted property.
func mergeModelInputLimits(base, override *ai.ModelInputLimits) *ai.ModelInputLimits {
	out := base.Clone()
	if override == nil {
		return out
	}
	if out == nil {
		out = &ai.ModelInputLimits{}
	}
	if override.MaxRequestBytes != 0 {
		out.MaxRequestBytes = override.MaxRequestBytes
	}
	if override.Images == nil {
		return out
	}
	if out.Images == nil {
		out.Images = &ai.ModelImageInputLimits{}
	}
	if override.Images.MaxPerMessage != 0 {
		out.Images.MaxPerMessage = override.Images.MaxPerMessage
	}
	if override.Images.MaxPerRequest != 0 {
		out.Images.MaxPerRequest = override.Images.MaxPerRequest
	}
	if override.Images.Resize == nil {
		return out
	}
	if out.Images.Resize == nil {
		out.Images.Resize = &ai.ModelImageResizeOptions{}
	}
	resize := override.Images.Resize
	if resize.MaxWidth != 0 {
		out.Images.Resize.MaxWidth = resize.MaxWidth
	}
	if resize.MaxHeight != 0 {
		out.Images.Resize.MaxHeight = resize.MaxHeight
	}
	if resize.MaxBytes != 0 {
		out.Images.Resize.MaxBytes = resize.MaxBytes
	}
	if resize.JPEGQuality != 0 {
		out.Images.Resize.JPEGQuality = resize.JPEGQuality
	}
	return out
}
