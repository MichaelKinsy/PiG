package cli

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestPrintCustomModelIDUnderAKeylessProviderKeepsTheOutputLimitAndCompat is the request of the copy TestRPCCustomModelIDKeepsTheProvidersLimitsCostAndCompat
// follows, where no credentials are resolved for it: print mode under the provider pig asks none of (ollama). Pi 1.5.2 answers "No API key found
// for ollama." there and sends nothing, as pig's RPC mode does, so Pi has no request to compare. With no credentials to resolve, the request's
// model was built again by provider and id, which name the provider's connection and no model: the request asked for one token
// (`max_completion_tokens: 1`) and ignored the provider's `maxTokensField`, while get_state reported the copied model. The request of the
// provider's own model sends `max_tokens` with its limit; the copy's is the same.
func TestPrintCustomModelIDUnderAKeylessProviderKeepsTheOutputLimitAndCompat(t *testing.T) {
	serverURL, requests := customModelCompletionsServer(t)
	config := `{"providers":{"ollama":{"baseUrl":"` + serverURL + `/v1","api":"openai-completions","models":[` +
		`{"id":"ollama-1","input":["text"],"contextWindow":36000,"maxTokens":2200,"compat":{"maxTokensField":"max_tokens"}}]}}}`
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, buildPigBinaryForSignalTest(t), "--no-session", "--model", "ollama/nosuch", "-p", "hello")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), customModelHomeEnv(t, config)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pig -p --model ollama/nosuch: %v\n%s", err, output)
	}

	select {
	case body := <-requests:
		if body["model"] != "nosuch" || body["max_tokens"] != float64(2200) || body["max_completion_tokens"] != nil {
			t.Fatalf("request: model %v, max_tokens %v, max_completion_tokens %v; want nosuch, 2200, absent", body["model"], body["max_tokens"], body["max_completion_tokens"])
		}
	default:
		t.Fatalf("the provider received no request; pig wrote:\n%s", output)
	}
}
