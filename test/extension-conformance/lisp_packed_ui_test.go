package extensionconformance

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Lisp SDK's packed placement of the UI rows: the SDK harness fixture (testdata/lisp-sdk-fixture) beside a peer extension in one shared cell, as
// createPackedUIConfigs builds it for the other SDKs.
func init() {
	packedUIConfigs["lisp"] = sync.OnceValues(createPackedLispUIConfigs)
}

func createPackedLispUIConfigs() ([]subprocess.ExtConfig, error) {
	peer := filepath.Join(fixtureRoot, "packed-ui-lisp", "ui-peer")
	if err := os.MkdirAll(peer, 0o700); err != nil {
		return nil, err
	}
	body := "(defpackage \"ui-peer\" (:use \"COMMON-LISP\"))\n(in-package \"ui-peer\")\n(defun new-extension () (pig:make-extension \"ui-peer\"))\n"
	if err := os.WriteFile(filepath.Join(peer, "ui-peer.lisp"), []byte(body), 0o600); err != nil {
		return nil, err
	}
	configs := []subprocess.ExtConfig{
		{Name: lispFixtureName, Source: filepath.Join(fixtureSourceRoot, "test", "extension-conformance", "testdata", "lisp-sdk-fixture"), Package: "lisp-sdk-fixture"},
		{Name: "ui-peer", Source: peer, Package: "ui-peer"},
	}
	for i := range configs {
		cfg := &configs[i]
		cfg.Enabled, cfg.RuntimeKind, cfg.RuntimeLanguage, cfg.Isolation, cfg.EntrypointKind = true, "subprocess", "lisp", "shared-ok", "factory"
		cfg.Factory, cfg.SDKName, cfg.ContentHash = "new-extension", "pig-sdk-lisp", "packed-ui-lisp-"+cfg.Name
	}
	return configs, nil
}
