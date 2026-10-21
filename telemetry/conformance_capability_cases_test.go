package telemetry_test

// Pins the optional-capability contract of the metrics, links and propagation cases of
// packages/telemetry/src/testing/conformance.ts: a fixture without Propagation or Metrics skips their cases (upstream's
// `if (!fixture.propagation) return`), a Metrics fixture without a measurement reader is an error, and an adapter that
// breaks one observable rule of the capability fails exactly the cases that check it.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// faultyPropagation forwards to the in-memory adapter and replaces one observable rule.
type faultyPropagation struct {
	*telemetry.InMemoryTelemetryContext
	inject  func(telemetry.TelemetryContext) telemetry.TelemetryCarrier
	extract func(telemetry.TelemetryCarrier) telemetry.TelemetryContext
}

func (p faultyPropagation) Inject(context telemetry.TelemetryContext) telemetry.TelemetryCarrier {
	if p.inject != nil {
		return p.inject(context)
	}
	return p.InMemoryTelemetryContext.Inject(context)
}

func (p faultyPropagation) Extract(carrier telemetry.TelemetryCarrier) telemetry.TelemetryContext {
	if p.extract != nil {
		return p.extract(carrier)
	}
	return p.InMemoryTelemetryContext.Extract(carrier)
}

// faultyMetrics forwards to the in-memory adapter and replaces one instrument factory.
type faultyMetrics struct {
	*telemetry.InMemoryTelemetryContext
	counter   func(string, telemetry.InstrumentOptions) telemetry.TelemetryCounter
	histogram func(string, telemetry.HistogramOptions) telemetry.TelemetryHistogram
}

func (m faultyMetrics) Counter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	if m.counter != nil {
		return m.counter(name, options)
	}
	return m.InMemoryTelemetryContext.Counter(name, options)
}

func (m faultyMetrics) Histogram(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
	if m.histogram != nil {
		return m.histogram(name, options)
	}
	return m.InMemoryTelemetryContext.Histogram(name, options)
}

// failingCases runs every conformance case on fixtures built by build and returns the sorted "group / name" of those that fail.
func failingCases(t *testing.T, build func(*telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture) []string {
	t.Helper()
	var failed []string
	for _, testCase := range telemetrytest.CreateTelemetryAdapterConformance(func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		return build(telemetry.NewInMemoryTelemetryContext()), nil
	}) {
		if err := testCase.Run(t.Context()); err != nil {
			failed = append(failed, testCase.Group+" / "+testCase.Name)
		}
	}
	slices.Sort(failed)
	return failed
}

func fullFixture(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
	return telemetrytest.TelemetryAdapterFixture{
		Context:     recorder,
		Propagation: recorder,
		Metrics:     recorder,
		GetMetrics: func(context.Context) ([]telemetry.RecordedTelemetryMeasurement, error) {
			return recorder.GetMetrics(), nil
		},
		GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) { return recorder.GetSpans(), nil },
	}
}

