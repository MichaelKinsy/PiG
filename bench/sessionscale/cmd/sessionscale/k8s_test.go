// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Two seeds of one size in different directories have one fingerprint but different files (the header's cwd), and the
// fingerprints are the ones bench/k8s's session-scale-entries profile pins for its sizes up to 65 MiB.
func TestK8sSeedFingerprintIsThePinnedOne(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "k8s", "cmd", "k8sbench", "profiles", "session-scale-entries.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prof struct {
		Fingerprints map[string]string `json:"fingerprints"`
	}
	if err := json.Unmarshal(data, &prof); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{7, 65} {
		want := prof.Fingerprints[strconv.Itoa(size)]
		var metas []k8sMeta
		for _, target := range []string{"pig", "pi"} {
			dir := filepath.Join(t.TempDir(), target+"-"+strconv.Itoa(size))
			var out bytes.Buffer
			if err := k8sSeed([]string{"-size", strconv.Itoa(size), "-bytes", strconv.Itoa(size) + "M", "-dir", dir}, &out); err != nil {
				t.Fatal(err)
			}
			var m k8sMeta
			raw, err := os.ReadFile(k8sMetaPath(dir, size))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &m); err != nil || !bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(out.Bytes())) {
				t.Fatalf("meta %s, printed %s: %v", raw, out.Bytes(), err)
			}
			if got, err := fileSHA256(filepath.Join(dir, "session.jsonl")); err != nil || got != m.SHA256 || m.SeedMs <= 0 || m.Bytes <= 0 {
				t.Errorf("meta %+v, file sha256 %s: %v", m, got, err)
			}
			metas = append(metas, m)
		}
		if metas[0].Fingerprint != want || metas[1].Fingerprint != want {
			t.Errorf("size %d: fingerprints %s and %s, the profile pins %s", size, metas[0].Fingerprint, metas[1].Fingerprint, want)
		}
		if metas[0].SHA256 == metas[1].SHA256 {
			t.Errorf("size %d: the two files are identical; the fingerprint test proves nothing", size)
		}
	}
}

