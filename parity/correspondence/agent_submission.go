package correspondence

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type AgentBundleSubmission struct {
	ID         string            `json:"id"`
	Role       string            `json:"role"`
	Packet     *AgentWorkPacket  `json:"packet"`
	Analyst    *AnalystBundle    `json:"analyst,omitempty"`
	Alignment  *AlignmentBundle  `json:"alignment,omitempty"`
	Contract   *ContractBundle   `json:"contract,omitempty"`
	Translator *TranslatorBundle `json:"translator,omitempty"`
	Adversary  *AdversaryBundle  `json:"adversary,omitempty"`
}

// AgentBundleSubmissionResult identifies the canonical path published for a
// validated submission. The path is repository-relative so direct and
// extension adapters expose the same deterministic result.
type AgentBundleSubmissionResult struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type AgentProposal struct {
	SubmissionID string
	PacketID     string
	SnapshotID   string
	Source       SourceIdentity
	Target       SourceIdentity
	Role         string
	QuestionID   string
	ProposalType string
	Rationale    string
	Analyses     []string
	Citations    []AlignmentCitation
	Payload      json.RawMessage
}

func (*AgentBundleSubmission) agentContent() {}

func BindAgentBundleSubmission(packet *AgentWorkPacket, bundle AgentContent) (*AgentBundleSubmission, error) {
	submission := &AgentBundleSubmission{Packet: packet}
	switch value := bundle.(type) {
	case *AnalystBundle:
		submission.Role = AnalystRole
		submission.Analyst = value
	case *AlignmentBundle:
		submission.Role = AlignmentReviewerRole
		submission.Alignment = value
	case *ContractBundle:
		submission.Role = ContractSynthesizerRole
		submission.Contract = value
	case *TranslatorBundle:
		submission.Role = TranslatorRole
		submission.Translator = value
	case *AdversaryBundle:
		submission.Role = AdversaryRole
		submission.Adversary = value
	default:
		return nil, fmt.Errorf("unsupported agent bundle %T", bundle)
	}
	id, err := agentContentID("submission", submission)
	if err != nil {
		return nil, err
	}
	submission.ID = id
	if err := submission.Validate(); err != nil {
		return nil, err
	}
	return submission, nil
}

func DecodeAgentBundleSubmission(reader io.Reader) (*AgentBundleSubmission, error) {
	var submission AgentBundleSubmission
	if err := decodeAgentJSON(reader, &submission, "agent bundle submission"); err != nil {
		return nil, err
	}
	if err := submission.Validate(); err != nil {
		return nil, err
	}
	return &submission, nil
}

func (submission *AgentBundleSubmission) Validate() error {
	if submission == nil || submission.Packet == nil || submission.ID == "" || submission.Role == "" {
		return fmt.Errorf("agent bundle submission has incomplete identity")
	}
	if err := submission.Packet.Validate(); err != nil {
		return err
	}
	if submission.Role != submission.Packet.Role {
		return fmt.Errorf("agent bundle submission role differs from packet")
	}
	bundleCount := 0
	if submission.Analyst != nil {
		bundleCount++
		if submission.Role != AnalystRole {
			return fmt.Errorf("agent bundle submission contains bundle for another role")
		}
		if err := submission.Analyst.Validate(submission.Packet); err != nil {
			return err
		}
	}
	if submission.Alignment != nil {
		bundleCount++
		if submission.Role != AlignmentReviewerRole {
			return fmt.Errorf("agent bundle submission contains bundle for another role")
		}
		if err := submission.Alignment.Validate(submission.Packet); err != nil {
			return err
		}
	}
	if submission.Contract != nil {
		bundleCount++
		if submission.Role != ContractSynthesizerRole {
			return fmt.Errorf("agent bundle submission contains bundle for another role")
		}
		if err := submission.Contract.Validate(submission.Packet); err != nil {
			return err
		}
	}
	if submission.Translator != nil {
		bundleCount++
		if submission.Role != TranslatorRole {
			return fmt.Errorf("agent bundle submission contains bundle for another role")
		}
		if err := submission.Translator.Validate(submission.Packet); err != nil {
			return err
		}
	}
	if submission.Adversary != nil {
		bundleCount++
		if submission.Role != AdversaryRole {
			return fmt.Errorf("agent bundle submission contains bundle for another role")
		}
		if err := submission.Adversary.Validate(submission.Packet); err != nil {
			return err
		}
	}
	if bundleCount != 1 {
		return fmt.Errorf("agent bundle submission must contain exactly one role bundle")
	}
	wantID, err := agentContentID("submission", submission)
	if err != nil {
		return err
	}
	if submission.ID != wantID {
		return fmt.Errorf("agent bundle submission ID does not match its content")
	}
	return nil
}

