package pigporter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/parity/closure"
	porter "github.com/MichaelKinsy/PiG/parity/porter"
)

const (
	toolName    = "pig_porter"
	commandName = "pig-porter"
)

// Extension exposes the same Porter request contract as the direct CLI. It
// does not derive mappings, run closure logic, or make verdict decisions.
func Extension() *sdk.Extension {
	ext := sdk.New("pig-porter")
	ext.Tool(toolName, "Run one deterministic Pig Porter operation.", sdk.Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{
					porter.OperationStatus, porter.OperationMappingReview, porter.OperationWhyOpen,
					porter.OperationFrontier, porter.OperationExplain, porter.OperationVerify,
					porter.OperationInventory, porter.OperationPlan, porter.OperationWorkPacket,
					porter.OperationPrompt,
					porter.OperationSubmitBundle, porter.OperationRequestEvidence, porter.OperationRequestMutation,
					porter.OperationMutationReadiness, porter.OperationReport, porter.OperationPlanUnits, porter.OperationGrantLease, porter.OperationIntegrate, porter.OperationCampaignStep, porter.OperationDispositionPlan},
			},
			"root":            map[string]any{"type": "string"},
			"database":        map[string]any{"type": "string"},
			"recordId":        map[string]any{"type": "string"},
			"upstreamVersion": map[string]any{"type": "string"},
			"targetCommit":    map[string]any{"type": "string"},
			"node":            map[string]any{"type": "string"},
			"role":            map[string]any{"type": "string"},
			"snapshotId":      map[string]any{"type": "string"},
			"packetPath":      map[string]any{"type": "string"},
			"bundlePath":      map[string]any{"type": "string"},
			"outputDirectory": map[string]any{"type": "string"},
			"workUnitId":      map[string]any{"type": "string"},
			"holder":          map[string]any{"type": "string"},
			"dataset": map[string]any{
				"type": "string",
				"enum": closure.ReportDatasets(),
			},
		},
	}, runTool)
	ext.Command(commandName, "Run a JSON Porter request from the command line.", runCommand)
	return ext
}

func runTool(ctx sdk.Context, params map[string]any) (any, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode Porter tool request: %w", err)
	}
	request, err := porter.DecodeRequest(strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	if request.Root == "" {
		request.Root = ctx.Cwd()
	}
	requestCtx, stop := linkedContext(ctx.Done())
	defer stop()
	response, err := porter.Execute(requestCtx, request)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func linkedContext(done <-chan struct{}) (context.Context, func()) {
	requestCtx, cancel := context.WithCancel(context.Background())
	if done == nil {
		return requestCtx, cancel
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-done:
			cancel()
		case <-requestCtx.Done():
		}
	}()
	return requestCtx, func() {
		cancel()
		<-stopped
	}
}

func runCommand(ctx sdk.Context, args string) error {
	request, err := porter.DecodeRequest(strings.NewReader(args))
	if err != nil {
		return err
	}
	if request.Root == "" {
		request.Root = ctx.Cwd()
	}
	requestCtx, stop := linkedContext(ctx.Done())
	defer stop()
	response, err := porter.Execute(requestCtx, request)
	if err != nil {
		return err
	}
	ctx.Notify(response.Output, "info")
	return nil
}
