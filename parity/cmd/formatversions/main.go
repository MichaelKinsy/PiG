package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type manifest struct {
	Fields []fieldDisposition `toml:"fields"`
}

type fieldDisposition struct {
	ID             string `toml:"id"`
	Path           string `toml:"path"`
	Owner          string `toml:"owner"`
	Field          string `toml:"field"`
	WireName       string `toml:"wire_name"`
	Classification string `toml:"classification"`
	Rationale      string `toml:"rationale"`
}

type candidate struct {
	ID       string
	Path     string
	Owner    string
	Field    string
	WireName string
}

var versionWireNames = map[string]struct{}{
	"apiVersion":       {},
	"formatVersion":    {},
	"format_version":   {},
	"protocolVersion":  {},
	"protocol_version": {},
	"schemaVersion":    {},
	"schema_version":   {},
	"version":          {},
}

func main() {
	root := flag.String("root", ".", "Pig repository root")
	manifestPath := flag.String("manifest", "parity/format-versions.toml", "reviewed version-field manifest")
	markerRoots := flag.String("marker-roots", ".", "comma-separated roots scanned for removed Pig format markers")
	generate := flag.Bool("generate", false, "print a pending manifest for current candidates")
	strict := flag.Bool("strict", false, "reject tracked Pig-owned format discriminators")
	flag.Parse()

	candidates, err := scanCandidates(*root)
	if err != nil {
		fatal(err)
	}
	if *generate {
		writeGenerated(candidates)
		return
	}
	var reviewed manifest
	if _, err := toml.DecodeFile(filepath.Join(*root, *manifestPath), &reviewed); err != nil {
		fatal(err)
	}
	problems := validate(candidates, reviewed, *strict)
	markerProblems, err := scanRemovedMarkers(*root, strings.Split(*markerRoots, ","))
	if err != nil {
		fatal(err)
	}
	problems = append(problems, markerProblems...)
	slices.Sort(problems)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, problem)
		}
		os.Exit(1)
	}
	counts := map[string]int{}
	for _, field := range reviewed.Fields {
		counts[field.Classification]++
	}
	fmt.Printf("format-version inventory: clean (%d fields: %d Pig-owned format, %d release/content, %d upstream, %d external)\n",
		len(reviewed.Fields), counts["pig-format"], counts["release-content"], counts["upstream"], counts["external"])
}

func scanRemovedMarkers(root string, roots []string) ([]string, error) {
	markers := []string{
		"pig.dev/" + "v1",
		"piglet-" + "v1.json",
		`"schema` + `Version": 1`,
		`"schema_` + `version": 1`,
		"schema_" + "version = 1",
		"dev.pig.builder.protocol=" + "1",
		"protocol-" + "v1",
		"record-" + "v1",
		"schema-" + "v1",
	}
	var problems []string
	for _, configured := range roots {
		configured = strings.TrimSpace(configured)
		if configured == "" {
			continue
		}
		path := configured
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		err := filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case ".git", ".upstream", ".venv", "artifacts", "bin", "node_modules", "tmp", "vendor":
					return filepath.SkipDir
				}
				return nil
			}
			if !isTextContractFile(entry.Name()) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(data)
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			for _, marker := range markers {
				if strings.Contains(text, marker) {
					problems = append(problems, filepath.ToSlash(relative)+": contains removed Pig format marker "+strconv.Quote(marker))
				}
			}
			if strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".md") {
				for line := range strings.SplitSeq(text, "\n") {
					if strings.TrimSpace(line) == "version: 1" {
						problems = append(problems, filepath.ToSlash(relative)+`: contains removed Piglet format marker "version: 1"`)
						break
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return problems, nil
}

func isTextContractFile(name string) bool {
	if name == "Dockerfile" {
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".js", ".json", ".md", ".mjs", ".py", ".toml", ".ts", ".tsx", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "format-version inventory:", err)
	os.Exit(2)
}

func scanCandidates(root string) ([]candidate, error) {
	var candidates []candidate
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".upstream", "bin", "node_modules", "tmp", "vendor":
				return filepath.SkipDir
			}
			if filepath.ToSlash(path) == "parity/artifacts" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range parsed.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			if generic.Tok == token.CONST {
				for _, spec := range generic.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range valueSpec.Names {
						if !strings.Contains(strings.ToLower(name.Name), "version") {
							continue
						}
						id := relative + "#const." + name.Name + ":go:-"
						candidates = append(candidates, candidate{
							ID: id, Path: relative, Owner: "<package>", Field: name.Name, WireName: "-",
						})
					}
				}
				continue
			}
			if generic.Tok != token.TYPE {
				continue
			}
			for _, spec := range generic.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structure.Fields.List {
					if len(field.Names) != 1 {
						continue
					}
					matchedTag := false
					if field.Tag != nil {
						tag, err := strconv.Unquote(field.Tag.Value)
						if err != nil {
							return fmt.Errorf("%s: decode struct tag: %w", relative, err)
						}
						for _, tagName := range []string{"json", "yaml"} {
							wireName := structTagName(tag, tagName)
							if _, ok := versionWireNames[wireName]; !ok {
								continue
							}
							matchedTag = true
							id := relative + "#" + typeSpec.Name.Name + "." + field.Names[0].Name + ":" + tagName + ":" + wireName
							candidates = append(candidates, candidate{
								ID: id, Path: relative, Owner: typeSpec.Name.Name,
								Field: field.Names[0].Name, WireName: wireName,
							})
						}
					}
					if !matchedTag && isVersionFieldName(field.Names[0].Name) {
						id := relative + "#" + typeSpec.Name.Name + "." + field.Names[0].Name + ":go:-"
						candidates = append(candidates, candidate{
							ID: id, Path: relative, Owner: typeSpec.Name.Name,
							Field: field.Names[0].Name, WireName: "-",
						})
					}
				}
			}
		}
		return nil
	})
	slices.SortFunc(candidates, func(a, b candidate) int { return strings.Compare(a.ID, b.ID) })
	return candidates, err
}

