package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

const evidenceKind = "pig-typescript-extension-corpus"

var pinPattern = regexp.MustCompile(`const\s+(UpstreamVersion|UpstreamCommit)\s*=\s*"([^"]+)"`)

type identity struct {
	UpstreamVersion  string `json:"upstreamVersion"`
	UpstreamCommit   string `json:"upstreamCommit"`
	CorpusSHA256     string `json:"corpusSha256"`
	RuntimeSHA256    string `json:"runtimeSha256"`
	PigSHA256        string `json:"pigSha256"`
	PigModule        string `json:"pigModule,omitempty"`
	PigModuleVersion string `json:"pigModuleVersion,omitempty"`
	GoVersion        string `json:"goVersion,omitempty"`
	NodeVersion      string `json:"nodeVersion"`
	OS               string `json:"os"`
	Architecture     string `json:"architecture"`
}

type validationPayload struct {
	Valid      bool     `json:"valid"`
	Name       string   `json:"name"`
	Hash       string   `json:"hash"`
	Tools      []string `json:"tools"`
	Commands   []string `json:"commands"`
	Registered bool     `json:"registered"`
	Phase      string   `json:"phase"`
	Code       string   `json:"code"`
	Error      string   `json:"error"`
}

type result struct {
	Source       string   `json:"source"`
	SourceSHA256 string   `json:"sourceSha256"`
	Name         string   `json:"name,omitempty"`
	Valid        bool     `json:"valid"`
	Registered   bool     `json:"registered"`
	Tools        []string `json:"tools,omitempty"`
	Commands     []string `json:"commands,omitempty"`
	Phase        string   `json:"phase,omitempty"`
	Code         string   `json:"code,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type summary struct {
	Total      int `json:"total"`
	Valid      int `json:"valid"`
	Registered int `json:"registered"`
	Failed     int `json:"failed"`
}

type report struct {
	Kind     string   `json:"kind"`
	Tier     string   `json:"tier"`
	Identity identity `json:"identity"`
	Summary  summary  `json:"summary"`
	Results  []result `json:"results"`
}

func main() {
	root := flag.String("root", ".", "PiG repository root")
	pigBin := flag.String("pig-bin", "bin/pig-parity", "PiG binary to validate with")
	output := flag.String("out", "-", "report path or - for stdout")
	timeout := flag.Duration("timeout", 120*time.Second, "test-only timeout for each example validation")
	flag.Parse()

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}
	binary := *pigBin
	if !filepath.IsAbs(binary) {
		binary = filepath.Join(absoluteRoot, binary)
	}

	report, err := run(absoluteRoot, binary, *timeout)
	if err != nil {
		fatal(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	encoded = append(encoded, '\n')
	if *output == "-" {
		_, err = os.Stdout.Write(encoded)
	} else {
		err = os.WriteFile(*output, encoded, 0o644)
	}
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "TypeScript extension corpus: %d/%d loaded and registered; report: %s\n", report.Summary.Registered, report.Summary.Total, *output)
	if report.Summary.Failed > 0 {
		os.Exit(1)
	}
}

func run(root, binary string, timeout time.Duration) (report, error) {
	corpusRoot := filepath.Join(root, ".upstream", "current", "packages", "coding-agent", "examples", "extensions")
	paths, err := filepath.Glob(filepath.Join(corpusRoot, "*.ts"))
	if err != nil {
		return report{}, err
	}
	slices.Sort(paths)
	if len(paths) == 0 {
		return report{}, fmt.Errorf("no TypeScript examples found under %s", corpusRoot)
	}
	version, commit, err := readPin(filepath.Join(root, "coding", "pigversion", "pigversion.go"))
	if err != nil {
		return report{}, err
	}
	corpusHash, err := hashFiles(corpusRoot, paths)
	if err != nil {
		return report{}, err
	}
	runtimeRoot := filepath.Join(root, "coding", "extension", "host", "subprocess", "runtime-node")
	runtimeFiles, err := sourceFiles(runtimeRoot)
	if err != nil {
		return report{}, err
	}
	runtimeHash, err := hashFiles(runtimeRoot, runtimeFiles)
	if err != nil {
		return report{}, err
	}
	binaryHash, err := hashFile(binary)
	if err != nil {
		return report{}, fmt.Errorf("hash PiG binary: %w", err)
	}
	id := identity{
		UpstreamVersion: version,
		UpstreamCommit:  commit,
		CorpusSHA256:    corpusHash,
		RuntimeSHA256:   runtimeHash,
		PigSHA256:       binaryHash,
		NodeVersion:     commandVersion("node", "--version"),
		OS:              runtime.GOOS,
		Architecture:    runtime.GOARCH,
	}
	if info, err := buildinfo.ReadFile(binary); err == nil {
		id.PigModule = info.Main.Path
		id.PigModuleVersion = info.Main.Version
		id.GoVersion = info.GoVersion
	}

	rep := report{Kind: evidenceKind, Tier: "load-and-registration", Identity: id}
	for _, path := range paths {
		item, err := validateExample(root, binary, corpusRoot, path, timeout)
		if err != nil {
			return report{}, err
		}
		rep.Results = append(rep.Results, item)
	}
	rep.Summary.Total = len(rep.Results)
	for _, item := range rep.Results {
		if item.Valid {
			rep.Summary.Valid++
		}
		if item.Registered {
			rep.Summary.Registered++
		}
		if !item.Valid || !item.Registered {
			rep.Summary.Failed++
		}
	}
	return rep, nil
}

func validateExample(root, binary, corpusRoot, path string, timeout time.Duration) (result, error) {
	sourceHash, err := hashFile(path)
	if err != nil {
		return result{}, err
	}
	relative, err := filepath.Rel(corpusRoot, path)
	if err != nil {
		return result{}, err
	}
	home, err := os.MkdirTemp("", "pig-extension-corpus-")
	if err != nil {
		return result{}, err
	}
	defer func() { _ = os.RemoveAll(home) }()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "install", path, "--validate-only", "--json")
	command.Dir = root
	command.Env = corpusEnvironment(home)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	item := result{Source: filepath.ToSlash(relative), SourceSHA256: sourceHash}
	var payload validationPayload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		if ctx.Err() != nil {
			item.Phase = "timeout"
			item.Code = "timeout"
			item.Error = ctx.Err().Error()
			return item, nil
		}
		return result{}, fmt.Errorf("decode validation output for %s: %w: %s", relative, err, stdout.String())
	}
	item.Name = payload.Name
	item.Valid = payload.Valid
	item.Registered = payload.Registered
	item.Tools = append([]string(nil), payload.Tools...)
	item.Commands = append([]string(nil), payload.Commands...)
	item.Phase = payload.Phase
	item.Code = payload.Code
	item.Error = payload.Error
	if item.Error == "" && runErr != nil {
		item.Error = strings.TrimSpace(stderr.String())
		if item.Error == "" {
			item.Error = runErr.Error()
		}
	}
	return item, nil
}

func corpusEnvironment(home string) []string {
	blocked := []string{"ALL_PROXY=", "HTTPS_PROXY=", "HTTP_PROXY=", "all_proxy=", "https_proxy=", "http_proxy=", "HOME=", "PIG_HOME="}
	environment := slices.DeleteFunc(append([]string(nil), os.Environ()...), func(value string) bool {
		return slices.ContainsFunc(blocked, func(prefix string) bool { return strings.HasPrefix(value, prefix) })
	})
	return append(environment, "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"))
}

func readPin(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	values := map[string]string{}
	for _, match := range pinPattern.FindAllStringSubmatch(string(data), -1) {
		values[match[1]] = match[2]
	}
	if values["UpstreamVersion"] == "" || values["UpstreamCommit"] == "" {
		return "", "", fmt.Errorf("%s does not declare both upstream pins", path)
	}
	return values["UpstreamVersion"], values["UpstreamCommit"], nil
}

func sourceFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".mjs") {
			paths = append(paths, path)
		}
		return nil
	})
	slices.Sort(paths)
	return paths, err
}

func hashFiles(root string, paths []string) (string, error) {
	paths = append([]string(nil), paths...)
	slices.Sort(paths)
	hash := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(relative))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func commandVersion(name string, args ...string) string {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(output))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
