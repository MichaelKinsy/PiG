// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/bench/sessionscale"
)

// The k8s subcommands are bench/k8s's adapter: k8sbench runs `k8s seed` once per target and size and `k8s sample` once
// per sample, and reads one JSON line from each (docs/site/docs/benchmarking.md, Profiles).
//
//	sessionscale k8s seed -size N -dir DIR [-bytes 299M] [-context 0] [-seed 1]
//	sessionscale k8s sample -target NAME -size N -dir DIR -sample I -cmd 'BIN ARGS' [-prompts 5] [-timeout 30m]

// fingerprintCwd replaces the header's working directory in the fingerprint, so two copies of one fixture in
// different directories have the same fingerprint.
const fingerprintCwd = "/sessionscale"

// k8sMeta is what `k8s seed` writes to DIR/k8s-<size>.json and prints.
type k8sMeta struct {
	SeedMs        float64 `json:"seedMs"`
	Fingerprint   string  `json:"fingerprint"`
	SHA256        string  `json:"sha256"`
	Bytes         int64   `json:"bytes"`
	Entries       int     `json:"entries"`
	ContextTokens int     `json:"contextTokens"`
	Window        int     `json:"window"`
	Compactions   int     `json:"compactions"`
	Leaves        int     `json:"leaves"`
}

func k8s(args []string) error {
	if len(args) == 0 {
		return errors.New("k8s: seed or sample")
	}
	switch args[0] {
	case "seed":
		return k8sSeed(args[1:], os.Stdout)
	case "sample":
		return k8sSample(args[1:], os.Stdout)
	}
	return fmt.Errorf("k8s: unknown command %q", args[0])
}

func k8sMetaPath(dir string, size int) string {
	return filepath.Join(dir, fmt.Sprintf("k8s-%d.json", size))
}

func k8sSeed(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("k8s seed", flag.ContinueOnError)
	size := fs.Int("size", 0, "the size k8sbench names the fixture by (its {turns})")
	dir := fs.String("dir", "", "fixture directory")
	sizeSpec := fs.String("bytes", "299M", "file size to reach")
	context := fs.Int("context", 0, "active context tokens (0: 150000)")
	seed := fs.Uint64("seed", 1, "content seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *size <= 0 || *dir == "" {
		return errors.New("k8s seed: -size and -dir are required")
	}
	n, err := parseSize(*sizeSpec)
	if err != nil {
		return fmt.Errorf("k8s seed: -bytes: %w", err)
	}
	start := time.Now()
	meta, err := sessionscale.Write(*dir, sessionscale.Shape{Bytes: n, ContextTokens: *context, Seed: *seed}, "")
	if err != nil {
		return err
	}
	seedMs := float64(time.Since(start).Microseconds()) / 1000
	fp, err := fixtureFingerprint(meta.Session)
	if err != nil {
		return err
	}
	out := k8sMeta{SeedMs: seedMs, Fingerprint: fp, SHA256: meta.SHA256, Bytes: meta.Bytes, Entries: meta.Entries,
		ContextTokens: meta.ContextTokens, Window: meta.Window, Compactions: meta.Compactions, Leaves: meta.Leaves}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.WriteFile(k8sMetaPath(*dir, *size), append(data, '\n'), 0o600); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", data)
	return err
}

