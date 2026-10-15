package ai

import (
	"strings"
	"testing"
)

func cloneGeneratedModel(t *testing.T, spec string) *GeneratedModel {
	t.Helper()
	provider, id, ok := strings.Cut(spec, "/")
	if !ok {
		t.Fatalf("expected provider/model, got %q", spec)
	}
	model := *mustGeneratedModel(t, provider, id)
	model.Compat = cloneCompat(model.Compat)
	return &model
}
