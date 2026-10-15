package agent

import "github.com/MichaelKinsy/PiG/ai"

// convertToLLM is the request a real provider converts when the agent loop sends msgs: the loop's ConvertToLLM, the D48 repair its RepairToolFlow option asks for, and the provider's transformMessages.
func convertToLLM(msgs []AgentMessage, model *ai.Model) []ai.Message {
	if model == nil {
		model = &ai.Model{Input: []string{"text", "image"}}
	}
	return ai.TransformMessages(ai.RepairToolFlow(ConvertToLLM(msgs)), model, nil)
}
