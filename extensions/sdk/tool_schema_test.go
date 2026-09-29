package sdk

import "testing"

func TestToolSchemaValidationBeforeRegistration(t *testing.T) {
	for _, variant := range []string{"tool", "prepare", "guidelines", "source", "sampling"} {
		t.Run(variant, func(t *testing.T) {
			ext := New("schema")
			register := func(schema Schema) {
				handler := func(Context, map[string]any) (any, error) { return nil, nil }
				switch variant {
				case "tool":
					ext.Tool("noop", "No-op", schema, handler)
				case "prepare":
					ext.ToolWithPrepareArguments("noop", "No-op", schema, nil, handler)
				case "guidelines":
					ext.ToolWithGuidelines("noop", "No-op", schema, nil, handler)
				case "source":
					ext.ToolWithSource("noop", "No-op", schema, "source", nil, handler)
				case "sampling":
					ext.ToolWithConstrainedSampling("noop", "No-op", schema, ConstrainedSampling{}, handler)
				}
			}
			var caught any
			func() { defer func() { caught = recover() }(); register(nil) }()
			want := `Tool "noop" registered by extension "schema" must define an object parameter schema.`
			if caught == nil {
				t.Fatal("nil schema did not fail synchronously")
			}
			if err, ok := caught.(error); !ok || err.Error() != want {
				t.Fatalf("failure=%v; want %q", caught, want)
			}
			if len(ext.tools) != 0 || len(ext.toolFuncs) != 0 || len(ext.toolPrepareFuncs) != 0 {
				t.Fatal("failed declaration retained registration state")
			}
			register(Schema{})
			if len(ext.tools) != 1 || len(ext.toolFuncs) != 1 {
				t.Fatal("empty object schema did not register")
			}
		})
	}
}
