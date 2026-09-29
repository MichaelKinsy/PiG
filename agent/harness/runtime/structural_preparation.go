package runtime

// Ports packages/agent/src/harness/runtime/drive/structural.ts (durable preparation conversion).

import (
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// StructuralPreparation pairs a reserved task identity with its immutable preparation.
type StructuralPreparation struct {
	TaskID      string
	Preparation session.DurableStructuralPreparation
}

func durableFileOperations(operations compaction.FileOperations) session.DurableFileOperations {
	read, written, edited := operations.Paths()
	return session.DurableFileOperations{Read: read, Written: written, Edited: edited}
}

// DurableCompactionPreparation encodes compaction content for the operation preparation namespace.
func DurableCompactionPreparation(preparation compaction.CompactionPreparation) session.DurableStructuralPreparation {
	result := session.DurableStructuralPreparation{Kind: session.PreparationCompaction,
		MessagesToSummarize: preparation.MessagesToSummarize, TurnPrefixMessages: preparation.TurnPrefixMessages,
		RetainedTail: preparation.RetainedTail, IsSplitTurn: preparation.IsSplitTurn, TokensBefore: preparation.TokensBefore,
		FileOps: durableFileOperations(preparation.FileOps), Settings: preparation.Settings,
	}
	if preparation.PreviousSummaryPresent || preparation.PreviousSummary != "" {
		result.PreviousSummary = new(preparation.PreviousSummary)
	}
	return result
}

// DurableBranchPreparation encodes abandoned branch content for the operation preparation namespace.
func DurableBranchPreparation(preparation compaction.BranchPreparation) session.DurableStructuralPreparation {
	return session.DurableStructuralPreparation{Kind: session.PreparationBranchSummary, Messages: preparation.Messages, FileOps: durableFileOperations(preparation.FileOps), TotalTokens: preparation.TotalTokens}
}

func fileOperations(operations session.DurableFileOperations) compaction.FileOperations {
	result := compaction.CreateFileOps()
	for _, path := range operations.Read {
		result.AddRead(path)
	}
	for _, path := range operations.Written {
		result.AddWritten(path)
	}
	for _, path := range operations.Edited {
		result.AddEdited(path)
	}
	return result
}

func compactionPreparation(preparation session.DurableStructuralPreparation) compaction.CompactionPreparation {
	result := compaction.CompactionPreparation{MessagesToSummarize: preparation.MessagesToSummarize, TurnPrefixMessages: preparation.TurnPrefixMessages, RetainedTail: preparation.RetainedTail, IsSplitTurn: preparation.IsSplitTurn, TokensBefore: preparation.TokensBefore, FileOps: fileOperations(preparation.FileOps), Settings: preparation.Settings}
	if preparation.PreviousSummary != nil {
		result.PreviousSummary = *preparation.PreviousSummary
		result.PreviousSummaryPresent = true
	}
	return result
}

func branchPreparation(preparation session.DurableStructuralPreparation) compaction.BranchPreparation {
	return compaction.BranchPreparation{Messages: preparation.Messages, FileOps: fileOperations(preparation.FileOps), TotalTokens: preparation.TotalTokens}
}
