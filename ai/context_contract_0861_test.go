package ai

import "testing"

func TestProviderStreamRejectsNilContext(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	//lint:ignore SA1012 the test proves Stream rejects a nil Context.
	if _, err := provider.Stream(nil, NormalizeContext(Context{}), StreamOptions{}); err == nil { //nolint:staticcheck // SA1012: the test proves Stream rejects a nil Context.
		t.Fatal("Stream accepted a nil provider context")
	}
}

func TestEventIteratorRejectsNilContext(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	defer func() {
		if recover() == nil {
			t.Fatal("Events accepted a nil iterator context")
		}
	}()
	//lint:ignore SA1012 the test proves Events rejects a nil Context.
	_ = stream.Events(nil) //nolint:staticcheck // SA1012: the test proves Events rejects a nil Context.
}