func isVersionFieldName(name string) bool {
	switch name {
	case "APIVersion", "FormatVersion", "ProtocolVersion", "SchemaVersion", "Version":
		return true
	default:
		return false
	}
}

func structTagName(tag, key string) string {
	for tag != "" {
		tag = strings.TrimLeft(tag, " ")
		if tag == "" {
			break
		}
		index := strings.IndexByte(tag, ':')
		if index <= 0 || index+1 >= len(tag) || tag[index+1] != '"' {
			break
		}
		name := tag[:index]
		value, rest, ok := cutQuoted(tag[index+1:])
		if !ok {
			break
		}
		tag = rest
		if name == key {
			value, _, _ = strings.Cut(value, ",")
			return value
		}
	}
	return ""
}

func cutQuoted(input string) (string, string, bool) {
	for i := 1; i < len(input); i++ {
		if input[i] != '"' || input[i-1] == '\\' {
			continue
		}
		value, err := strconv.Unquote(input[:i+1])
		if err != nil {
			return "", "", false
		}
		return value, input[i+1:], true
	}
	return "", "", false
}

func validate(candidates []candidate, reviewed manifest, strict bool) []string {
	actual := make(map[string]candidate, len(candidates))
	for _, candidate := range candidates {
		actual[candidate.ID] = candidate
	}
	seen := make(map[string]struct{}, len(reviewed.Fields))
	var problems []string
	previous := ""
	allowed := map[string]struct{}{"pig-format": {}, "release-content": {}, "upstream": {}, "external": {}}
	for _, field := range reviewed.Fields {
		if _, duplicate := seen[field.ID]; duplicate {
			problems = append(problems, "duplicate disposition "+field.ID)
			continue
		}
		seen[field.ID] = struct{}{}
		if previous != "" && field.ID < previous {
			problems = append(problems, "manifest is not sorted at "+field.ID)
		}
		previous = field.ID
		candidate, ok := actual[field.ID]
		if !ok {
			problems = append(problems, "stale disposition "+field.ID)
			continue
		}
		if field.Path != candidate.Path || field.Owner != candidate.Owner || field.Field != candidate.Field || field.WireName != candidate.WireName {
			problems = append(problems, "candidate identity drift for "+field.ID)
		}
		if _, ok := allowed[field.Classification]; !ok {
			problems = append(problems, fmt.Sprintf("%s has invalid classification %q", field.ID, field.Classification))
		}
		if strings.TrimSpace(field.Rationale) == "" {
			problems = append(problems, field.ID+" has no rationale")
		}
		if strict && field.Classification == "pig-format" {
			problems = append(problems, field.ID+" remains a Pig-owned format discriminator")
		}
	}
	for _, candidate := range candidates {
		if _, ok := seen[candidate.ID]; !ok {
			problems = append(problems, "unclassified version-like field "+candidate.ID)
		}
	}
	slices.Sort(problems)
	return problems
}

func writeGenerated(candidates []candidate) {
	for _, candidate := range candidates {
		fmt.Println("[[fields]]")
		fmt.Printf("id = %q\n", candidate.ID)
		fmt.Printf("path = %q\n", candidate.Path)
		fmt.Printf("owner = %q\n", candidate.Owner)
		fmt.Printf("field = %q\n", candidate.Field)
		fmt.Printf("wire_name = %q\n", candidate.WireName)
		fmt.Println(`classification = "pending"`)
		fmt.Println(`rationale = ""`)
		fmt.Println()
	}
}
