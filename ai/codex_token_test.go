package ai

import (
	"encoding/base64"
	"encoding/json"
)

// codexToken is an API key the codex API accepts: openai-codex-responses.ts extractAccountId reads the ChatGPT account id from the JWT claim.
func codexToken() string {
	payload, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_test"}})
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}
