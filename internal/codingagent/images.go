package codingagent

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

func NormalizeToolResultImages(result agent.AgentToolResult, autoResize bool) agent.AgentToolResult {
	return imageprocessing.NormalizeToolResultImages(result, autoResize)
}

func PrepareCLIImageAttachment(data []byte, mime string) ([]byte, string, string, error) {
	return imageprocessing.PrepareCLIImageAttachment(data, mime)
}

func DetectSupportedImageMimeType(data []byte) string {
	return imageprocessing.DetectSupportedImageMimeType(data)
}
