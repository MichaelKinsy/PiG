//go:build pig_strip_bedrock_converse_stream

package bedrock

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// This build has no bedrock-converse-stream provider: the AWS SDK and smithy are not linked, and the API stays unregistered.
func init() { pigstrip.Strip(pigstrip.ListAPIs, string(ai.APIBedrockConverseStream)) }
