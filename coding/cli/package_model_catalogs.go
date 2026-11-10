package cli

// Ports packages/coding-agent/src/package-manager-cli.ts:583-615.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type packageModelRuntime interface {
	Refresh(context.Context, ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult
}

var createPackageModelRuntime = func(ctx context.Context, options coding.CreateModelRuntimeOptions) (packageModelRuntime, error) {
	return coding.CreateModelRuntime(ctx, options)
}

func refreshModelCatalogs(agentDir string) error {
	// upstream: packages/coding-agent/src/package-manager-cli.ts:refreshModelCatalogs
	ctx, cancel := context.WithTimeout(context.Background(), 15000*time.Millisecond)
	defer cancel()
	modelsPath := filepath.Join(agentDir, "models.json")
	runtime, err := createPackageModelRuntime(ctx, coding.CreateModelRuntimeOptions{
		AuthPath: filepath.Join(agentDir, "auth.json"), ModelsPath: new(&modelsPath), AllowModelNetwork: false,
	})
	if err != nil {
		return err
	}
	result := runtime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(true), Force: new(true)})
	if result.Aborted {
		return fmt.Errorf("Model catalog refresh timed out.")
	}
	order := slices.Clone(result.ErrorOrder)
	for _, id := range slices.Sorted(maps.Keys(result.Errors)) {
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	var failures []string
	for _, id := range order {
		err := result.Errors[id]
		// A stale login only affects that provider's catalog; the others are still worth refreshing.
		if modelsError, ok := errors.AsType[*ai.ModelsError](err); ok && modelsError.Code == ai.ModelsErrorOAuth {
			fmt.Fprintln(os.Stderr, chalkYellow("Warning: skipped model catalog for "+id+": "+err.Error()+"\nLog in again with `"+codingagent.AppName+" auth login "+id+"` to refresh it."))
		} else {
			failures = append(failures, id+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("Could not refresh model catalogs: %s", strings.Join(failures, "; "))
	}
	fmt.Println(chalkGreen("Model catalogs refreshed"))
	return nil
}
