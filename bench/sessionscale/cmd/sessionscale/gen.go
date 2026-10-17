// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/bench/sessionscale"
)

// parseSize reads 299M, 1G, 512K or a plain byte count; M and G are powers of 1024.
func parseSize(s string) (int64, error) {
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, strings.TrimSuffix(s, "K")
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(mult)), nil
}

func gen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	out := fs.String("out", "", "fixture directory")
	size := fs.String("bytes", "", "file size to reach (default 299M, or the profile's size)")
	context := fs.Int("context", 0, "active context tokens (0: 150000)")
	window := fs.Int("window", 0, "model context window (0: derived)")
	compactions := fs.Int("compactions", 0, "compactions (0: owner shape scaled)")
	leaves := fs.Int("leaves", 0, "leaves (0: owner shape scaled)")
	seed := fs.Uint64("seed", 1, "content seed")
	profile := fs.String("profile", "", `shape profile: "" (2026-10-10 report) or "p1" (real-session profile; -bytes defaults to its size)`)
	baseURL := fs.String("base-url", "", "scripted model endpoint written to models.json")
	_ = fs.Parse(args)
	if *out == "" {
		return fmt.Errorf("gen: -out is required")
	}
	var bytes int64
	if *size == "" && *profile == "" {
		*size = "299M"
	}
	if *size != "" {
		n, err := parseSize(*size)
		if err != nil {
			return fmt.Errorf("gen: -bytes: %w", err)
		}
		bytes = n
	}
	meta, err := sessionscale.Write(*out, sessionscale.Shape{
		Bytes: bytes, ContextTokens: *context, Window: *window, Compactions: *compactions, Leaves: *leaves, Seed: *seed, Profile: *profile,
	}, *baseURL)
	if err != nil {
		return err
	}
	//portlint:allow jsonescape benchmark tool output, not Pi-facing
	return json.NewEncoder(os.Stdout).Encode(meta)
}
