package pigletbuild

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

func TestPublishGitHubNamedNamespace(t *testing.T) {
	gh, _ := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, _ := writePublishKey(t)
	builder := newSigningFakeBuilder(t)
	args := []string{source, "--to", "github", "--repo", "acme/piglets", "--sign-key", keyPath, "--tag-prefix", "porter/"}
	code, out, stderr := runPublishForTest(t, builder.builders, args...)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{"tag porter/v1.2.3", "Source: git:github.com/acme/piglets@porter/v1.2.3", "pull github:acme/piglets/porter@1.2.3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if calls := gh.calls(); len(calls) != 1 || calls[0].Args[2] != "porter/v1.2.3" || builder.buildCalls != 0 {
		t.Fatalf("calls=%v builds=%d", calls, builder.buildCalls)
	}
	gh.setExists()
	code, _, stderr = runPublishForTest(t, builder.builders, append(args, "--yes")...)
	if code != 1 || !strings.Contains(stderr, "porter/v1.2.3 already exists") || builder.buildCalls != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

// The fake gh persists exactly the publisher's uploads. The HTTPS server serves
// them with GitHub's escaped-tag URL and release-list API shapes.
func TestMonorepoPublishPullUpdateIsolation(t *testing.T) {
	gh, _ := publishTestEnv(t)
	target := "linux/amd64"
	var mu sync.Mutex
	var tags []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/repos/acme/piglets/releases" {
			if r.URL.Query().Get("page") != "1" {
				t.Errorf("unexpected API page %s", r.URL)
			}
			rows := make([]map[string]any, 0, len(tags))
			for _, tag := range tags {
				rows = append(rows, map[string]any{"tag_name": tag, "draft": false, "prerelease": false})
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		const prefix = "/acme/piglets/releases/download/"
		path, ok := strings.CutPrefix(r.URL.EscapedPath(), prefix)
		tag, asset, found := strings.Cut(path, "/")
		if !ok || !found || !strings.Contains(tag, "%2F") || strings.Contains(asset, "/") {
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(gh.state, "releases", tag, asset))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Transport = githubTestTransport{endpoint: endpoint, transport: client.Transport}
	keys := map[string]string{}
	ids := map[string]string{}
	publish := func(name, version, keyPath string) {
		t.Helper()
		source := writePublishPiglet(t, fmt.Sprintf("name: %s\nrelease: {version: %s}\n", name, version))
		builder := newSigningFakeBuilder(t)
		code, out, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/piglets", "--sign-key", keyPath, "--tag-prefix", name+"/", "--targets", target, "--yes")
		if code != 0 {
			t.Fatalf("publish %s %s: %d %s %s", name, version, code, out, stderr)
		}
		if !strings.Contains(out, "/releases/tag/"+name+"%2Fv"+version) {
			t.Fatalf("unescaped published URL: %s", out)
		}
		verified, err := pigletrelease.Verify(gh.assets()["piglet-release.json"])
		if err != nil || verified.Index.GitHub == nil || verified.Index.GitHub.TagPrefix != name+"/" || verified.Index.GitHub.Repository != "acme/piglets" {
			t.Fatalf("signed identity=%+v err=%v", verified.Index, err)
		}
		mu.Lock()
		tags = append(tags, name+"/v"+version)
		mu.Unlock()
	}
	for _, name := range []string{"alpha", "beta"} {
		keys[name], _, ids[name] = writePublishKey(t)
		for _, version := range []string{"1.0.0", "2.0.0"} {
			publish(name, version, keys[name])
		}
	}
	duplicateSource := writePublishPiglet(t, "name: alpha\nrelease: {version: 1.0.0}\n")
	duplicateBuilder := newSigningFakeBuilder(t)
	if code, _, stderr := runPublishForTest(t, duplicateBuilder.builders, duplicateSource, "--to", "github", "--repo", "acme/piglets", "--sign-key", keys["alpha"], "--tag-prefix", "alpha/", "--targets", target, "--yes"); code != 1 || duplicateBuilder.buildCalls != 0 || !strings.Contains(stderr, "alpha/v1.0.0 already exists") {
		t.Fatalf("duplicate publication=%d %s", code, stderr)
	}
	// A prerelease and another Piglet's much higher version cannot update alpha.
	publish("beta", "99.0.0-rc.1", keys["beta"])
	publish("unrelated", "99.0.0", keys["alpha"])
	options := pigletrelease.Options{Client: client, Target: target}
	initial := map[string]pigletrelease.Result{}
	for _, name := range []string{"alpha", "beta"} {
		result, err := pigletrelease.Pull(t.Context(), "github:acme/piglets/"+name+"@1.0.0", options)
		if err != nil || result.Piglet != name || result.SignerKeyID != ids[name] {
			t.Fatalf("pull=%+v err=%v", result, err)
		}
		initial[name] = result
		receiptBytes, err := os.ReadFile(result.Receipt)
		if err != nil {
			t.Fatal(err)
		}
		var receipt pigletrelease.Receipt
		if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
			t.Fatal(err)
		}
		envelope, err := json.Marshal(receipt.IndexEnvelope)
		if err != nil {
			t.Fatal(err)
		}
		verified, err := pigletrelease.Verify(envelope)
		if err != nil || verified.Index.GitHub == nil || verified.Index.GitHub.TagPrefix != name+"/" {
			t.Fatalf("receipt lost signed namespace: %+v %v", verified.Index, err)
		}
	}
	alphaIndex := filepath.Join(gh.state, "releases", "alpha%2Fv2.0.0", "piglet-release.json")
	original, err := os.ReadFile(alphaIndex)
	if err != nil {
		t.Fatal(err)
	}
	betaIndex, err := os.ReadFile(filepath.Join(gh.state, "releases", "beta%2Fv2.0.0", "piglet-release.json"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := signature.ReadPrivateKey(keys["alpha"])
	if err != nil {
		t.Fatal(err)
	}
	mutateIndex := func(change func(*pigletrelease.Index)) []byte {
		t.Helper()
		verified, err := pigletrelease.Verify(original)
		if err != nil {
			t.Fatal(err)
		}
		change(&verified.Index)
		data, err := pigletrelease.Sign(verified.Index, key)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"other Piglet", betaIndex, "names beta, requested alpha"},
		{"other repository", mutateIndex(func(i *pigletrelease.Index) { i.GitHub.Repository = "other/piglets" }), "repository or tag namespace"},
		{"other version", mutateIndex(func(i *pigletrelease.Index) { i.Version = "3.0.0" }), "version 3.0.0, requested 2.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			err := os.WriteFile(alphaIndex, tc.data, 0o644)
			mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pigletrelease.Update(t.Context(), "alpha", options); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("update error=%v", err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(initial["alpha"].Receipt))), "2.0.0", "linux", "amd64.pull")); !os.IsNotExist(err) {
				t.Fatalf("failed update published receipt: %v", err)
			}
		})
	}
	mu.Lock()
	err = os.WriteFile(alphaIndex, original, 0o644)
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	assetPath := filepath.Join(gh.state, "releases", "alpha%2Fv2.0.0", "pig-alpha-linux-amd64")
	asset, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), asset...)
	tampered[0] ^= 1
	mu.Lock()
	err = os.WriteFile(assetPath, tampered, 0o755)
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pigletrelease.Update(t.Context(), "alpha", options); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("tampered update=%v", err)
	}
	mu.Lock()
	err = os.WriteFile(assetPath, asset, 0o755)
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		result, err := pigletrelease.Update(t.Context(), name, options)
		if err != nil || result.Piglet != name || result.Version != "2.0.0" || result.SignerKeyID != ids[name] {
			t.Fatalf("update=%+v err=%v", result, err)
		}
		same, err := pigletrelease.Update(t.Context(), name, options)
		if err != nil || !reflect.DeepEqual(result, same) {
			t.Fatalf("no-op update=%+v err=%v", same, err)
		}
	}
	rollback := options
	rollback.Version = "1.0.0"
	if _, err := pigletrelease.Update(t.Context(), "alpha", rollback); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("rollback=%v", err)
	}
	rotatedPath, rotated, rotatedID := writePublishKey(t)
	if rotatedID != signature.KeyID(rotated.Public().(ed25519.PublicKey)) {
		t.Fatal("bad fixture key")
	}
	publish("alpha", "3.0.0", rotatedPath)
	if _, err := pigletrelease.Update(t.Context(), "alpha", options); err == nil || !strings.Contains(err.Error(), "pinned to signer") {
		t.Fatalf("signer rotation=%v", err)
	}
	accepted := options
	accepted.AcceptSigner = rotatedID
	if result, err := pigletrelease.Update(t.Context(), "alpha", accepted); err != nil || result.Version != "3.0.0" {
		t.Fatalf("accepted rotation=%+v %v", result, err)
	}
	if err := signature.RevokeKey(signature.TrustDir(), ids["beta"]); err != nil {
		t.Fatal(err)
	}
	if _, err := pigletrelease.Update(t.Context(), "beta", options); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked update=%v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := pigletrelease.Update(canceled, "alpha", options); err == nil {
		t.Fatal("canceled update succeeded")
	}
}

type githubTestTransport struct {
	endpoint  *url.URL
	transport http.RoundTripper
}

func (g githubTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "github.com" && r.URL.Host != "api.github.com" {
		return nil, fmt.Errorf("unexpected host %s", r.URL.Host)
	}
	clone := r.Clone(r.Context())
	clone.URL.Scheme, clone.URL.Host = g.endpoint.Scheme, g.endpoint.Host
	return g.transport.RoundTrip(clone)
}