func (submission *AgentBundleSubmission) Proposals() ([]AgentProposal, error) {
	if err := submission.Validate(); err != nil {
		return nil, err
	}
	var proposals []AgentProposal
	switch submission.Role {
	case AnalystRole:
		for _, finding := range submission.Analyst.Findings {
			payload, err := json.Marshal(finding)
			if err != nil {
				return nil, err
			}
			proposals = append(proposals, AgentProposal{
				SubmissionID: submission.ID, PacketID: submission.Packet.ID, SnapshotID: submission.Packet.SnapshotID,
				Source: submission.Packet.Source, Target: submission.Packet.Target,
				Role: submission.Role, QuestionID: finding.QuestionID, ProposalType: finding.Proposal,
				Rationale: finding.Rationale, Analyses: finding.Analyses, Citations: finding.Citations, Payload: payload,
			})
		}
	case AlignmentReviewerRole:
		for _, finding := range submission.Alignment.Findings {
			payload, err := json.Marshal(finding)
			if err != nil {
				return nil, err
			}
			question, ok := agentQuestion(submission.Packet, finding.QuestionID)
			if !ok {
				return nil, fmt.Errorf("alignment finding %s is outside packet scope", finding.QuestionID)
			}
			proposals = append(proposals, AgentProposal{
				SubmissionID: submission.ID, PacketID: submission.Packet.ID, SnapshotID: submission.Packet.SnapshotID,
				Source: submission.Packet.Source, Target: submission.Packet.Target,
				Role: submission.Role, QuestionID: finding.QuestionID, ProposalType: finding.Proposal,
				Rationale: finding.Rationale, Analyses: question.RequiredAnalyses, Citations: finding.Citations, Payload: payload,
			})
		}
	case ContractSynthesizerRole:
		for _, proposal := range submission.Contract.Proposals {
			payload, err := json.Marshal(proposal)
			if err != nil {
				return nil, err
			}
			proposals = append(proposals, AgentProposal{
				SubmissionID: submission.ID, PacketID: submission.Packet.ID, SnapshotID: submission.Packet.SnapshotID,
				Source: submission.Packet.Source, Target: submission.Packet.Target,
				Role: submission.Role, QuestionID: proposal.QuestionID, ProposalType: proposal.Class + ":" + proposal.Analysis,
				Rationale: proposal.TestRequirement, Analyses: []string{proposal.Analysis}, Citations: proposal.Citations, Payload: payload,
			})
		}
	case TranslatorRole:
		for _, translation := range submission.Translator.Translations {
			payload, err := json.Marshal(translation)
			if err != nil {
				return nil, err
			}
			proposals = append(proposals, AgentProposal{
				SubmissionID: submission.ID, PacketID: submission.Packet.ID, SnapshotID: submission.Packet.SnapshotID,
				Source: submission.Packet.Source, Target: submission.Packet.Target,
				Role: submission.Role, QuestionID: translation.QuestionID, ProposalType: "translation:" + translation.Analysis,
				Rationale: translation.Rationale, Analyses: []string{translation.Analysis}, Citations: translation.Citations, Payload: payload,
			})
		}
	case AdversaryRole:
		for _, finding := range submission.Adversary.Findings {
			payload, err := json.Marshal(finding)
			if err != nil {
				return nil, err
			}
			proposals = append(proposals, AgentProposal{
				SubmissionID: submission.ID, PacketID: submission.Packet.ID, SnapshotID: submission.Packet.SnapshotID,
				Source: submission.Packet.Source, Target: submission.Packet.Target,
				Role: submission.Role, QuestionID: finding.QuestionID, ProposalType: finding.Fault,
				Rationale: finding.Rationale, Analyses: finding.Analyses, Citations: finding.Citations, Payload: payload,
			})
		}
	}
	return proposals, nil
}

