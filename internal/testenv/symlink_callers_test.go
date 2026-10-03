package testenv

import (
	"bytes"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// windowsTestFiles returns every _test.go file of this module that builds for Windows.
func windowsTestFiles(t *testing.T) (root string, files []string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s: %v", root, err)
	}
	windows := build.Default
	windows.GOOS = "windows"
	windows.GOARCH = "amd64"
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			switch entry.Name() {
			case ".git", ".upstream", "node_modules", "testdata", "target":
				return filepath.SkipDir
			}
			// A nested module cannot import this package.
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		match, err := windows.MatchFile(filepath.Dir(path), entry.Name())
		if err != nil || !match {
			return err
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, files
}

// Every test in this module that builds for Windows creates symbolic links
// through Symlink. It then skips, naming Developer Mode, only when Windows
// refuses the symlink privilege, and runs where the privilege is held. A
// direct os.Symlink call fails the test on such a host instead.
func TestWindowsTestsCreateSymlinksThroughSymlink(t *testing.T) {
	root, files := windowsTestFiles(t)
	call := []byte("os." + "Symlink(")
	var direct []string
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(source, call) {
			rel, _ := filepath.Rel(root, path)
			direct = append(direct, filepath.ToSlash(rel))
		}
	}
	if len(direct) > 0 {
		t.Fatalf("tests that build for Windows call os.Symlink directly; use testenv.Symlink:\n%s", strings.Join(direct, "\n"))
	}
}

// fileSymlinkCallers lists every function that may call Symlink, keyed by "module-relative file::function". Symlink skips on a Windows host without the symlink privilege, so a caller belongs here only when its behavior depends on a file symbolic link or on a link whose target is missing or cyclic, which a privilege-free junction cannot stand in for. A link to an existing directory uses RequireDirectoryLink and runs on stock Windows.
var fileSymlinkCallers = map[string]string{
	"cmd/pig/self_update_contract_test.go::TestStandaloneUpdateLocksThroughReceiptRollback":                                    "file link; the Windows branch returns first",
	"cmd/pig/self_update_npm_registry_test.go::isolatedNpmBin":                                                                 "file link",
	"cmd/pig/self_update_npm_registry_test.go::TestIsolatedNpmBinBypassesVersionManagerWrapper":                                "file link",
	"coding/extension/host/runtimecell/cache_identity_test.go::TestPublishedEntryRejectsForeignIdentity":                       "file link",
	"coding/mcpext/connection_test.go::TestMCPConnectionsExpandsTildeInTheCommandArgumentsAndCwdOfStdioServers":                "file link; the test skips on Windows",
	"coding/packagecontent/entry_shapes_test.go::TestExtensionDirectoryFollowsSymlinksAndIgnoreFiles":                          "file link",
	"coding/packagecontent/packagecontent_test.go::TestValidateRejectsSymlinkedManifest":                                       "file link",
	"durable/env/node/filesystem_test.go::TestFilesystemListsSymlinksAsSymlinks":                                               "file link",
	"durable/env/node/filesystem_test.go::TestFilesystemReturnsFileInfoForFilesDirectoriesAndSymlinksWithoutFollowingSymlinks": "file link; the directory case uses RequireDirectoryLink",
	"durable/tools/tools_test.go::TestEditEditsRegularFilesThroughSymlinks":                                                    "file link",
	"durable/tools/tools_test.go::TestEditSerializesConcurrentEditsThroughCanonicalAndSymlinkPaths":                            "file link",
	"coding/pigletbuild/records_test.go::TestHashTreeExcludesVirtualenvArtifacts":                                              "file link",
	"coding/pigletbuild/records_test.go::TestHashTreeIgnoresMarkdownSymlinksAndRejectsSourceSymlinks":                          "file link",
	"coding/piglet/release/store_security_test.go::TestInventoryRejectsManagedSymlinkAncestors":                                "file link; the directory case uses RequireDirectoryLink",
	"internal/codingagent/resources_test.go::TestDedupFallbackOnBrokenSymlink":                                                 "link to a missing target",
	"internal/codingagent/resources_test.go::TestDedupSymlinkLoopGuarded":                                                      "cyclic links",
	"internal/codingagent/resources_test.go::TestResourceSymlinkDedup":                                                         "file link",
	"internal/codingagent/selfupdate_tier_test.go::TestAC2ResolveSelfUpdateTier":                                               "file link",
	"internal/codingagent/selfupdate_tier_test.go::TestResolveSelfUpdateTier_SymlinkedBinaryResolvesToTarget":                  "file link",
	"internal/codingagent/session_continue_test.go::TestListSessionsAcrossRootIgnoresSymlinkToFile":                            "file link",
	"internal/codingagent/skill_collision_test.go::TestDeduplicateSkillsCollisionDiagnostics":                                  "file link",
	"internal/codingagent/tools/find_upstream_test.go::TestSearchToolsResolveAgentBinAtCallTime":                               "file link",
	"internal/codingagent/tools/mutation_queue_fifo_test.go::TestFileMutationQueue_PropagatesNonMissingPathError":              "cyclic links",
	"internal/codingagent/tools/mutation_queue_upstream_test.go::testMutationOrder":                                            "file link",
	"internal/codingagent/tools/zz_review_fifo_test.go::TestReviewWriteQueueErrorVisible":                                      "cyclic links",
	"internal/pilock/legacy_test.go::TestLegacyReplacementRefusesChangedIdentity":                                              "file link",
	"internal/pilock/legacy_test.go::TestLegacyUpgradePreservesOtherPaths":                                                     "file link; the directory case uses RequireDirectoryLink",
	"internal/pilock/link_test.go::linkLockPath":                                                                               "file link; the directory cases use RequireDirectoryLink",
	"test/parity/cmd/closure/main_test.go::TestCommittedBundlePathsIgnoreUntrackedAndRejectDirty":                              "file link",
	"test/parity/cmd/interfaceinventory/main_test.go::TestMappingReferencesRejectEscapesAndMissingFragments":                   "file link",
}

