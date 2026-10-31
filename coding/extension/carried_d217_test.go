package extension

// Pig divergence D217: Pi 2e0f175b (#11368) removes the realtime and image rows that models.dev does not type as chat models from the chat catalog. The pinned Pi
// that this oracle runs still lists them, so those rows are taken out of Pi's model objects before the comparison. Every other model object is still compared with Pi.
var d217CarriedDroppedChatRefs = []string{
	"azure/gpt-realtime-2.1",
	"openai/gpt-realtime-2.1",
	"google/gemini-3.1-flash-live-preview",
	"google/gemini-3.1-flash-lite-image",
	"google-vertex/gemini-3.1-flash-lite-image",
}