// SubmitAgentBundle decodes, validates, and atomically publishes one agent
// bundle through the same path used by every Porter adapter.
func SubmitAgentBundle(root, packetInput, bundleInput, outputDirectory string) (AgentBundleSubmissionResult, error) {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return AgentBundleSubmissionResult{}, fmt.Errorf("resolve submission root: %w", err)
	}
	packetPath, err := resolveAgentInput(rootReal, packetInput)
	if err != nil {
		return AgentBundleSubmissionResult{}, fmt.Errorf("resolve packet input: %w", err)
	}
	packetFile, err := os.Open(packetPath)
	if err != nil {
		return AgentBundleSubmissionResult{}, fmt.Errorf("open packet input: %w", err)
	}
	packet, decodeErr := DecodeAgentWorkPacket(packetFile)
	closeErr := packetFile.Close()
	if decodeErr != nil {
		return AgentBundleSubmissionResult{}, decodeErr
	}
	if closeErr != nil {
		return AgentBundleSubmissionResult{}, fmt.Errorf("close packet input: %w", closeErr)
	}
	bundlePath, err := resolveAgentInput(rootReal, bundleInput)
	if err != nil {
		return AgentBundleSubmissionResult{}, fmt.Errorf("resolve bundle input: %w", err)
	}
	bundleFile, err := os.Open(bundlePath)
	if err != nil {
		return AgentBundleSubmissionResult{}, fmt.Errorf("open bundle input: %w", err)
	}
	var bundle AgentContent
	switch packet.Role {
	case AnalystRole:
		bundle, decodeErr = DecodeAnalystBundle(bundleFile, packet)
	case AlignmentReviewerRole:
		bundle, decodeErr = DecodeAlignmentBundle(bundleFile, packet)
	case ContractSynthesizerRole:
		bundle, decodeErr = DecodeContractBundle(bundleFile, packet)
	case TranslatorRole:
		bundle, decodeErr = DecodeTranslatorBundle(bundleFile, packet)
	case AdversaryRole:
		bundle, decodeErr = DecodeAdversaryBundle(bundleFile, packet)
	default:
		decodeErr = fmt.Errorf("unsupported packet role %s", packet.Role)
	}
	closeErr = bundleFile.Close()
	if decodeErr != nil {
		return AgentBundleSubmissionResult{}, decodeErr
	}
	if closeErr != nil {
		return AgentBundleSubmissionResult{}, fmt.Errorf("close bundle input: %w", closeErr)
	}
	submission, err := BindAgentBundleSubmission(packet, bundle)
	if err != nil {
		return AgentBundleSubmissionResult{}, err
	}
	if err := validateTranslationOriginalHashes(rootReal, submission); err != nil {
		return AgentBundleSubmissionResult{}, err
	}
	publishedPath, err := WriteAgentBundleSubmission(rootReal, outputDirectory, submission)
	if err != nil {
		return AgentBundleSubmissionResult{}, err
	}
	relativePath, err := filepath.Rel(rootReal, publishedPath)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return AgentBundleSubmissionResult{}, fmt.Errorf("published submission escapes repository root")
	}
	return AgentBundleSubmissionResult{ID: submission.ID, Path: filepath.ToSlash(relativePath)}, nil
}

func validateTranslationOriginalHashes(root string, submission *AgentBundleSubmission) error {
	if submission.Translator == nil {
		return nil
	}
	for _, translation := range submission.Translator.Translations {
		edits := append(slices.Clone(translation.ProductionEdits), translation.TestEdits...)
		for _, edit := range edits {
			command := exec.Command("git", "show", submission.Packet.Target.Revision+":"+edit.Path)
			command.Dir = root
			data, err := command.Output()
			if err != nil {
				return fmt.Errorf("translator edit %s is absent from target commit: %w", edit.Path, err)
			}
			if hashString(string(data)) != edit.OriginalHash {
				return fmt.Errorf("translator edit %s original hash differs from target commit", edit.Path)
			}
		}
	}
	return nil
}