// fixtureFingerprint is the first 16 hex digits of the sha256 of the session file with fingerprintCwd as its header's
// cwd (the header re-encoded with sorted keys).
func fixtureFingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<20)
	head, err := r.ReadBytes('\n')
	if err != nil {
		return "", fmt.Errorf("%s: no header line: %w", path, err)
	}
	var header map[string]any
	if err := json.Unmarshal(head, &header); err != nil || header["type"] != "session" {
		return "", fmt.Errorf("%s: the first line is not a session header", path)
	}
	header["cwd"] = fingerprintCwd
	//portlint:allow mapkeyorder benchmark tool output, not Pi-facing
	canon, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(canon)
	h.Write([]byte{'\n'})
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// k8sSample runs one `run` of the runtime on a copy of the fixture (interactive, resume then the prompts) and prints
// one k8sbench sample line.
func k8sSample(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("k8s sample", flag.ContinueOnError)
	target := fs.String("target", "", "k8sbench target name")
	size := fs.Int("size", 0, "the fixture's size (its {turns})")
	dir := fs.String("dir", "", "fixture directory written by k8s seed")
	index := fs.Int("sample", 0, "the round")
	command := fs.String("cmd", "", "command line of the runtime (space separated)")
	prompts := fs.Int("prompts", 5, "prompts after the resume (at least 2)")
	timeout := fs.Duration("timeout", 30*time.Minute, "overall timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" || *size <= 0 || *dir == "" || *command == "" {
		return errors.New("k8s sample: -target, -size, -dir and -cmd are required")
	}
	if *prompts < 2 {
		return errors.New("k8s sample: -prompts must be at least 2 (k8sbench needs two turns)")
	}
	data, err := os.ReadFile(k8sMetaPath(*dir, *size))
	if err != nil {
		return err
	}
	var meta k8sMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("%s: %w", k8sMetaPath(*dir, *size), err)
	}
	if got, err := fileSHA256(filepath.Join(*dir, "session.jsonl")); err != nil {
		return err
	} else if got != meta.SHA256 {
		return fmt.Errorf("%s changed since it was generated (sha256 %s, generated %s)", filepath.Join(*dir, "session.jsonl"), got[:16], meta.SHA256[:16])
	}
	work, err := os.MkdirTemp("", "sessionscale-k8s-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	points := filepath.Join(work, "points.jsonl")
	if err := run([]string{"-arm", *target, "-cmd", *command, "-fixture", *dir, "-prompts", strconv.Itoa(*prompts),
		"-private", "-out", points, "-work", filepath.Join(work, "run"), "-timeout", timeout.String()}); err != nil {
		return err
	}
	raw, err := os.ReadFile(points)
	if err != nil {
		return err
	}
	line, err := k8sLine(raw, *target, *size, *index, meta.Fingerprint, *prompts)
	if err != nil {
		return err
	}
	//portlint:allow mapkeyorder benchmark tool output, not Pi-facing
	out, err := json.Marshal(line)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", out)
	return err
}

const kibPerMiB = 1024

func mib(kib int64) float64 { return float64(kib) / kibPerMiB }

// runPoint is the part of run's point a sample line uses (run.go builds on Linux only).
type runPoint struct {
	Label        string  `json:"label"`
	RssKiB       int64   `json:"rssKiB"`
	PssKiB       int64   `json:"pssKiB"`
	AnonKiB      int64   `json:"anonKiB"`
	HWMKiB       int64   `json:"hwmKiB"`
	CPUms        int64   `json:"cpuMs"`
	WallMs       int64   `json:"wallMs"`
	PromptCPUms  []int64 `json:"promptCpuMs"`
	PromptWallMs []int64 `json:"promptWallMs"`
	QuitMs       *int64  `json:"quitMs"`
	Requests     []struct {
		Bytes    int `json:"bytes"`
		Messages int `json:"messages"`
	} `json:"requests"`
}

// k8sLine maps run's points to a k8sbench sample line: open and openCpu are the resume point's wall and CPU time from
// the start (ready and settled), turn[] and turnCpu[] the prompts' wall and CPU times, peakRss the largest peak RSS of
// the process tree (MiB), and the metrics the tree's memory after the resume and after the prompts and the quit time.
func k8sLine(raw []byte, target string, size, index int, fingerprint string, prompts int) (map[string]any, error) {
	var resume, after *runPoint
	var quitMs *int64
	for l := range bytes.SplitSeq(bytes.TrimSpace(raw), []byte{'\n'}) {
		var p runPoint
		if err := json.Unmarshal(l, &p); err != nil {
			return nil, fmt.Errorf("run wrote %q: %w", l, err)
		}
		switch p.Label {
		case "resume":
			resume = &p
		case fmt.Sprintf("prompts-%d", prompts):
			after = &p
		case "quit":
			quitMs = p.QuitMs
		}
	}
	if resume == nil || after == nil {
		return nil, fmt.Errorf("run wrote no resume point or no point after %d prompts", prompts)
	}
	if len(after.PromptWallMs) != prompts || len(after.PromptCPUms) != prompts {
		return nil, fmt.Errorf("run timed %d prompts, want %d", len(after.PromptWallMs), prompts)
	}
	line := map[string]any{
		"t": "sample", "target": target, "turns": size, "sample": index, "fingerprint": fingerprint,
		"open": resume.WallMs, "openCpu": resume.CPUms, "turn": after.PromptWallMs, "turnCpu": after.PromptCPUms,
		"peakRss":    mib(max(resume.HWMKiB, after.HWMKiB)),
		"resumeRss":  mib(resume.RssKiB),
		"resumePss":  mib(resume.PssKiB),
		"resumeAnon": mib(resume.AnonKiB),
		"promptRss":  mib(after.RssKiB),
		"promptAnon": mib(after.AnonKiB),
	}
	if quitMs != nil {
		line["quitMs"] = *quitMs
	}
	if len(after.Requests) > 0 {
		first := after.Requests[0]
		line["requestBytes"] = first.Bytes
		line["requestMessages"] = first.Messages
	}
	return line, nil
}
