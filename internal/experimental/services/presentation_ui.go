// Ports packages/coding-agent/src/experimental/services/presentation-ui.ts.
package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// PresentationSelectItem is a local selector choice. Nil Description preserves omission.
type PresentationSelectItem struct {
	Value       string
	Label       string
	Description *string
}

// PresentationUI supplies process-local presentation capabilities. Select waits for selection or cancellation; nil means the selector was cancelled. ShowStatus completes after the owner loop applies the status.
type PresentationUI interface {
	Select(context.Context, string, []PresentationSelectItem, *string) (*string, error)
	ShowStatus(context.Context, string) error
}

// PresentationUIDefinition names the local service; Go shares type and value names.
var PresentationUIDefinition = chord.DefineService[PresentationUI]("pi.local.presentation-ui", chord.ServiceOptions{Local: true})

func init() {
	chord.RegisterServiceView(PresentationUIDefinition, func(resolve func() (PresentationUI, error)) PresentationUI {
		return presentationUIView{resolve: resolve}
	})
}

type presentationUIView struct {
	resolve func() (PresentationUI, error)
}

func (view presentationUIView) Select(ctx context.Context, title string, items []PresentationSelectItem, selectedValue *string) (*string, error) {
	service, err := view.resolve()
	if err != nil {
		return nil, err
	}
	return service.Select(ctx, title, items, selectedValue)
}

func (view presentationUIView) ShowStatus(ctx context.Context, message string) error {
	service, err := view.resolve()
	if err != nil {
		return err
	}
	return service.ShowStatus(ctx, message)
}