func resolveAgentInput(root, configured string) (string, error) {
	if configured == "" || configured != strings.TrimSpace(configured) || strings.ContainsRune(configured, '\x00') || filepath.IsAbs(configured) || !filepath.IsLocal(configured) || filepath.ToSlash(filepath.Clean(configured)) != configured {
		return "", fmt.Errorf("input path must be canonical and repository-relative")
	}
	path := filepath.Join(root, configured)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("input escapes repository root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("input is not a regular file")
	}
	return resolved, nil
}

func WriteAgentBundleSubmission(root, relativeDirectory string, submission *AgentBundleSubmission) (_ string, resultErr error) {
	if err := submission.Validate(); err != nil {
		return "", err
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve submission root: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return "", fmt.Errorf("resolve submission root: %w", err)
	}
	directoryRelative := filepath.Join(filepath.FromSlash(relativeDirectory), strings.TrimPrefix(submission.Packet.SnapshotID, "snapshot:"))
	if !filepath.IsLocal(directoryRelative) {
		return "", fmt.Errorf("submission directory escapes root")
	}
	rootDirectory, err := os.OpenRoot(rootReal)
	if err != nil {
		return "", fmt.Errorf("open submission root: %w", err)
	}
	defer func() {
		if closeErr := rootDirectory.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close submission root: %w", closeErr)
		}
	}()
	current := ""
	for component := range strings.SplitSeq(directoryRelative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := rootDirectory.Lstat(current)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("submission directory contains a symlink")
			}
			if !info.IsDir() {
				return "", fmt.Errorf("submission directory component %s is not a directory", current)
			}
			continue
		}
		if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect submission directory: %w", statErr)
		}
		if err := rootDirectory.Mkdir(current, 0o755); err != nil {
			return "", fmt.Errorf("create submission directory: %w", err)
		}
	}
	directory, err := rootDirectory.OpenRoot(directoryRelative)
	if err != nil {
		return "", fmt.Errorf("open submission directory: %w", err)
	}
	defer func() {
		if closeErr := directory.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close submission directory: %w", closeErr)
		}
	}()
	encoded, err := json.Marshal(submission)
	if err != nil {
		return "", fmt.Errorf("encode agent bundle submission: %w", err)
	}
	encoded = append(encoded, '\n')
	fileName := strings.TrimPrefix(submission.ID, "submission:") + ".json"
	if info, statErr := directory.Lstat(fileName); statErr == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("submission path %s is not a regular file", filepath.Join(rootReal, directoryRelative, fileName))
		}
		existing, readErr := directory.ReadFile(fileName)
		if readErr != nil {
			return "", fmt.Errorf("read existing submission: %w", readErr)
		}
		if !bytes.Equal(existing, encoded) {
			return "", fmt.Errorf("submission path %s contains different content", filepath.Join(rootReal, directoryRelative, fileName))
		}
		return filepath.Join(rootReal, directoryRelative, fileName), nil
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect existing submission: %w", statErr)
	}
	var temporary *os.File
	var temporaryName string
	for range 10 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("create submission temporary name: %w", err)
		}
		temporaryName = ".submission-" + hex.EncodeToString(random[:]) + ".tmp"
		temporary, err = directory.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("create submission temporary file: %w", err)
		}
	}
	if temporary == nil {
		return "", fmt.Errorf("create submission temporary file: name collisions exhausted")
	}
	defer func() { _ = directory.Remove(temporaryName) }()
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("write submission: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("chmod submission: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close submission: %w", err)
	}
	if err := directory.Rename(temporaryName, fileName); err != nil {
		existing, readErr := directory.ReadFile(fileName)
		if readErr == nil && bytes.Equal(existing, encoded) {
			return filepath.Join(rootReal, directoryRelative, fileName), nil
		}
		return "", fmt.Errorf("publish submission: %w", err)
	}
	return filepath.Join(rootReal, directoryRelative, fileName), nil
}
