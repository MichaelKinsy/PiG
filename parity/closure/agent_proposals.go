package closure

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

const adversaryProposalType = "agent:adversary-proposal"

type agentProposalValue struct {
	SubmissionID string                             `json:"submissionId"`
	PacketID     string                             `json:"packetId"`
	Role         string                             `json:"role"`
	QuestionID   string                             `json:"questionId"`
	ProposalType string                             `json:"proposalType"`
	Rationale    string                             `json:"rationale"`
	Analyses     []string                           `json:"analyses"`
	Citations    []correspondence.AlignmentCitation `json:"citations"`
	Payload      json.RawMessage                    `json:"payload"`
}

func AddAgentBundleSubmission(records []Record, submission *correspondence.AgentBundleSubmission) ([]Record, error) {
	proposals, err := submission.Proposals()
	if err != nil {
		return nil, err
	}
	indexed := make(map[string]Record, len(records))
	for _, record := range records {
		indexed[record.RecordID()] = record
	}
	if _, ok := indexed[submission.Packet.SnapshotID].(*Snapshot); !ok {
		return nil, fmt.Errorf("agent bundle submission references unknown snapshot %s", submission.Packet.SnapshotID)
	}
	for _, obligationID := range submission.Packet.ObligationIDs {
		if _, ok := indexed[obligationID].(*Obligation); !ok {
			return nil, fmt.Errorf("agent bundle packet references unknown obligation %s", obligationID)
		}
	}
	for _, evidenceID := range submission.Packet.EvidenceIDs {
		if _, ok := indexed[evidenceID].(*EvidenceRun); !ok {
			return nil, fmt.Errorf("agent bundle packet references unknown evidence %s", evidenceID)
		}
	}
	return AddAgentProposals(records, proposals)
}

func AddAgentProposals(records []Record, proposals []correspondence.AgentProposal) ([]Record, error) {
	result := slices.Clone(records)
	indexed := make(map[string]Record, len(records))
	for _, record := range records {
		indexed[record.RecordID()] = record
	}
	for _, proposal := range proposals {
		snapshot, ok := indexed[proposal.SnapshotID].(*Snapshot)
		if !ok {
			return nil, fmt.Errorf("agent proposal %s references unknown snapshot %s", proposal.SubmissionID, proposal.SnapshotID)
		}
		if proposal.Source.Language != correspondence.LanguageTypeScript || proposal.Source.Revision != coding.UpstreamVersion || proposal.Target.Language != correspondence.LanguageGo || proposal.Target.Revision != snapshot.TargetCommit {
			return nil, fmt.Errorf("agent proposal %s source or target identity differs from snapshot", proposal.SubmissionID)
		}
		behaviorID, err := agentQuestionBehaviorID(proposal.QuestionID)
		if err != nil {
			return nil, err
		}
		if _, ok := indexed[behaviorID].(*Behavior); !ok {
			return nil, fmt.Errorf("agent proposal question %s has no behavior %s", proposal.QuestionID, behaviorID)
		}
		for _, analysis := range proposal.Analyses {
			obligationID := strings.Replace(behaviorID, "behavior:", "obligation:", 1) + ":" + analysis
			if _, ok := indexed[obligationID].(*Obligation); !ok {
				return nil, fmt.Errorf("agent proposal %s analysis %s has no obligation", proposal.QuestionID, analysis)
			}
		}
		pinIDs, err := proposalCitationPins(indexed, snapshot.ID, proposal.Citations)
		if err != nil {
			return nil, fmt.Errorf("agent proposal %s: %w", proposal.QuestionID, err)
		}
		value, err := json.Marshal(agentProposalValue{
			SubmissionID: proposal.SubmissionID, PacketID: proposal.PacketID, Role: proposal.Role, QuestionID: proposal.QuestionID,
			ProposalType: proposal.ProposalType, Rationale: proposal.Rationale, Analyses: proposal.Analyses,
			Citations: proposal.Citations, Payload: proposal.Payload,
		})
		if err != nil {
			return nil, err
		}
		hypothesisType := "agent:" + proposal.Role + "-proposal"
		idMaterial := strings.Join([]string{proposal.SubmissionID, proposal.QuestionID, proposal.ProposalType}, "\x00")
		hypothesis := &Hypothesis{
			Kind: KindHypothesis, ID: "hypothesis:agent:" + strings.TrimPrefix(HashBytes([]byte(idMaterial)), "sha256:"),
			SnapshotID: snapshot.ID, HypothesisType: hypothesisType, SubjectID: behaviorID, PinIDs: pinIDs, Value: value,
		}
		if existing := indexed[hypothesis.ID]; existing != nil {
			existingHash, hashErr := ContentHash(existing)
			if hashErr != nil {
				return nil, hashErr
			}
			hypothesisHash, hashErr := ContentHash(hypothesis)
			if hashErr != nil {
				return nil, hashErr
			}
			if existingHash != hypothesisHash {
				return nil, fmt.Errorf("agent proposal ID %s collides with different content", hypothesis.ID)
			}
			continue
		}
		indexed[hypothesis.ID] = hypothesis
		result = append(result, hypothesis)
	}
	return result, nil
}

