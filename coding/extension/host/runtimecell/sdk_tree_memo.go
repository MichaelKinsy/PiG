package runtimecell

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The cache key of a packed cell covers every file of its SDK tree (hashTree), and a load looks for its built cell under that key. For an SDK
// that this host staged itself the tree does not change between loads, yet every load of a process read and hashed it again: 212 files and 2.1 MB for
// the Go SDK of an installed pig. sdkTreeHash answers such a lookup from a memo of the process, after checking that nothing it covers changed.
//
// What the check is. A memo holds the hash of one physical root and, for each file the hash covered, in walk order, its path, size and modification
// time in nanoseconds. A lookup walks the root again and lstats every file, which reads no file, and answers from the memo only when the walk finds
// the same files in the same order, each with the size and time it had. So:
//
//   - a file edited to another size, or to the same size with another time, a file added, removed, renamed or turned into a link, and a directory
//     that now holds another set of files, give a hash read again from the files;
//   - a file that is not covered (a hidden directory, node_modules, target, vendor, dist, build, __pycache__, a name ending .tmp, ~, .pyc or .pyo, a
//     link) is not looked at, as hashTree does not look at it: changes to it change no hash, before or after;
//   - a change of mode alone, or of a directory's own time, changes no hash, as hashTree does not hash them.
//
// What it cannot see: an edit that keeps both the size and the modification time of a file that was already older than racyTreeWindow when the memo
// read it. A tool that restores times (cp -p, rsync -t, touch -d) over a file of the same length does that. The staged SDK is written only by
// this host's staging, which replaces the whole directory with files of the time of the write; the same check, on the same two properties, already
// serves the SDK's source digest (subprocess.sdkSourceDigest).
//
// The racy case. A file written within racyTreeWindow of the moment the memo began to read the tree is not yet told apart from a later edit by its
// time: a file system that rounds times to a second, or to the two seconds of FAT, gives an edit made right after the read the same time, and with
// the same size nothing shows it. Git has the problem with its index and answers it by not trusting the entries that are not older than the
// index. The memo does the same: a tree with such a file, or with one dated in the future of the clock (a clock stepped back, or another machine's
// clock on a network file system), is hashed and not memoized, so every lookup reads it again until its files are older than the window. A tree
// freshly staged is read in full by the loads of its first seconds and then is memoized.
//
// Which roots. Only a root the host stages itself is memoized (stagedSDKTree): a directory directly inside <config root>/state/pigsdk. Every other
// root, which a developer edits (the repository's extensions/sdk found from the working directory, a PIG_SDK_GO_ROOT, an extension's replace), is
// hashed by content on every lookup, as before.
//
// The memo changes only which lookup reads the files. The hash is the one hashTree computes, so a cache entry keeps its name, and a build does not
// use the memo: it hashes the tree under its build lock and again when the compiler returns (goPackedCellHash), so an image is always published
// under the key of the content it was built from, and the worst a wrong answer here can do is not find an entry.

// racyTreeWindow is how recent a file's modification time may be, before the memo reads the tree, for the file not to be trusted yet. It is the
// coarsest time stamp in use, FAT's two seconds: the clocks of the file systems that keep nanoseconds are a tick behind the clock of the process
// and so are covered as well.
// pig additive (D20): the shared cell cache and its keys have no upstream equivalent.
const racyTreeWindow = 2 * time.Second

// treeMemoClock is the clock the racy rule reads; tests replace it to place a file's time exactly at the edge of the window, or in the future of the
// clock.
var treeMemoClock = time.Now

// maxMemoizedTrees bounds the memo. A process stages the SDKs of one config root (a language each); more roots than this are tests or a host
// serving several config roots, and the memo starts again rather than grow.
// pig additive (D20): the shared cell cache and its keys have no upstream equivalent.
const maxMemoizedTrees = 32

// treeStamp is what an lstat says of one file a tree hash covers.
type treeStamp struct {
	path  string
	size  int64
	mtime int64 // nanoseconds since the Unix epoch
}

