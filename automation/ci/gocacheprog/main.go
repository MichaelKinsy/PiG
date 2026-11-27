// SPDX-License-Identifier: MIT

// Command gocacheprog implements Go's GOCACHEPROG wire protocol (Go 1.24+) with a
// local content-addressed disk cache and an optional S3/R2-compatible remote backend.
//
// Security and resilience properties:
//   - Zero external module dependencies: builds rapidly with only the Go standard library.
//   - Fail-open fallback to local disk: missing credentials, unreachable endpoints,
//     HTTP errors, or corrupt remote objects never fail a Go build or test run.
//   - Read-only enforcement on PRs: --readonly (or PIG_GOCACHE_READONLY=1, or missing
//     write credentials) disables all remote PUT requests while keeping local disk writes.
//   - End-to-end integrity check: every remote GET verifies sha256(body) == OutputID.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gocacheprog", flag.ContinueOnError)
	fs.SetOutput(stderr)

	defaultDiskDir := os.Getenv("PIG_GOCACHE_DISK_DIR")
	if defaultDiskDir == "" {
		if base, err := os.UserCacheDir(); err == nil && base != "" {
			defaultDiskDir = filepath.Join(base, "pig-gocacheprog")
		} else {
			defaultDiskDir = filepath.Join(os.TempDir(), "pig-gocacheprog")
		}
	}

	diskDir := fs.String("disk-dir", defaultDiskDir, "local disk cache directory")
	bucket := fs.String("bucket", os.Getenv("PIG_GOCACHE_BUCKET"), "S3/R2 bucket name (empty = local disk only)")
	endpoint := fs.String("endpoint", os.Getenv("PIG_GOCACHE_ENDPOINT"), "S3-compatible endpoint URL (e.g. R2 or MinIO)")
	region := fs.String("region", firstNonEmpty(os.Getenv("PIG_GOCACHE_REGION"), os.Getenv("AWS_REGION")), "S3 region (default: auto for custom endpoint, else us-east-1)")
	prefix := fs.String("prefix", os.Getenv("PIG_GOCACHE_PREFIX"), "remote object key prefix")
	readOnly := fs.Bool("readonly", parseEnvBool("PIG_GOCACHE_READONLY"), "disable remote PUTs (safe read-only mode for pull requests)")
	anonymous := fs.Bool("anonymous", parseEnvBool("PIG_GOCACHE_ANONYMOUS"), "allow unsigned remote GETs for public-read buckets (implies --readonly)")
	timeout := fs.Duration("timeout", parseEnvDuration("PIG_GOCACHE_TIMEOUT", 10*time.Second), "per-request remote HTTP timeout")
	maxErrors := fs.Int("max-remote-errors", 5, "consecutive remote errors before bypassing remote for this process")
	maxDiskBytes := fs.Int64("max-disk-bytes", parseEnvInt64("PIG_GOCACHE_MAX_DISK_BYTES", DefaultMaxDiskBytes), "maximum local disk cache size in bytes (0 = default)")
	maxDiskAge := fs.Duration("max-disk-age", parseEnvDuration("PIG_GOCACHE_MAX_DISK_AGE", DefaultMaxDiskAge), "maximum local disk cache entry age (0 = default)")
	maxRemoteBytes := fs.Int64("max-remote-bytes", parseEnvInt64("PIG_GOCACHE_MAX_REMOTE_BYTES", DefaultMaxRemoteBytes), "maximum remote object size in bytes before treating as a miss")
	verbose := fs.Bool("verbose", parseEnvBool("PIG_GOCACHE_VERBOSE"), "log cache summary statistics to stderr on close")
	selfCheck := fs.Bool("self-check", false, "verify configuration and local disk writability, print mode, and exit")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	effectiveReadOnly := *readOnly || *anonymous
	var remote RemoteBackend
	mode := "local-only"

	if strings.TrimSpace(*bucket) != "" {
		accessKey := firstNonEmpty(os.Getenv("PIG_GOCACHE_ACCESS_KEY_ID"), os.Getenv("AWS_ACCESS_KEY_ID"))
		secretKey := firstNonEmpty(os.Getenv("PIG_GOCACHE_SECRET_ACCESS_KEY"), os.Getenv("AWS_SECRET_ACCESS_KEY"))
		sessionToken := firstNonEmpty(os.Getenv("PIG_GOCACHE_SESSION_TOKEN"), os.Getenv("AWS_SESSION_TOKEN"))

		backend, err := NewS3Backend(S3Config{
			Bucket:               *bucket,
			Endpoint:             *endpoint,
			Region:               *region,
			Prefix:               *prefix,
			AccessKeyID:          accessKey,
			SecretAccessKey:      secretKey,
			SessionToken:         sessionToken,
			ReadOnly:             effectiveReadOnly,
			Anonymous:            *anonymous,
			Timeout:              *timeout,
			MaxConsecutiveErrors: *maxErrors,
			MaxObjectBytes:       *maxRemoteBytes,
		})
		if err != nil {
			// Never fail the build when credentials are missing (e.g. fork PRs) or endpoint is misconfigured.
			_, _ = fmt.Fprintf(stderr, "gocacheprog: remote cache disabled (%v); falling back to local disk cache\n", err)
		} else {
			remote = backend
			if backend.ReadOnly() {
				mode = "remote-readonly"
			} else {
				mode = "remote-readwrite"
			}
		}
	}

	srv, err := NewServer(ServerConfig{
		DiskDir:      *diskDir,
		Remote:       remote,
		ReadOnly:     effectiveReadOnly,
		Verbose:      *verbose,
		Log:          stderr,
		MaxDiskBytes: *maxDiskBytes,
		MaxDiskAge:   *maxDiskAge,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gocacheprog: %v\n", err)
		return 1
	}

	if *selfCheck {
		if err := srv.ProbeDiskWritable(); err != nil {
			_, _ = fmt.Fprintf(stderr, "gocacheprog: self-check disk write probe failed: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "ok mode=%s disk=%s\n", mode, srv.diskDir)
		return 0
	}

	if err := srv.Serve(context.Background(), stdin, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "gocacheprog: %v\n", err)
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseEnvBool(name string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes"
}

func parseEnvDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

func parseEnvInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		return n
	}
	return fallback
}
