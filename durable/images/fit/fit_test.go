// Ports packages/durable/test/images-fit.test.ts: `fitImage`, `fitImages` and the `fit-images` extension
// (src/images/fit.ts), images fitted to the model's limits as they enter a conversation, before they are stored.
//
// Go mapping: Pi's processor is Photon in a worker thread, here CreateNodePhotonImages; its encoders are not
// reproduced, so the assertions are on what the model gets (type, size, notes), as in the processor's own ports. The
// harness cases build their setup from the exported API, as harness_coding_tools_test.go does (test/chat-support.ts).

package fit_test

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/images/fit"
	"github.com/MichaelKinsy/PiG/durable/images/node"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

var background = context.Background()

func photonImages(t testing.TB) tools.ImageProcessor {
	t.Helper()
	processor, err := node.CreateNodePhotonImages(nil)
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

// recordedProcessor is processor with the type of every Prepare call recorded.
type recordedProcessor struct {
	processor tools.ImageProcessor
	mu        sync.Mutex
	calls     []string
}

func (recorded *recordedProcessor) Prepare(ctx context.Context, bytes []byte, mimeType string, limits tools.ImageLimits) (*tools.PreparedImage, error) {
	recorded.mu.Lock()
	recorded.calls = append(recorded.calls, mimeType)
	recorded.mu.Unlock()
	return recorded.processor.Prepare(ctx, bytes, mimeType, limits)
}

type failingProcessor struct{}

func (failingProcessor) Prepare(context.Context, []byte, string, tools.ImageLimits) (*tools.PreparedImage, error) {
	return nil, errString("worker exited")
}

type errString string

func (err errString) Error() string { return string(err) }

func imageOf(bytes []byte, mimeType string) ai.ImageContent {
	return ai.ImageContent{Data: tools.ToBase64(bytes), MimeType: mimeType}
}

// fitOne is the single image FitImages made of item, with its notes; nil when it was left out.
func fitOne(t *testing.T, photon tools.ImageProcessor, item ai.ImageContent, limits tools.ImageLimits) (*ai.ImageContent, []string) {
	t.Helper()
	fitted := fit.FitImages(background, []ai.ToolResultMessageContent{item}, limits, photon)
	if fitted.Kind != fit.FittedImagesFitted {
		t.Fatal("the image was not changed")
	}
	if len(fitted.Content) == 0 {
		return nil, fitted.Notes
	}
	image := fitted.Content[0].(ai.ImageContent)
	return &image, fitted.Notes
}

func TestFitImage(t *testing.T) {
	photon := photonImages(t)

	// images-fit.test.ts "prepares an image from its bytes, as base64 for the model"
	t.Run("prepares an image from its bytes, as base64 for the model", func(t *testing.T) {
		png := encode(t, 8, 6, "png")
		got := fit.FitImage(background, png, "image/png", tools.DEFAULT_IMAGE_LIMITS, photon)
		want := fit.FittedImage{Kind: fit.FittedImageFitted, Image: imageOf(png, "image/png"), Notes: []string{}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	// images-fit.test.ts "leaves out an image the processor fails on, with the failure in its note"
	t.Run("leaves out an image the processor fails on, with the failure in its note", func(t *testing.T) {
		got := fit.FitImage(background, encode(t, 8, 6, "png"), "image/png", tools.DEFAULT_IMAGE_LIMITS, failingProcessor{})
		want := fit.FittedImage{Kind: fit.FittedImageOmitted, Note: "An image (image/png) cannot be prepared for the model and was left out: worker exited."}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
}

func TestFitImages(t *testing.T) {
	photon := photonImages(t)
	limits := tools.DEFAULT_IMAGE_LIMITS

	// images-fit.test.ts "is unchanged when every image comes back as it was, and asks the processor only about images"
	t.Run("is unchanged when every image comes back as it was, and asks the processor only about images", func(t *testing.T) {
		recorded := &recordedProcessor{processor: photon}
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, imageOf(encode(t, 8, 6, "png"), "image/png")}
		if got := fit.FitImages(background, content, limits, recorded); got.Kind != fit.FittedImagesUnchanged {
			t.Fatalf("got %+v, want unchanged", got)
		}
		if !slices.Equal(recorded.calls, []string{"image/png"}) {
			t.Fatalf("calls %v, want [image/png]", recorded.calls)
		}
		if got := fit.FitImages(background, []ai.ToolResultMessageContent{ai.TextContent{Text: "only text"}}, limits, recorded); got.Kind != fit.FittedImagesUnchanged {
			t.Fatalf("got %+v, want unchanged", got)
		}
		if !slices.Equal(recorded.calls, []string{"image/png"}) {
			t.Fatalf("calls %v after text only, want [image/png]", recorded.calls)
		}
	})

	// images-fit.test.ts "scales an image beyond the limits down, keeps the text in place, and says how to map coordinates back"
	t.Run("scales an image beyond the limits down, keeps the text in place, and says how to map coordinates back", func(t *testing.T) {
		text := ai.TextContent{Text: "before"}
		wide := imageOf(encode(t, 400, 100, "png"), "image/png")
		narrow := limits
		narrow.MaxWidth = 200
		fitted := fit.FitImages(background, []ai.ToolResultMessageContent{text, wide, ai.TextContent{Text: "after"}}, narrow, photon)
		if fitted.Kind != fit.FittedImagesFitted {
			t.Fatalf("kind %q, want fitted", fitted.Kind)
		}
		kinds := []string{}
		for _, item := range fitted.Content {
			switch item.(type) {
			case ai.TextContent:
				kinds = append(kinds, "text")
			case ai.ImageContent:
				kinds = append(kinds, "image")
			}
		}
		if !slices.Equal(kinds, []string{"text", "image", "text"}) {
			t.Fatalf("content types %v, want [text image text]", kinds)
		}
		if fitted.Content[0] != ai.ToolResultMessageContent(text) {
			t.Fatalf("the text before was changed: %+v", fitted.Content[0])
		}
		if width, height := decodedSize(t, fitted.Content[1].(ai.ImageContent).Data); width != 200 || height != 50 {
			t.Fatalf("size %dx%d, want 200x50", width, height)
		}
		wantNotes := []string{"Resized from 400x100 to 200x50. Multiply coordinates by 2.00 to map them to the original."}
		if !reflect.DeepEqual(fitted.Notes, wantNotes) {
			t.Fatalf("notes %v, want %v", fitted.Notes, wantNotes)
		}
	})

	// images-fit.test.ts "re-encodes an image within the dimensions but over the byte limit"
	t.Run("re-encodes an image within the dimensions but over the byte limit", func(t *testing.T) {
		tight := limits
		tight.MaxBytes = 8_000
		fitted, notes := fitOne(t, photon, imageOf(noisyPng(t, 64, 64), "image/png"), tight)
		if len(fitted.Data) > tight.MaxBytes {
			t.Fatalf("%d bytes of base64, want at most %d", len(fitted.Data), tight.MaxBytes)
		}
		if len(notes) == 0 {
			t.Fatal("no notes")
		}
	})

	// images-fit.test.ts "turns an image upright that fits but carries an EXIF orientation"
	t.Run("turns an image upright that fits but carries an EXIF orientation", func(t *testing.T) {
		fitted, _ := fitOne(t, photon, imageOf(withOrientation(encode(t, 40, 30, "jpeg"), 6), "image/jpeg"), limits)
		if width, height := decodedSize(t, fitted.Data); width != 30 || height != 40 {
			t.Fatalf("size %dx%d, want 30x40", width, height)
		}
	})

	// images-fit.test.ts "converts a format providers do not take inline to PNG"
	t.Run("converts a format providers do not take inline to PNG", func(t *testing.T) {
		fitted, notes := fitOne(t, photon, imageOf(bmp(4, 4), "image/bmp"), limits)
		if fitted == nil || fitted.MimeType != "image/png" {
			t.Fatalf("image %+v, want image/png", fitted)
		}
		if !reflect.DeepEqual(notes, []string{"Converted from image/bmp to image/png."}) {
			t.Fatalf("notes %v", notes)
		}
	})

	// images-fit.test.ts "corrects a declared type the bytes contradict, without a note"
	t.Run("corrects a declared type the bytes contradict, without a note", func(t *testing.T) {
		jpegBytes := encode(t, 8, 6, "jpeg")
		fitted, notes := fitOne(t, photon, imageOf(jpegBytes, "image/png"), limits)
		if want := imageOf(jpegBytes, "image/jpeg"); *fitted != want || !reflect.DeepEqual(notes, []string{}) {
			t.Fatalf("got %+v %v, want %+v and no notes", fitted, notes, want)
		}
	})

	// images-fit.test.ts "leaves out what cannot be prepared, with a note: damaged images, other bytes, malformed base64"
	t.Run("leaves out what cannot be prepared, with a note: damaged images, other bytes, malformed base64", func(t *testing.T) {
		note := "An image (image/png) cannot be prepared for the model and was left out."
		damaged := encode(t, 4, 4, "png")[:40]
		got := fit.FitImages(background, []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, imageOf(damaged, "image/png")}, limits, photon)
		want := fit.FittedImages{Kind: fit.FittedImagesFitted, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}}, Notes: []string{note}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("damaged: got %+v, want %+v", got, want)
		}
		other := []byte("not an image at all, though it says so")
		image, notes := fitOne(t, photon, imageOf(other, "image/png"), limits)
		if image != nil || !reflect.DeepEqual(notes, []string{note}) {
			t.Fatalf("other bytes: got %+v %v, want no image and %q", image, notes, note)
		}
		image, notes = fitOne(t, photon, ai.ImageContent{Data: "%%%", MimeType: "image/png"}, limits)
		if image != nil {
			t.Fatalf("malformed base64: got an image %+v", image)
		}
		if !regexp.MustCompile(`^An image \(image/png\) cannot be prepared for the model and was left out: `).MatchString(notes[0]) {
			t.Fatalf("malformed base64: note %q", notes[0])
		}
	})
}

var pictureParameters = map[string]any{"type": "object", "properties": map[string]any{}}

// fitSetup is a Harness with the fit-images extension over a faux model that takes images at most 100 pixels wide, and
// the tool "picture".
type fitSetup struct {
	faux     interface{ SetResponses([]ai.FauxResponseStep) }
	models   *ai.Models
	registry harness.Registry
}

func newFitSetup(t *testing.T, photon tools.ImageProcessor) *fitSetup {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{
		ID:          "faux-1",
		Input:       []string{"text", "image"},
		InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 100}}},
	}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	setup := &fitSetup{faux: faux, models: models, registry: harness.CreateRegistry()}
	if err := setup.registry.Install(fit.CreateFitImagesExtension(photon)); err != nil {
		t.Fatal(err)
	}
	return setup
}

