package correspondence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type AgentPrompt struct {
	ID             string          `json:"id"`
	PacketID       string          `json:"packetId"`
	Role           string          `json:"role"`
	OutputSchema   string          `json:"outputSchema"`
	OutputContract json.RawMessage `json:"outputContract"`
	Instructions   string          `json:"instructions"`
}

func (*AgentPrompt) agentContent() {}

func BuildAgentPrompt(packet *AgentWorkPacket) (*AgentPrompt, error) {
	if err := packet.Validate(); err != nil {
		return nil, err
	}
	roleInstruction := map[string]string{
		AnalystRole:             "Identify exact gaps, matches, ambiguity, and blockers.",
		AlignmentReviewerRole:   "Challenge every proposed source-to-target alignment.",
		ContractSynthesizerRole: "Propose facet-specific assertions, oracles, tests, and execution witnesses.",
		TranslatorRole:          "Propose production and regression patches plus facet-relevant mutation operators.",
		AdversaryRole:           "Find false proof, uncovered behavior, stale support, ambiguity, and blockers.",
	}[packet.Role]
	if roleInstruction == "" {
		return nil, fmt.Errorf("unsupported prompt role %q", packet.Role)
	}
	instructions := strings.Join([]string{
		roleInstruction,
		"Use only the immutable packet citations and stay within its question and path scope.",
		"Return only the strict " + packet.OutputSchema + " JSON shape.",
		"Narrative transcripts, confidence, and agreement are unciteable and grant no authority.",
		"Do not accept mappings, attest or sign evidence, derive verdicts, waive behavior, approve divergences, adjudicate contradictions, or integrate patches.",
	}, " ")
	contract, err := agentOutputContract(packet.OutputSchema)
	if err != nil {
		return nil, err
	}
	prompt := &AgentPrompt{PacketID: packet.ID, Role: packet.Role, OutputSchema: packet.OutputSchema, OutputContract: contract, Instructions: instructions}
	id, err := agentContentID("prompt", prompt)
	if err != nil {
		return nil, err
	}
	prompt.ID = id
	return prompt, nil
}

func (prompt *AgentPrompt) Validate(packet *AgentWorkPacket) error {
	if prompt == nil || packet == nil || prompt.PacketID != packet.ID || prompt.Role != packet.Role || prompt.OutputSchema != packet.OutputSchema || prompt.Instructions == "" {
		return fmt.Errorf("agent prompt differs from packet")
	}
	contract, err := agentOutputContract(packet.OutputSchema)
	if err != nil || !bytes.Equal(prompt.OutputContract, contract) {
		return fmt.Errorf("agent prompt output contract differs from packet")
	}
	wantID, err := agentContentID("prompt", prompt)
	if err != nil {
		return err
	}
	if prompt.ID != wantID {
		return fmt.Errorf("agent prompt ID does not match its content")
	}
	return nil
}

func agentOutputContract(schema string) (json.RawMessage, error) {
	contracts := map[string]struct {
		Collection string
		Entry      []string
	}{
		"analyst-bundle":    {Collection: "findings", Entry: []string{"questionId", "proposal", "rationale", "analyses", "citations"}},
		"alignment-bundle":  {Collection: "findings", Entry: []string{"questionId", "proposal", "rationale", "citations"}},
		"contract-bundle":   {Collection: "proposals", Entry: []string{"questionId", "analysis", "class", "oracle", "testRequirement", "witnessType", "citations"}},
		"translator-bundle": {Collection: "translations", Entry: []string{"questionId", "analysis", "rationale", "productionEdits[{path,originalHash,replacement}]", "testEdits[{path,originalHash,replacement}]", "evidencePlan", "mutationOperators", "citations"}},
		"adversary-bundle":  {Collection: "findings", Entry: []string{"questionId", "fault", "rationale", "analyses", "citations"}},
	}
	contract, ok := contracts[schema]
	if !ok {
		return nil, fmt.Errorf("unsupported output schema %q", schema)
	}
	encoded, err := json.Marshal(struct {
		AdditionalProperties bool     `json:"additionalProperties"`
		Required             []string `json:"required"`
		Collection           string   `json:"collection"`
		EntryFields          []string `json:"entryFields"`
	}{AdditionalProperties: false, Required: []string{"id", "role", "packetId", contract.Collection}, Collection: contract.Collection, EntryFields: contract.Entry})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