func stampOf(path string, info fs.FileInfo) treeStamp {
	return treeStamp{path: path, size: info.Size(), mtime: info.ModTime().UnixNano()}
}

// memoizedTree is the hash of a tree and the files it covered. It is never changed once published.
type memoizedTree struct {
	hash   string
	stamps []treeStamp
}

// memoizedTrees holds the memo of the process by physical root. Its lock guards the map and nothing else: no file is read or listed while it is
// held, so a lookup of one root never waits for the reading of another.
var memoizedTrees struct {
	mu     sync.Mutex
	byRoot map[string]*memoizedTree
}

// sdkTreeHash is hashTree(root) for the lookup of a built cell, which may answer from the process's memo. See the comment at the top of the file
// for what the memo checks and where it is used.
func sdkTreeHash(root string) string {
	abs, err := resolveTreeRoot(root)
	if err != nil {
		return "error:" + err.Error()
	}
	if !stagedSDKTree(abs) {
		return hashPhysicalTree(abs, nil)
	}
	memoizedTrees.mu.Lock()
	memo := memoizedTrees.byRoot[abs]
	memoizedTrees.mu.Unlock()
	if memo != nil {
		if treeStampsMatch(abs, memo.stamps) {
			return memo.hash
		}
		forgetTree(abs, memo)
	}
	return hashAndMemoizeTree(abs)
}

// stagedSDKTree reports whether abs, a physical path, is a directory the host stages an SDK in: directly inside a staging area,
// <config root>/state/pigsdk, as RetiredStagedSDK judges. A link in the path is resolved first, so a root that reaches the area only through one is
// not staged here and is hashed by content.
func stagedSDKTree(abs string) bool {
	return isSDKStagingArea(filepath.Dir(abs))
}

// hashAndMemoizeTree reads the tree at abs and memoizes its hash when no file of it is racy.
func hashAndMemoizeTree(abs string) string {
	// The clock is read before the first file: a file is trusted when it is older than the window at this moment.
	began := treeMemoClock()
	var stamps []treeStamp
	hash := hashPhysicalTree(abs, &stamps)
	trusted := len(stamps) > 0 && !strings.HasPrefix(hash, "error:") && !anyRacy(stamps, began)
	memoizedTrees.mu.Lock()
	defer memoizedTrees.mu.Unlock()
	if !trusted {
		delete(memoizedTrees.byRoot, abs)
		return hash
	}
	if _, known := memoizedTrees.byRoot[abs]; memoizedTrees.byRoot == nil || (!known && len(memoizedTrees.byRoot) >= maxMemoizedTrees) {
		memoizedTrees.byRoot = make(map[string]*memoizedTree, 4)
	}
	memoizedTrees.byRoot[abs] = &memoizedTree{hash: hash, stamps: stamps}
	return hash
}

// anyRacy reports whether a file of stamps was modified at or after the start of the window that ends at began, which includes a time later than
// began.
func anyRacy(stamps []treeStamp, began time.Time) bool {
	limit := began.Add(-racyTreeWindow).UnixNano()
	for _, stamp := range stamps {
		if stamp.mtime >= limit {
			return true
		}
	}
	return false
}

// forgetTree drops memo for abs unless another lookup has replaced it already.
func forgetTree(abs string, memo *memoizedTree) {
	memoizedTrees.mu.Lock()
	defer memoizedTrees.mu.Unlock()
	if memoizedTrees.byRoot[abs] == memo {
		delete(memoizedTrees.byRoot, abs)
	}
}

var errTreeChanged = errors.New("tree changed")

// treeStampsMatch reports whether the files a tree hash covers under abs are the stamped files, in order, each with the size and modification time
// it was stamped with. It stops at the first difference, and reads no file.
func treeStampsMatch(abs string, stamps []treeStamp) bool {
	matched := 0
	err := walkHashedFiles(abs, func(path string, info fs.FileInfo) error {
		if matched == len(stamps) || stamps[matched] != stampOf(path, info) {
			return errTreeChanged
		}
		matched++
		return nil
	})
	return err == nil && matched == len(stamps)
}