// Pi source: packages/telemetry/src/testing/conformance.ts
// mutation-checked: each faulty adapter below fails exactly the listed cases
func TestConformanceCapabilityCasesCheckTheirRules(t *testing.T) {
	t.Run("a fixture without the optional capabilities skips their cases", func(t *testing.T) {
		if failed := failingCases(t, func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
			fixture := fullFixture(recorder)
			fixture.Propagation, fixture.Metrics, fixture.GetMetrics = nil, nil, nil
			return fixture
		}); len(failed) != 0 {
			t.Fatalf("failing cases = %v, want none", failed)
		}
	})

	t.Run("a Metrics fixture without a measurement reader is an error", func(t *testing.T) {
		failed := failingCases(t, func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
			fixture := fullFixture(recorder)
			fixture.GetMetrics = nil
			return fixture
		})
		if want := []string{"metrics / copies instrument metadata and measurement attributes", "metrics / records ordered measurements and optional histogram exemplars"}; !slices.Equal(failed, want) {
			t.Fatalf("failing cases = %v, want %v", failed, want)
		}
	})

	for _, test := range []struct {
		name   string
		build  func(*telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture
		failed []string
	}{
		{
			"inject returns no carrier",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.Propagation = faultyPropagation{InMemoryTelemetryContext: recorder, inject: func(telemetry.TelemetryContext) telemetry.TelemetryCarrier { return nil }}
				return fixture
			},
			[]string{"capabilities / carries adapter capabilities through contexts and spans", "propagation / propagates across fresh adapters and multiple hops", "propagation / roundtrips parent identity through JSON without retaining the carrier"},
		},
		{
			"extract ignores the carrier",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.Propagation = faultyPropagation{InMemoryTelemetryContext: recorder, extract: func(telemetry.TelemetryCarrier) telemetry.TelemetryContext { return recorder }}
				return fixture
			},
			[]string{"capabilities / carries adapter capabilities through contexts and spans", "propagation / propagates across fresh adapters and multiple hops", "propagation / roundtrips parent identity through JSON without retaining the carrier"},
		},
		{
			"inject returns a carrier for a no-op context",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.Propagation = faultyPropagation{InMemoryTelemetryContext: recorder, inject: func(telemetry.TelemetryContext) telemetry.TelemetryCarrier {
					return telemetry.TelemetryCarrier{"traceparent": "x"}
				}}
				return fixture
			},
			[]string{"capabilities / carries adapter capabilities through contexts and spans", "propagation / absent and unknown carriers produce recorded roots", "propagation / propagates across fresh adapters and multiple hops", "propagation / roundtrips parent identity through JSON without retaining the carrier"},
		},
		{
			"a counter accepts negative increments",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.Metrics = faultyMetrics{InMemoryTelemetryContext: recorder, counter: func(name string, options telemetry.InstrumentOptions) telemetry.TelemetryCounter {
					return recorder.UpDownCounter(name, options)
				}}
				return fixture
			},
			[]string{"capabilities / carries adapter capabilities through contexts and spans", "metrics / records ordered measurements and optional histogram exemplars"},
		},
		{
			"a histogram drops its boundaries",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.Metrics = faultyMetrics{InMemoryTelemetryContext: recorder, histogram: func(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
					options.Boundaries = nil
					return recorder.Histogram(name, options)
				}}
				return fixture
			},
			[]string{"capabilities / carries adapter capabilities through contexts and spans", "metrics / copies instrument metadata and measurement attributes", "metrics / records ordered measurements and optional histogram exemplars"},
		},
		{
			"a histogram keeps the caller's boundaries and options",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.Metrics = faultyMetrics{InMemoryTelemetryContext: recorder, histogram: func(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
					return aliasedHistogram{recorder: recorder, name: name, options: options}
				}}
				return fixture
			},
			[]string{"capabilities / carries adapter capabilities through contexts and spans", "metrics / copies instrument metadata and measurement attributes"},
		},
		{
			"the span snapshots gain a link after the source settled",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.GetSpans = func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) {
					spans := recorder.GetSpans()
					for i := range spans {
						if spans[i].Name == "link-source" {
							spans[i].Links = append(spans[i].Links, telemetry.RecordedTelemetryLink{Context: spans[i].Context, Attributes: telemetry.SpanAttributes{telemetry.Attr("late", true)}})
						}
					}
					return spans, nil
				}
				return fixture
			},
			[]string{"links / ignores links added after source settlement"},
		},
		{
			"a link replaces the span's parent",
			func(recorder *telemetry.InMemoryTelemetryContext) telemetrytest.TelemetryAdapterFixture {
				fixture := fullFixture(recorder)
				fixture.GetSpans = func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) {
					spans := recorder.GetSpans()
					for i := range spans {
						if spans[i].Name == "linked" && len(spans[i].Links) > 0 {
							spans[i].ParentContext = spans[i].Links[0].Context
						}
					}
					return spans, nil
				}
				return fixture
			},
			[]string{"links / records initial and late links without changing parents"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if failed := failingCases(t, test.build); !slices.Equal(failed, test.failed) {
				t.Fatalf("failing cases = %v, want %v", failed, test.failed)
			}
		})
	}

	t.Run("a failing fixture reader fails the case with its error", func(t *testing.T) {
		readFailure := errors.New("no metrics")
		cases := telemetrytest.CreateTelemetryAdapterConformance(func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
			fixture := fullFixture(telemetry.NewInMemoryTelemetryContext())
			fixture.GetMetrics = func(context.Context) ([]telemetry.RecordedTelemetryMeasurement, error) { return nil, readFailure }
			return fixture, nil
		})
		for _, testCase := range cases {
			if strings.HasPrefix(testCase.Group, "metrics") {
				if err := testCase.Run(t.Context()); !errors.Is(err, readFailure) {
					t.Fatalf("%s: err = %v, want the reader's error", testCase.Name, err)
				}
			}
		}
	})
}

// aliasedHistogram keeps the options the caller passed instead of copying them, the defect the copy case detects: the recorded boundaries and description follow the caller's later mutation.
type aliasedHistogram struct {
	recorder *telemetry.InMemoryTelemetryContext
	name     string
	options  telemetry.HistogramOptions
}

func (h aliasedHistogram) Record(value float64, attributes telemetry.SpanAttributes, context telemetry.TelemetryContext) {
	h.recorder.Histogram(h.name, h.options).Record(value, attributes, context)
}
