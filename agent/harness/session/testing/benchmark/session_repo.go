package benchmark

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/agent/harness/session/testing/conformance"
	"github.com/MichaelKinsy/PiG/ai"
)

// SessionRepoCatalogBenchmarkDataset is one closed-session catalog size.
type SessionRepoCatalogBenchmarkDataset struct {
	Name         string
	SessionCount int
}

// SessionRepoCatalogReadBenchmarkScenario is a catalog read; Run returns a
// count so each result is consumed.
type SessionRepoCatalogReadBenchmarkScenario struct {
	Name           string
	ExpectedResult func(dataset SessionRepoCatalogBenchmarkDataset) int
	Run            func(ctx context.Context, repo conformance.RepoAdapter) (int, error)
}

// SessionRepoCatalogWriteBenchmarkScenario prepares an operation against an
// independently prepared repository.
type SessionRepoCatalogWriteBenchmarkScenario struct {
	Name           string
	ExpectedResult int
	Prepare        func(ctx context.Context, repo conformance.RepoAdapter) (run func() (int, error), err error)
}

// SessionRepoForkWriteBenchmarkScenario forks a seeded source session.
type SessionRepoForkWriteBenchmarkScenario struct {
	Name           string
	ExpectedResult func(dataset StorageBenchmarkDataset) int
	Run            func(ctx context.Context, repo conformance.RepoAdapter, source session.SessionMetadata, dataset StorageBenchmarkDataset) (int, error)
}

// SessionRepoBenchmarkSessionID is the deterministic session id shared by
// repository benchmark workloads.
func SessionRepoBenchmarkSessionID(index int) string {
	return fmt.Sprintf("benchmark-session-%08d", index)
}

var (
	benchmarkSessionID       = SessionRepoBenchmarkSessionID(0)
	forkDestinationSessionID = SessionRepoBenchmarkSessionID(1)
)

func createCatalogDataset(scale string, sessionCount int) SessionRepoCatalogBenchmarkDataset {
	return SessionRepoCatalogBenchmarkDataset{Name: "synthetic catalog: " + scale + " closed sessions", SessionCount: sessionCount}
}

// SessionRepoCatalogBenchmarkDatasets are the deterministic closed-session
// catalogs shared by repository measurements.
var SessionRepoCatalogBenchmarkDatasets = []SessionRepoCatalogBenchmarkDataset{
	createCatalogDataset("100", 100),
	createCatalogDataset("1k", 1_000),
	createCatalogDataset("10k", 10_000),
}

// SeedSessionRepoCatalogBenchmark seeds one catalog and returns its metadata
// in creation order.
func SeedSessionRepoCatalogBenchmark(ctx context.Context, repo conformance.RepoAdapter, dataset SessionRepoCatalogBenchmarkDataset) ([]session.SessionMetadata, error) {
	metadata := make([]session.SessionMetadata, 0, dataset.SessionCount)
	for index := range dataset.SessionCount {
		created, err := repo.Create(ctx, session.SessionCreateOptions{ID: SessionRepoBenchmarkSessionID(index)})
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, created.Metadata())
		if err := created.Close(ctx); err != nil {
			return nil, err
		}
	}
	return metadata, nil
}

// SessionRepoCatalogReadBenchmarkScenarios are the shared catalog reads.
var SessionRepoCatalogReadBenchmarkScenarios = []SessionRepoCatalogReadBenchmarkScenario{
	{
		Name:           "list sessions",
		ExpectedResult: func(dataset SessionRepoCatalogBenchmarkDataset) int { return dataset.SessionCount },
		Run: func(ctx context.Context, repo conformance.RepoAdapter) (int, error) {
			listed, err := repo.List(ctx)
			return len(listed), err
		},
	},
}

func createClosedSession(ctx context.Context, repo conformance.RepoAdapter) (session.SessionMetadata, error) {
	created, err := repo.Create(ctx, session.SessionCreateOptions{ID: benchmarkSessionID})
	if err != nil {
		return session.SessionMetadata{}, err
	}
	return created.Metadata(), created.Close(ctx)
}

