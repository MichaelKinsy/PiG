package main

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestRPCModelProjectionPreservesIndependentInputLimits(t *testing.T) {
	model := &ai.Model{ID: "configured", InputLimits: &ai.ModelInputLimits{MaxRequestBytes: 12345, Images: &ai.ModelImageInputLimits{MaxPerMessage: 3, MaxPerRequest: 5, Resize: &ai.ModelImageResizeOptions{MaxWidth: 640, MaxHeight: 480, MaxBytes: 9000, JPEGQuality: 71}}}}
	state := rpcModelValue(model)
	available := rpcModelList([]*ai.Model{model})
	model.InputLimits.Images.Resize.MaxWidth = 1
	model.InputLimits.Images.MaxPerRequest = 1
	for _, tc := range []struct {
		name  string
		model *RPCModel
	}{{"state", state}, {"available", available[0]}} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.model)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				InputLimits *ai.ModelInputLimits `json:"inputLimits"`
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.InputLimits == nil || wire.InputLimits.Images == nil || wire.InputLimits.Images.Resize == nil {
				t.Fatalf("RPC omitted configured limits: %s", data)
			}
			limits := wire.InputLimits
			if limits.MaxRequestBytes != 12345 || limits.Images.MaxPerMessage != 3 || limits.Images.MaxPerRequest != 5 || *limits.Images.Resize != (ai.ModelImageResizeOptions{MaxWidth: 640, MaxHeight: 480, MaxBytes: 9000, JPEGQuality: 71}) {
				t.Fatalf("RPC limit projection aliases/drops model fields: %s", data)
			}
		})
	}
}
