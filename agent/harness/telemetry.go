package harness

// StartAiSpan invokes callback with an AI-request span and a derived context that parents nested telemetry to that span. It waits for callback and returns its result and error unchanged. Cancellation remains the callback's responsibility; the wrapper does not validate schema attributes or set span status.
func StartAiSpan[Result any](ctx Context, name string, attributes SpanAttributes, callback func(TelemetrySpan, Context) (Result, error)) (Result, error) {
	return startTelemetrySpan(ctx, name, attributes, callback)
}

// StartHarnessSpan invokes callback with a harness span and a derived context that parents nested telemetry to that span. It waits for callback and returns its result and error unchanged. Cancellation remains the callback's responsibility; the wrapper does not validate schema attributes or set span status.
func StartHarnessSpan[Result any](ctx Context, name string, attributes SpanAttributes, callback func(TelemetrySpan, Context) (Result, error)) (Result, error) {
	return startTelemetrySpan(ctx, name, attributes, callback)
}

func startTelemetrySpan[Result any](ctx Context, name string, attributes SpanAttributes, callback func(TelemetrySpan, Context) (Result, error)) (Result, error) {
	var result Result
	err := GetTelemetryContext(ctx).StartSpan(SpanOptions{Name: name, Attributes: attributes}, func(span TelemetrySpan) error {
		var err error
		result, err = callback(span, WithTelemetryContext(ctx, span))
		return err
	})
	return result, err
}
