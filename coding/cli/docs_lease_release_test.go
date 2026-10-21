//go:build !pig_strip_docs

package cli

// releaseDocsLeases gives back the leases a test's startup took on its documentation bundle, so the test's temporary directory can be removed where an open
// lease file blocks that (Windows). A session never releases them.
func releaseDocsLeases() {
	docsLeases.Lock()
	releases := docsLeases.releases
	docsLeases.releases = nil
	docsLeases.Unlock()
	for _, release := range releases {
		release()
	}
}
