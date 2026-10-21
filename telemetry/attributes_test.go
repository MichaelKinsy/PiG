package telemetry_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

func attributeNames(attributes telemetry.SpanAttributes) []string {
	names := make([]string, len(attributes))
	for index, attribute := range attributes {
		names[index] = attribute.Name
	}
	return names
}

// SpanAttributes mirrors a JavaScript object's own string keys: insertion order, an overwritten name keeping its
// position, and canonical array-index names before every other name in ascending numeric order.
func TestSpanAttributesSetFollowsObjectKeyOrder(t *testing.T) {
	attributes := telemetry.SpanAttributes{}
	for _, name := range []string{"b", "a", "10", "01", "2", "-1", "4294967294", "4294967295", "9", "a"} {
		attributes = attributes.Set(name, name)
	}
	want := []string{"2", "9", "10", "4294967294", "b", "a", "01", "-1", "4294967295"}
	if got := attributeNames(attributes); !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if value, ok := attributes.Get("a"); !ok || value != "a" {
		t.Fatalf("Get(a) = %v, %v", value, ok)
	}
	if value, ok := attributes.Get("missing"); ok || value != nil || attributes.Value("missing") != nil {
		t.Fatalf("Get(missing) = %v, %v", value, ok)
	}
}

func TestSpanAttributesMergeOverwritesInPlaceAndAppends(t *testing.T) {
	base := telemetry.SpanAttributes{telemetry.Attr("a", 1), telemetry.Attr("b", 2)}
	merged := base.Merge(telemetry.SpanAttributes{telemetry.Attr("c", 3), telemetry.Attr("a", 4), telemetry.Attr("b", nil)})
	if want := (telemetry.SpanAttributes{telemetry.Attr("a", 4), telemetry.Attr("b", 2), telemetry.Attr("c", 3)}); !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
	if want := (telemetry.SpanAttributes{telemetry.Attr("a", 1), telemetry.Attr("b", 2)}); !reflect.DeepEqual(base, want) {
		t.Fatalf("Merge changed the receiver: %v", base)
	}
}

func TestSpanAttributesMarshalJSONWritesAnObjectInOrder(t *testing.T) {
	encoded, err := json.Marshal(struct {
		Present telemetry.SpanAttributes `json:"present"`
		Absent  telemetry.SpanAttributes `json:"absent"`
		Empty   telemetry.SpanAttributes `json:"empty"`
	}{telemetry.SpanAttributes{telemetry.Attr("z", 1), telemetry.Attr("a", []string{"x"}), telemetry.Attr("m", "é")}, nil, telemetry.SpanAttributes{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"present":{"z":1,"a":["x"],"m":"é"},"absent":null,"empty":{}}`; string(encoded) != want {
		t.Fatalf("JSON = %s, want %s", encoded, want)
	}
}

// A recorder stores each name once at the position of its first write, drops undefined values, and detaches the bag.
func TestInMemoryRecorderKeepsAttributeInsertionOrder(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "ordered", Attributes: telemetry.SpanAttributes{telemetry.Attr("z", 1), telemetry.Attr("a", 2), telemetry.Attr("gone", 3), telemetry.Attr("gone", nil), telemetry.Attr("z", 4)}}, func(span telemetry.TelemetrySpan) error {
		span.SetAttributes(telemetry.SpanAttributes{telemetry.Attr("m", 5), telemetry.Attr("a", 6), telemetry.Attr("skipped", nil)})
		span.AddEvent("event", telemetry.SpanAttributes{telemetry.Attr("y", 1), telemetry.Attr("x", 2)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	span := recorder.GetSpans()[0]
	if want := (telemetry.SpanAttributes{telemetry.Attr("z", 4), telemetry.Attr("a", 6), telemetry.Attr("m", 5)}); !reflect.DeepEqual(span.Attributes, want) {
		t.Fatalf("attributes = %v, want %v", span.Attributes, want)
	}
	if got := attributeNames(span.Events[0].Attributes); !reflect.DeepEqual(got, []string{"y", "x"}) {
		t.Fatalf("event attribute order = %v", got)
	}
}