func (setup *fitSetup) addTool(t *testing.T, name string, execute func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error)) {
	t.Helper()
	tool := harness.DefineTool(durable.ToolRegistration{
		Name: name, Description: "A picture", Parameters: pictureParameters,
		Execute: execute,
	})
	if err := setup.registry.Install(new(durable.Extension{Name: name, Tools: []*durable.ToolRegistration{tool}})); err != nil {
		t.Fatal(err)
	}
}

// run submits "show" to a new conversation whose model calls the given tools, and returns what it stored for each call.
func (setup *fitSetup) run(t *testing.T, calls ...string) map[string][]ai.ToolResultMessageContent {
	t.Helper()
	blocks := make([]ai.FauxContentBlock, 0, len(calls))
	for _, id := range calls {
		blocks = append(blocks, ai.FauxToolCall(toolOf(id), map[string]any{}, &ai.FauxToolCallOptions{ID: id}))
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: blocks, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}}),
	})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: setup.models, Registry: setup.registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(background) })
	root, err := opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("show")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := submission.Wait(background); err != nil {
		t.Fatal(err)
	}
	page, err := root.Entries(background, durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string][]ai.ToolResultMessageContent{}
	for index := range page.Items {
		if durable.ToolResultEntry.Is(&page.Items[index]) {
			message := page.Items[index].Model[0].(ai.ToolResultMessage)
			stored[message.ToolCallID] = message.Content
		}
	}
	return stored
}

