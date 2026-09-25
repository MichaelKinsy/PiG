package pigletbuild

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// BuilderReadiness is the side-effect-free probe result used by auto selection
// and non-interactive remediation.
type BuilderReadiness struct {
	Builder string `json:"builder"`
	Ready   bool   `json:"ready"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

// BuilderUnavailableError carries one exact readiness result to JSON callers.
type BuilderUnavailableError struct {
	Readiness BuilderReadiness
}

func (e *BuilderUnavailableError) Error() string {
	return unavailableBuilderMessage(e.Readiness)
}

// NoBuilderReadyError carries every side-effect-free probe result for auto.
type NoBuilderReadyError struct {
	Readiness []BuilderReadiness
}

func (e *NoBuilderReadyError) Error() string {
	messages := make([]string, len(e.Readiness))
	for i, readiness := range e.Readiness {
		messages[i] = unavailableBuilderMessage(readiness)
	}
	return "no build backend is ready: " + strings.Join(messages, "; ")
}

// BuilderRequest is one already-validated, locked Piglet build request.
type BuilderRequest struct {
	Piglet  *piglet.Piglet
	Cells   []subprocess.CellSpec
	Options Options
	Output  string
	Stdout  io.Writer
	Stderr  io.Writer
}

// BuilderResult identifies the output produced by exactly one backend.
type BuilderResult struct {
	Builder  string
	Artifact string
	Record   string
}

// BuilderBackend probes and executes one build provenance. Probe must have no
// authentication, prompt, download, or mutation side effects.
// pig additive (D18): Piglet Binary builds select generic named builders.
type BuilderBackend interface {
	Name() string
	Probe(context.Context, BuilderRequest) BuilderReadiness
	Build(context.Context, BuilderRequest) (BuilderResult, error)
}

type nativeBuilder struct{}

func (nativeBuilder) Name() string { return "native" }

func (nativeBuilder) Probe(_ context.Context, request BuilderRequest) BuilderReadiness {
	result := BuilderReadiness{Builder: "native"}
	if err := validateNativeTargets(request.Options.Targets); err != nil {
		result.Code = "target-unavailable"
		result.Message = err.Error()
		result.Remedy = "configure a container or remote builder for non-host targets"
		return result
	}
	if _, err := pigSourceRoot(); err != nil {
		result.Code = "source-unavailable"
		result.Message = err.Error()
		result.Remedy = "run from a Pig checkout or set PIG_SOURCE_ROOT"
		return result
	}
	if _, err := toolchain.Go(); err != nil {
		result.Code = "go-unavailable"
		result.Message = err.Error()
		result.Remedy = "run `pig setup go` to install a verified Go toolchain, or install Go from https://go.dev/dl/"
		return result
	}
	result.Ready = true
	return result
}

func (nativeBuilder) Build(ctx context.Context, request BuilderRequest) (BuilderResult, error) {
	artifact, record, err := buildNativeWithRecords(ctx, request.Piglet, request.Cells, request.Options, request.Output, request.Stdout, request.Stderr)
	if err != nil {
		return BuilderResult{}, err
	}
	return BuilderResult{Builder: "native", Artifact: artifact, Record: record}, nil
}

func executeBuild(ctx context.Context, builders []BuilderBackend, selector string, request BuilderRequest) (BuilderResult, error) {
	builder, readiness, err := selectBuilder(ctx, builders, selector, request)
	if err != nil {
		return BuilderResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return BuilderResult{}, err
	}
	result, err := builder.Build(ctx, request)
	if err != nil {
		return BuilderResult{}, fmt.Errorf("builder %s failed after execution started; no fallback was attempted: %w", builder.Name(), err)
	}
	if result.Builder == "" {
		result.Builder = readiness.Builder
	}
	return result, nil
}

func selectBuilder(ctx context.Context, builders []BuilderBackend, selector string, request BuilderRequest) (BuilderBackend, BuilderReadiness, error) {
	if selector == "" {
		selector = "auto"
	}
	if selector != "auto" {
		for _, builder := range builders {
			if builder.Name() != selector {
				continue
			}
			readiness := builder.Probe(ctx, request)
			if !readiness.Ready {
				return nil, readiness, &BuilderUnavailableError{Readiness: readiness}
			}
			return builder, readiness, nil
		}
		return nil, BuilderReadiness{}, fmt.Errorf("builder %q is not registered; available: %s", selector, builderNames(builders))
	}
	unavailable := make([]BuilderReadiness, 0, len(builders))
	for _, builder := range builders {
		readiness := builder.Probe(ctx, request)
		if readiness.Ready {
			return builder, readiness, nil
		}
		unavailable = append(unavailable, readiness)
	}
	return nil, BuilderReadiness{}, &NoBuilderReadyError{Readiness: unavailable}
}

func unavailableBuilderMessage(readiness BuilderReadiness) string {
	message := readiness.Message
	if message == "" {
		message = "unavailable"
	}
	if readiness.Remedy != "" {
		message += "; remedy: " + readiness.Remedy
	}
	return fmt.Sprintf("builder %s unavailable (%s): %s", readiness.Builder, readiness.Code, message)
}

func builderNames(builders []BuilderBackend) string {
	names := make([]string, 0, len(builders))
	for _, builder := range builders {
		names = append(names, builder.Name())
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
