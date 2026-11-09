package storage_test

// pi: packages/durable/src/storage/sqlite/storage.ts (via node.ts, Node's built-in node:sqlite)

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// oracleSegment is a run of consecutive commits, or of consecutive reads, of one program.
type oracleSegment struct {
	commits bool
	first   int
	steps   []oracleStep
}

func oracleSegments(program oracleProgram) []oracleSegment {
	var segments []oracleSegment
	for index, step := range program.Steps {
		commits := step.Op == "commit"
		if last := len(segments) - 1; last >= 0 && segments[last].commits == commits {
			segments[last].steps = append(segments[last].steps, step)
			continue
		}
		segments = append(segments, oracleSegment{commits: commits, first: index, steps: []oracleStep{step}})
	}
	return segments
}

// oracleNext is the continuation a scan's answer carries.
func oracleNext(answer oracleResult) json.RawMessage {
	var page map[string]json.RawMessage
	if answer.Err != nil || answer.Undef || json.Unmarshal(answer.OK, &page) != nil {
		return nil
	}
	return page["next"]
}

// TestSqliteReadCacheMatchesPiWhileOtherWritersCommit reads one database file through a cached PiG storage that stays open
// while Pi (Node's node:sqlite), a second PiG storage and the cached storage itself take turns committing to it, in
// random order, between runs of reads. After every run the cached storage must answer what Pi answers from the same file,
// and what an uncached PiG storage answers, exactly.
func TestSqliteReadCacheMatchesPiWhileOtherWritersCommit(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd":
	default:
		t.Skip("no read cache on " + runtime.GOOS)
	}
	count := 20
	if testing.Short() {
		count = 6
	}
	base := t.TempDir()
	programs := make([]oracleProgram, count)
	dirs := make([]string, count)
	cached := make([]*sqlite.SqliteStorage, count)
	uncached := make([]*sqlite.SqliteStorage, count)
	other := make([]*sqlite.SqliteStorage, count)
	segments := make([][]oracleSegment, count)
	answers := make([]map[int]oracleResult, count)
	random := rand.New(rand.NewPCG(2026, 11))
	waves := 0
	for i := range count {
		programs[i] = oracleProgramFor(int64(31000+i), false)
		for k, step := range programs[i].Steps {
			// A minted ID is a counter of its own storage, not something stored in the file: another connection mints its own.
			if step.Op == "mintId" {
				programs[i].Steps[k].Op = "conversation"
			}
		}
		segments[i] = oracleSegments(programs[i])
		waves = max(waves, len(segments[i]))
		dirs[i] = filepath.Join(base, fmt.Sprint("program-", i))
		if err := os.MkdirAll(dirs[i], 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dirs[i], "store.db")
		var err error
		if cached[i], err = sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{}); err != nil {
			t.Fatal(err)
		}
		off := new(int64(0))
		if uncached[i], err = sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{ReadCacheBytes: off}); err != nil {
			t.Fatal(err)
		}
		if other[i], err = sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{ReadCacheBytes: off}); err != nil {
			t.Fatal(err)
		}
		answers[i] = map[int]oracleResult{}
	}
	defer func() {
		for i := range count {
			for _, storage := range []*sqlite.SqliteStorage{cached[i], uncached[i], other[i]} {
				_ = storage.Close(t.Context())
			}
		}
	}()

	ops, failures := map[string]int{}, 0
	var cachedVsUncached, cachedVsPi, uncachedVsPi, writers [3]int
	for wave := range waves {
		type job struct {
			program int
			segment oracleSegment
			steps   oracleProgram
		}
		var jobs []job
		for i := range count {
			if wave >= len(segments[i]) {
				continue
			}
			segment := segments[i][wave]
			steps := make([]oracleStep, len(segment.steps))
			copy(steps, segment.steps)
			for k := range steps {
				// A scan continues from the answer of an earlier step, which another run of the program may have asked.
				if from := steps[k].CursorFrom; from != nil {
					steps[k].CursorFrom, steps[k].Cursor = nil, nil
					if earlier, ok := answers[i][*from]; ok {
						steps[k].Cursor = oracleNext(earlier)
					}
				}
			}
			jobs = append(jobs, job{program: i, segment: segment, steps: oracleProgram{Steps: steps}})
		}
		// Commits: each run goes to Pi, to the second PiG storage or to the cached one.
		var piWrites []job
		for _, current := range jobs {
			if !current.segment.commits {
				continue
			}
			writer := random.IntN(3)
			writers[writer]++
			switch writer {
			case 0:
				piWrites = append(piWrites, current)
			case 1:
				runPigProgram(t, current.steps, other[current.program], false)
			default:
				runPigProgram(t, current.steps, cached[current.program], false)
			}
		}
		if len(piWrites) > 0 {
			programsOf, dirsOf := make([]oracleProgram, len(piWrites)), make([]string, len(piWrites))
			for k, current := range piWrites {
				programsOf[k], dirsOf[k] = current.steps, dirs[current.program]
			}
			results := runPiJSONL(t, "sqlite-write", programsOf, dirsOf)
			for k, current := range piWrites {
				for _, result := range results[k] {
					if result.Err != nil && result.Index < 0 {
						t.Fatalf("program %d: Pi could not open the database: %v", current.program, result.Err)
					}
				}
			}
		}
		// Reads: the cached storage answers twice, the second time from memory; the rest answer once.
		var reads []job
		for _, current := range jobs {
			if !current.segment.commits {
				reads = append(reads, current)
			}
		}
		if len(reads) == 0 {
			continue
		}
		programsOf, dirsOf := make([]oracleProgram, len(reads)), make([]string, len(reads))
		for k, current := range reads {
			programsOf[k], dirsOf[k] = current.steps, dirs[current.program]
		}
		pi := runPiJSONL(t, "sqlite-read", programsOf, dirsOf)
		for k, current := range reads {
			label := fmt.Sprintf("program %d wave %d", current.program, wave)
			first := runPigProgram(t, current.steps, cached[current.program], false)
			second := runPigProgram(t, current.steps, cached[current.program], false)
			reference := runPigProgram(t, current.steps, uncached[current.program], false)
			cachedVsUncached[0] += compareOracleResults(t, label+": cached (first) vs uncached", current.steps, first, reference, cachedVsUncached[0], ops, &failures)
			cachedVsUncached[1] += compareOracleResults(t, label+": cached (second) vs uncached", current.steps, second, reference, cachedVsUncached[1], ops, &failures)
			cachedVsPi[0] += compareOracleResults(t, label+": cached (first) vs Pi", current.steps, first, pi[k], cachedVsPi[0], ops, &failures)
			cachedVsPi[1] += compareOracleResults(t, label+": cached (second) vs Pi", current.steps, second, pi[k], cachedVsPi[1], ops, &failures)
			uncachedVsPi[0] += compareOracleResults(t, label+": uncached vs Pi", current.steps, reference, pi[k], uncachedVsPi[0], ops, &failures)
			for offset, answer := range second {
				answers[current.program][current.segment.first+offset] = answer
			}
		}
	}
	if cachedVsUncached[0] != 0 || cachedVsUncached[1] != 0 {
		t.Errorf("the cached storage answered differently from the uncached one %v times", cachedVsUncached)
	}
	// The generator's own differences from Pi (member order of some records) are not the cache's.
	if cachedVsPi[0] > uncachedVsPi[0] || cachedVsPi[1] > uncachedVsPi[0] {
		t.Errorf("the cached storage differs from Pi %v times, the uncached one %d", cachedVsPi, uncachedVsPi[0])
	}
	var hits, resets, adoptions uint64
	for _, storage := range cached {
		stats := storage.ReadCacheStats()
		hits, resets, adoptions = hits+stats.Hits, resets+stats.Resets, adoptions+stats.Adoptions
		if stats.Disabled {
			t.Errorf("a cache stopped: %+v", stats)
		}
	}
	if hits == 0 || resets == 0 || adoptions == 0 || writers[0] == 0 || writers[1] == 0 || writers[2] == 0 {
		t.Errorf("the run did not exercise the cache against every writer: %d hits, %d resets, %d adoptions, writers %v", hits, resets, adoptions, writers)
	}
	if ops["entry"] == 0 || ops["scanEntries"] < 10 || ops["findDocument"] == 0 {
		t.Errorf("the reads were one-sided: %v", ops)
	}
}