func agentQuestionBehaviorID(questionID string) (string, error) {
	if setting, ok := strings.CutPrefix(questionID, "alignment:settings:"); ok {
		return "behavior:correspondence:setting:" + setting, nil
	}
	separator := strings.LastIndexByte(questionID, ':')
	if separator < 0 || separator == len(questionID)-1 || !strings.HasPrefix(questionID, "alignment:") {
		return "", fmt.Errorf("agent proposal has invalid question identity %s", questionID)
	}
	return "behavior:correspondence:function:" + questionID[separator+1:], nil
}

func proposalCitationPins(records map[string]Record, snapshotID string, citations []correspondence.AlignmentCitation) ([]string, error) {
	pinIDs := make([]string, 0, len(citations))
	for _, citation := range citations {
		repository := "upstream"
		if citation.Side == "target" {
			repository = "pig"
		} else if citation.Side != "source" {
			return nil, fmt.Errorf("citation has unknown side %s", citation.Side)
		}
		var matches []string
		for id, record := range records {
			pin, ok := record.(*Pin)
			if !ok || pin.SnapshotID != snapshotID || pin.Repository != repository {
				continue
			}
			if pin.Path == citation.Path && pin.StartLine == citation.StartLine && pin.EndLine == citation.EndLine && pin.QuoteHash == citation.SourceHash {
				matches = append(matches, id)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("citation %s:%d-%d resolves to %d pins", citation.Path, citation.StartLine, citation.EndLine, len(matches))
		}
		pinIDs = append(pinIDs, matches[0])
	}
	slices.Sort(pinIDs)
	pinIDs = slices.Compact(pinIDs)
	if len(pinIDs) < 2 {
		return nil, fmt.Errorf("proposal must bind distinct source and target pins")
	}
	return pinIDs, nil
}

func (g *Graph) unresolvedAdversaryFindings(behaviorID string) []*Hypothesis {
	var findings []*Hypothesis
	for _, id := range g.recordIDs(KindHypothesis) {
		finding := g.Records[id].(*Hypothesis)
		if finding.HypothesisType != adversaryProposalType || finding.SubjectID != behaviorID {
			continue
		}
		if g.scopedDecision(finding.ID, "dismiss-agent-finding") != nil {
			continue
		}
		findings = append(findings, finding)
	}
	return findings
}

func (g *Graph) agentFindingSupportIDs(behaviorID string) []string {
	var ids []string
	for _, id := range g.recordIDs(KindHypothesis) {
		finding := g.Records[id].(*Hypothesis)
		if finding.HypothesisType != adversaryProposalType || finding.SubjectID != behaviorID {
			continue
		}
		ids = append(ids, finding.ID)
		if decision := g.scopedDecision(finding.ID, "dismiss-agent-finding", "confirm-agent-finding"); decision != nil {
			ids = append(ids, decision.ID)
		}
	}
	slices.Sort(ids)
	return ids
}
