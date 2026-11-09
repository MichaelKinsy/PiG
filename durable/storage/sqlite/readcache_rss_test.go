package sqlite_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

const residentChildEnv = "PIG_READCACHE_RSS_CHILD"

// peakResident is the process's peak resident set in kilobytes (Linux: VmHWM).
func peakResident() (int64, bool) {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if fields := strings.Fields(line); len(fields) == 3 && fields[0] == "VmHWM:" {
			kilobytes, err := strconv.ParseInt(fields[1], 10, 64)
			return kilobytes, err == nil
		}
	}
	return 0, false
}

// residentChild fills a store with about 8 MiB of entries, reads far more distinct pages of it than the budget holds, and
// prints the peak resident set and what the cache holds.
func residentChild(mode string) {
	options := node.NodeSqliteStorageOptions{}
	if mode == "uncached" {
		options.ReadCacheBytes = new(int64(0))
	}
	path := os.Getenv(residentChildEnv + "_PATH")
	storage, err := node.OpenNodeSqliteStorage(path, options)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	runtime.GC()
	before, _ := peakResident()
	rng := rand.New(rand.NewPCG(1, 2))
	for pass := range 3 {
		for range 1500 {
			low := durable.EntryId(1000 + rng.IntN(3800))
			query := durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, MinEntryId: &low}
			if _, err := storage.ScanEntries(testContext, query, 10+rng.IntN(40), nil); err != nil {
				fmt.Fprintln(os.Stderr, pass, err)
				os.Exit(2)
			}
		}
	}
	after, _ := peakResident()
	stats := storage.ReadCacheStats()
	fmt.Printf("resident %d %d %d %d\n", before, after, stats.Bytes, stats.Items)
}

// The resident set a full cache adds stays within its budget: the same reads of the same store, with the cache and
// without it, in two processes.
func TestReadCacheResidentSetStaysNearItsBudget(t *testing.T) {
	if mode := os.Getenv(residentChildEnv); mode != "" {
		residentChild(mode)
		return
	}
	if raceDetector {
		t.Skip("the race detector's shadow memory is not the program's")
	}
	if _, ok := peakResident(); !ok {
		t.Skip("no /proc/self/status")
	}
	storage, path := createSqliteStorage(t, uncachedOptions())
	must(durabletest.CommitWrites(testContext, storage, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}))
	for start := 1000; start < 5000; start += 100 {
		var writes []durable.StorageWrite
		for id := start; id < start+100; id++ {
			writes = append(writes, durable.EntryWrite{Value: durable.EntryRecord{
				Id: durable.EntryId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m",
				Data: map[string]any{"id": float64(id), "text": strings.Repeat("x", 1500)},
			}})
		}
		must(durabletest.CommitWrites(testContext, storage, writes))
	}
	mustDo(t, storage.Close(testContext))
	measure := func(mode string) (before, after, bytes, items int64) {
		command := exec.Command(os.Args[0], "-test.run=^TestReadCacheResidentSetStaysNearItsBudget$")
		command.Env = append(os.Environ(), residentChildEnv+"="+mode, residentChildEnv+"_PATH="+path)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("%s: %v\n%s", mode, err, output)
		}
		for line := range strings.SplitSeq(string(output), "\n") {
			if fields := strings.Fields(line); len(fields) == 5 && fields[0] == "resident" {
				numbers := make([]int64, 4)
				for index := range numbers {
					numbers[index], _ = strconv.ParseInt(fields[index+1], 10, 64)
				}
				return numbers[0], numbers[1], numbers[2], numbers[3]
			}
		}
		t.Fatalf("%s: no result in %q", mode, output)
		return
	}
	_, withoutAfter, _, _ := measure("uncached")
	_, withAfter, bytes, items := measure("cached")
	grown := (withAfter - withoutAfter) << 10
	t.Logf("peak resident: %d KiB without the cache, %d KiB with it; the cache holds %d items, estimated %d bytes", withoutAfter, withAfter, items, bytes)
	if bytes > sqlite.DefaultReadCacheBytes || items == 0 {
		t.Fatalf("the cache holds %d bytes in %d items of a %d byte budget", bytes, items, sqlite.DefaultReadCacheBytes)
	}
	// The collector lets a heap grow to twice its live data before it runs, so a full cache can add about twice its
	// budget to the peak resident set.
	if grown > 3*sqlite.DefaultReadCacheBytes {
		t.Fatalf("the cache added %d bytes to the peak resident set; its budget is %d", grown, sqlite.DefaultReadCacheBytes)
	}
}