func TestK8sFingerprintCoversTheBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	write := func(s string) string {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		fp, err := fixtureFingerprint(path)
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	a := write(`{"type":"session","cwd":"/a","id":"x"}` + "\n" + `{"type":"message"}` + "\n")
	if b := write(`{"cwd":"/b","id":"x","type":"session"}` + "\n" + `{"type":"message"}` + "\n"); a != b {
		t.Errorf("cwd or key order changed the fingerprint: %s %s", a, b)
	}
	if c := write(`{"type":"session","cwd":"/a","id":"y"}` + "\n" + `{"type":"message"}` + "\n"); c == a {
		t.Error("the header's id is not covered")
	}
	if d := write(`{"type":"session","cwd":"/a","id":"x"}` + "\n" + `{"type":"message "}` + "\n"); d == a {
		t.Error("the body is not covered")
	}
	if err := os.WriteFile(path, []byte(`{"type":"message"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureFingerprint(path); err == nil {
		t.Error("a file without a session header has a fingerprint")
	}
}

const k8sPoints = `{"arm":"pig","label":"resume","prompts":0,"rssKiB":2048,"pssKiB":1536,"anonKiB":1024,"hwmKiB":3072,"cpuMs":900,"wallMs":2500}
{"arm":"pig","label":"prompts-3","prompts":3,"rssKiB":4096,"pssKiB":4000,"anonKiB":2048,"hwmKiB":5120,"cpuMs":1200,"wallMs":7000,"promptCpuMs":[40,30,20],"promptWallMs":[1400,1300,1350],"requests":[{"n":1,"bytes":5000,"messages":12,"messagesHash":"aa"},{"n":2,"bytes":5100,"messages":14,"messagesHash":"bb"}]}
{"arm":"pig","label":"quit","quitMs":71,"error":"<nil>"}
`

func TestK8sLine(t *testing.T) {
	line, err := k8sLine([]byte(k8sPoints), "pig", 7, 4, "c031303a10133dde", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"t": "sample", "target": "pig", "turns": 7, "sample": 4, "fingerprint": "c031303a10133dde",
		"open": int64(2500), "openCpu": int64(900), "turn": []int64{1400, 1300, 1350}, "turnCpu": []int64{40, 30, 20},
		"peakRss": 5.0, "resumeRss": 2.0, "resumePss": 1.5, "resumeAnon": 1.0, "promptRss": 4.0, "promptAnon": 2.0,
		"quitMs": int64(71), "requestBytes": 5000, "requestMessages": 12}
	if !reflect.DeepEqual(line, want) {
		t.Errorf("line\n%v\nwant\n%v", line, want)
	}
	for _, c := range []struct{ points, want string }{
		{strings.SplitN(k8sPoints, "\n", 2)[1], "no resume point"},
		{strings.Replace(k8sPoints, `"promptWallMs":[1400,1300,1350]`, `"promptWallMs":[1400,1300]`, 1), "timed 2 prompts, want 3"},
		{strings.Replace(k8sPoints, `"prompts-3"`, `"prompts-2"`, 1), "no point after 3 prompts"},
		{k8sPoints + "not json\n", "run wrote"},
	} {
		if _, err := k8sLine([]byte(c.points), "pig", 7, 4, "fp", 3); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
}

// A sample refuses a fixture whose session file changed after the seed, and a sample of fewer than two prompts.
func TestK8sSampleChecksTheFixture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pig-7")
	if err := k8sSeed([]string{"-size", "7", "-bytes", "64K", "-dir", dir}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "session.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{}\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	args := []string{"-target", "pig", "-size", "7", "-dir", dir, "-cmd", "/bin/false", "-timeout", "5s"}
	if err := k8sSample(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "changed since it was generated") {
		t.Errorf("changed fixture: %v", err)
	}
	if err := k8sSample(append(args, "-prompts", "1"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Errorf("one prompt: %v", err)
	}
}

// Every size each session-scale profile pins is the fingerprint its own seed command produces, and every target seeds
// with the same command: a wrong pin would refuse every session on a cluster. The 299 MiB sizes take a few seconds
// each (minutes under -race), so -short and -race leave them out.
func TestK8sProfilesPinWhatTheirSeedWrites(t *testing.T) {
	for _, name := range []string{"session-scale-context", "session-scale-entries"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "k8s", "cmd", "k8sbench", "profiles", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var prof struct {
			Fingerprints map[string]string `json:"fingerprints"`
			Targets      []struct {
				Name string   `json:"name"`
				Seed []string `json:"seed"`
			} `json:"targets"`
		}
		if err := json.Unmarshal(data, &prof); err != nil {
			t.Fatal(err)
		}
		if len(prof.Targets) != 2 || len(prof.Fingerprints) == 0 {
			t.Fatalf("%s: %d targets, %d pins", name, len(prof.Targets), len(prof.Fingerprints))
		}
		seed := prof.Targets[0].Seed
		for _, tg := range prof.Targets[1:] {
			if !reflect.DeepEqual(tg.Seed, seed) {
				t.Errorf("%s: %s seeds with %q, %s with %q", name, tg.Name, tg.Seed, prof.Targets[0].Name, seed)
			}
		}
		if len(seed) < 4 || !reflect.DeepEqual(seed[:3], []string{"{root}/bin/sessionscale", "k8s", "seed"}) {
			t.Fatalf("%s: seed command %q", name, seed)
		}
		for size, want := range prof.Fingerprints {
			if n, err := strconv.Atoi(size); err != nil || ((testing.Short() || raceEnabled) && n > 65) {
				continue
			}
			dir := filepath.Join(t.TempDir(), "fixture")
			args := make([]string, 0, len(seed)-3)
			for _, a := range seed[3:] {
				args = append(args, strings.NewReplacer("{turns}", size, "{dir}", dir).Replace(a))
			}
			var out bytes.Buffer
			if err := k8sSeed(args, &out); err != nil {
				t.Fatalf("%s %s: %v", name, size, err)
			}
			var m k8sMeta
			if err := json.Unmarshal(out.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			if m.Fingerprint != want {
				t.Errorf("%s size %s: seed writes fingerprint %s, the profile pins %s", name, size, m.Fingerprint, want)
			}
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
	}
}