func matched(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

// SessionRepoCatalogWriteBenchmarkScenarios are the shared catalog writes.
var SessionRepoCatalogWriteBenchmarkScenarios = []SessionRepoCatalogWriteBenchmarkScenario{
	{
		Name: "create empty session", ExpectedResult: 1,
		Prepare: func(ctx context.Context, repo conformance.RepoAdapter) (func() (int, error), error) {
			return func() (int, error) {
				created, err := repo.Create(ctx, session.SessionCreateOptions{ID: benchmarkSessionID})
				if err != nil {
					return 0, err
				}
				return matched(created.Metadata().ID == benchmarkSessionID), nil
			}, nil
		},
	},
	{
		Name: "open closed empty session", ExpectedResult: 1,
		Prepare: func(ctx context.Context, repo conformance.RepoAdapter) (func() (int, error), error) {
			metadata, err := createClosedSession(ctx, repo)
			return func() (int, error) {
				reopened, err := repo.Open(ctx, metadata)
				if err != nil {
					return 0, err
				}
				return matched(reopened.Metadata().ID == metadata.ID), nil
			}, err
		},
	},
	{
		Name: "delete closed empty session", ExpectedResult: 1,
		Prepare: func(ctx context.Context, repo conformance.RepoAdapter) (func() (int, error), error) {
			metadata, err := createClosedSession(ctx, repo)
			return func() (int, error) {
				return 1, repo.Delete(ctx, metadata)
			}, err
		},
	},
}

// SessionRepoForkBenchmarkDatasets bound initial fork timing because each
// iteration owns an equivalent seeded repository.
var SessionRepoForkBenchmarkDatasets = []StorageBenchmarkDataset{StorageBenchmarkDatasets[0], StorageBenchmarkDatasets[1]}

// SeedSessionRepoForkBenchmark seeds one open source session with a linear
// configured main branch.
func SeedSessionRepoForkBenchmark(ctx context.Context, repo conformance.RepoAdapter, dataset StorageBenchmarkDataset) (session.SessionMetadata, error) {
	created, err := repo.Create(ctx, session.SessionCreateOptions{ID: benchmarkSessionID})
	if err != nil {
		return session.SessionMetadata{}, err
	}
	transactions := GenerateStorageBenchmarkSeedTransactions(dataset)
	transactions = append(transactions, []session.Write{
		session.SetValue(session.BranchTip("main"), new(dataset.TipID)),
		session.SetValue(session.LaneConfig("main"), session.LaneConfiguration{
			Model: session.ModelRef{Provider: "benchmark", ModelID: "benchmark"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{},
		}),
		session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{}}),
	})
	for _, transaction := range transactions {
		if _, err := created.Mutate(ctx, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
			return mutator.Commit(ctx, transaction)
		}); err != nil {
			return session.SessionMetadata{}, err
		}
	}
	return created.Metadata(), nil
}

// SessionRepoForkWriteBenchmarkScenarios are the shared fork writes.
var SessionRepoForkWriteBenchmarkScenarios = []SessionRepoForkWriteBenchmarkScenario{
	{
		Name:           "fork open current branch",
		ExpectedResult: func(dataset StorageBenchmarkDataset) int { return dataset.EntryCount },
		Run: func(ctx context.Context, repo conformance.RepoAdapter, source session.SessionMetadata, dataset StorageBenchmarkDataset) (int, error) {
			fork, err := repo.Fork(ctx, source, session.ForkOptions{ID: forkDestinationSessionID, Scope: session.ForkScopeBranch, Branch: "main"})
			if err != nil {
				return 0, err
			}
			metadata := fork.Metadata()
			if metadata.ID == forkDestinationSessionID && metadata.ParentSessionID == source.ID {
				return dataset.EntryCount, nil
			}
			return 0, nil
		},
	},
}