// toolOf is the tool a call id names: the ids of these tests are "<tool>" or "<tool>:<suffix>".
func toolOf(id string) string {
	for index := range len(id) {
		if id[index] == ':' {
			return id[:index]
		}
	}
	return id
}

func TestFitImagesExtension(t *testing.T) {
	photon := photonImages(t)

	// images-fit.test.ts "fits a tool result's images to the conversation model's limits before it is committed"
	t.Run("fits a tool result's images to the conversation model's limits before it is committed", func(t *testing.T) {
		setup := newFitSetup(t, photon)
		small := imageOf(encode(t, 8, 6, "png"), "image/png")
		wide := imageOf(encode(t, 400, 100, "png"), "image/png")
		setup.addTool(t, "picture", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if api.CallId() == "picture:small" {
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{small}}, nil
			}
			return durable.ToolExecutionResult{
				Output:      []ai.ToolResultMessageContent{ai.TextContent{Text: "here"}, wide},
				Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityInfo, Message: "the tool's own"}},
			}, nil
		})
		stored := setup.run(t, "picture:wide", "picture:small")
		got := stored["picture:wide"]
		if len(got) != 3 {
			t.Fatalf("stored %d items, want 3: %+v", len(got), got)
		}
		if got[0] != ai.ToolResultMessageContent(ai.TextContent{Text: "here"}) {
			t.Fatalf("first item %+v", got[0])
		}
		if width, height := decodedSize(t, got[1].(ai.ImageContent).Data); width != 100 || height != 25 {
			t.Fatalf("image %dx%d, want 100x25", width, height)
		}
		wantText := "<harness>\n[info] the tool's own\n[info] Resized from 400x100 to 100x25. Multiply coordinates by 4.00 to map them to the original.\n</harness>"
		if got[2] != ai.ToolResultMessageContent(ai.TextContent{Text: wantText}) {
			t.Fatalf("diagnostics %+v, want %q", got[2], wantText)
		}
		// An image that fits is stored as the tool made it, without a note.
		if want := []ai.ToolResultMessageContent{small}; !reflect.DeepEqual(stored["picture:small"], want) {
			t.Fatalf("small stored %+v, want %+v", stored["picture:small"], want)
		}
	})

	// The hook as the Harness runs it, for the calls it leaves alone: a result without images, and a nested call whose
	// result the calling program gets, not the model.
	t.Run("leaves results without images, and nested calls' results, as they are", func(t *testing.T) {
		setup := newFitSetup(t, photon)
		wide := imageOf(encode(t, 400, 100, "png"), "image/png")
		setup.addTool(t, "picture", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{wide}}, nil
		})
		setup.addTool(t, "words", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "just text"}}}, nil
		})
		var nestedData string
		setup.addTool(t, "outer", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			nested, err := api.ExecuteTool(ctx, "picture", map[string]any{})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			encoded, err := json.Marshal(nested.StructuredOutput)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			var block struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(encoded, &block); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			nestedData = block.Data
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "outer done"}}}, nil
		})
		stored := setup.run(t, "words", "outer")
		if want := []ai.ToolResultMessageContent{ai.TextContent{Text: "just text"}}; !reflect.DeepEqual(stored["words"], want) {
			t.Fatalf("words stored %+v, want %+v", stored["words"], want)
		}
		if want := []ai.ToolResultMessageContent{ai.TextContent{Text: "outer done"}}; !reflect.DeepEqual(stored["outer"], want) {
			t.Fatalf("outer stored %+v, want %+v", stored["outer"], want)
		}
		if nestedData != wide.Data {
			t.Fatalf("the nested call's image was fitted: %d bytes of base64, want the tool's own %d", len(nestedData), len(wide.Data))
		}
	})

	// images-fit.test.ts "leaves nested calls' results to the program that made them"
	t.Run("leaves nested calls' results to the program that made them", func(t *testing.T) {
		registration := fit.CreateFitImagesExtension(photon).Hooks[0]
		if registration.Task != harness.ToolTask.AnyDefinition().Name {
			t.Fatalf("task %q, want the tool task", registration.Task)
		}
		afterTool := registration.Handlers.(*harness.ToolHooks).AfterTool
		result := durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{imageOf(encode(t, 4000, 10, "png"), "image/png")}}
		call := harness.NewToolHookCall(ai.ToolCall{ID: "n", Name: "picture", Arguments: map[string]any{}}, &harness.ToolParent{TaskId: 1, CallId: "c"})
		// A nested result is not looked at: the hook API, nil here, is never touched.
		replaced, err := afterTool(background, call, result, nil)
		if replaced != nil || err != nil {
			t.Fatalf("got %+v, %v, want nothing", replaced, err)
		}
	})
}