// Windows-building tests keep Symlink for file links only, so the directory-link behavior they exercise runs on a stock Windows host.
func TestSymlinkCallersAreFileOrUnresolvableLinks(t *testing.T) {
	root, files := windowsTestFiles(t)
	found := map[string]bool{}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// The package's own tests call Symlink unqualified; other files may rename or dot-import it.
		local := ""
		if parsed.Name.Name == "testenv" {
			local = "."
		}
		for _, spec := range parsed.Imports {
			if spec.Path.Value != `"github.com/MichaelKinsy/PiG/internal/testenv"` {
				continue
			}
			local = "testenv"
			if spec.Name != nil {
				local = spec.Name.Name
			}
		}
		if local == "" || local == "_" {
			continue
		}
		callsSymlink := func(call *ast.CallExpr) bool {
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				return local == "." && fun.Name == "Symlink"
			case *ast.SelectorExpr:
				pkg, ok := fun.X.(*ast.Ident)
				return ok && local != "." && pkg.Name == local && fun.Sel.Name == "Symlink"
			}
			return false
		}
		for _, declaration := range parsed.Decls {
			// A call in a package-level function literal belongs to no function name, so it is keyed by the file alone.
			caller := "<package scope>"
			if function, ok := declaration.(*ast.FuncDecl); ok {
				caller = function.Name.Name
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && callsSymlink(call) {
					found[rel+"::"+caller] = true
				}
				return true
			})
		}
	}
	for caller := range found {
		if _, ok := fileSymlinkCallers[caller]; !ok {
			t.Errorf("%s calls testenv.Symlink; use testenv.RequireDirectoryLink for a directory link, or list the caller in fileSymlinkCallers with the reason a file link is required", caller)
		}
	}
	for caller := range fileSymlinkCallers {
		if !found[caller] {
			t.Errorf("fileSymlinkCallers lists %s, which no longer calls testenv.Symlink", caller)
		}
	}
}
