package coding_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func newTestFauxRuntime(t testing.TB) (*coding.Services, *ai.Model) {
	t.Helper()
	t.Setenv("PIG_TEST_FAUX", "1")
	t.Setenv("PIG_TEST_FAUX_SCENARIO", "parity-basic")
	agentDir := t.TempDir()
	config := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := coding.BuildModel("test-faux/faux-1", services)
	if err != nil {
		t.Fatal(err)
	}
	// A configured identity is essential: otherwise prepareRequest bypasses BuildModel.
	if model.ProviderMeta.ProviderID != "test-faux" {
		t.Fatalf("provider metadata = %#v", model.ProviderMeta)
	}
	return services, model
}

func testFauxResultIDs(t testing.TB, result *ai.AssistantMessage) []string {
	t.Helper()
	if result.StopReason != ai.StopReasonStop && result.StopReason != ai.StopReasonToolUse {
		t.Fatalf("result = %#v", result)
	}
	var ids []string
	for _, block := range result.Content {
		if call, ok := block.(ai.ToolCall); ok {
			ids = append(ids, call.ID)
		}
	}
	return ids
}

// Pi's ModelRuntime.prepareRequest retrieves the registered provider, preserving its state across requests even when no prior messages are supplied.
func TestModelRuntimeTestFauxToolCallIDs(t *testing.T) {
	services, model := newTestFauxRuntime(t)
	runtime := services.ModelRuntime()
	for _, mode := range []struct {
		name string
		run  func(context.Context, *ai.Model, ai.Context, ai.StreamOptions) *ai.AssistantMessage
	}{
		{"Complete", runtime.Complete},
		{"Stream", func(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
			return runtime.Stream(ctx, model, request, options).Result()
		}},
		{"StreamSimple", func(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
			return runtime.StreamSimple(ctx, model, request, options).Result()
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, tc := range []struct {
				session string
				prompt  string
				want    []string
			}{
				{"retained", "What is 20+22?", nil},
				{"retained", "Run: expr 20 + 22", []string{"call_test_faux_1"}},
				{"retained", "Run: expr 20 + 22", []string{"call_test_faux_2"}},
				{"isolated", "Run: expr 20 + 22", []string{"call_test_faux_1"}},
				{"retained", "Run: parallel reads", []string{"call_test_faux_3", "call_test_faux_4"}},
			} {
				request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(tc.prompt)}}}
				result := mode.run(t.Context(), model, request, ai.StreamOptions{SessionID: mode.name + "/" + tc.session})
				if ids := testFauxResultIDs(t, result); !slices.Equal(ids, tc.want) {
					t.Errorf("%s/%s: IDs = %v, want %v", tc.session, tc.prompt, ids, tc.want)
				}
			}
		})
	}
	// The omitted-session counter is retained through the registry facade too.
	for i := 1; i <= 2; i++ {
		request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Run: expr 20 + 22")}}}
		result := services.Registry().Complete(t.Context(), model, request, ai.StreamOptions{})
		want := []string{fmt.Sprintf("call_test_faux_%d", i)}
		if ids := testFauxResultIDs(t, result); !slices.Equal(ids, want) {
			t.Errorf("default session: IDs = %v, want %v", ids, want)
		}
	}
}

func TestModelRuntimeTestFauxToolCallIDsConcurrent(t *testing.T) {
	services, model := newTestFauxRuntime(t)
	const requests = 32
	results := make(chan *ai.AssistantMessage, requests)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() {
			<-start
			request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Run: parallel reads")}}}
			results <- services.ModelRuntime().Complete(t.Context(), model, request, ai.StreamOptions{SessionID: "concurrent"})
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var numbers []int
	for result := range results {
		ids := testFauxResultIDs(t, result)
		if len(ids) != 2 {
			t.Fatalf("batch IDs = %v, want two", ids)
		}
		var batch []int
		for _, id := range ids {
			suffix, ok := strings.CutPrefix(id, "call_test_faux_")
			n, err := strconv.Atoi(suffix)
			if !ok || err != nil {
				t.Fatalf("invalid ID %q", id)
			}
			batch = append(batch, n)
		}
		if batch[1] != batch[0]+1 {
			t.Errorf("non-contiguous batch: %v", ids)
		}
		numbers = append(numbers, batch...)
	}
	slices.Sort(numbers)
	want := make([]int, requests*2)
	for i := range want {
		want[i] = i + 1
	}
	if !slices.Equal(numbers, want) {
		t.Errorf("allocated IDs = %v, want %v", numbers, want)
	}
}
