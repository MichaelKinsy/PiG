//go:build pig_strip_bedrock_converse_stream

package ai

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// This build has no bedrock-converse-stream provider: the AWS SDK and smithy are not linked.
func init() { pigstrip.Strip(pigstrip.ListAPIs, string(APIBedrockConverseStream)) }

// NewBedrockAPIProvider reports that this Piglet strips bedrock-converse-stream.
func NewBedrockAPIProvider(model Model) (Provider, error) {
	return nil, strippedBedrockError(&model)
}

// awsResponseErrorStatus finds no AWS SDK response error: this build has no AWS SDK.
func awsResponseErrorStatus(error) (int, bool) { return 0, false }
