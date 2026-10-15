package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const modulePrefix = "github.com/MichaelKinsy/PiG/"

// mutant is one source edit: bytes [Start,End) of File become Repl.
type mutant struct {
	ID    string
	File  string
	Start int
	End   int
	Repl  string
}

type lineRange struct{ from, to int }

// blockRanges parses coverage block lines ("<import path>/file.go:l.c,l.c n")
// into per-file line ranges.
func blockRanges(data string) map[string][]lineRange {
	out := map[string][]lineRange{}
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		spec, _, _ := strings.Cut(line, " ")
		file, pos, ok := strings.Cut(spec, ":")
		if !ok || !strings.HasPrefix(file, modulePrefix) {
			continue
		}
		start, end, _ := strings.Cut(pos, ",")
		from, _, _ := strings.Cut(start, ".")
		to, _, _ := strings.Cut(end, ".")
		f, err1 := strconv.Atoi(from)
		t, err2 := strconv.Atoi(to)
		if err1 != nil || err2 != nil {
			continue
		}
		rel := strings.TrimPrefix(file, modulePrefix)
		out[rel] = append(out[rel], lineRange{f, t})
	}
	return out
}

func inRanges(rs []lineRange, line int) bool {
	if rs == nil {
		return false
	}
	for _, r := range rs {
		if line >= r.from && line <= r.to {
			return true
		}
	}
	return false
}

var swapOp = map[token.Token]string{
	token.EQL: "!=", token.NEQ: "==", token.LSS: ">=", token.GEQ: "<", token.GTR: "<=", token.LEQ: ">",
	token.LAND: "||", token.LOR: "&&", token.ADD: "-", token.SUB: "+",
}

// mutantsFor lists the mutation sites of one production file.
func mutantsFor(fset *token.FileSet, rel string, src []byte, file *ast.File, ranges []lineRange) []mutant {
	var out []mutant
	add := func(pos, end token.Pos, kind, repl string) {
		p := fset.Position(pos)
		if ranges != nil && !inRanges(ranges, p.Line) {
			return
		}
		out = append(out, mutant{
			ID:   fmt.Sprintf("%s:%d:%d:%s", rel, p.Line, p.Column, kind),
			File: rel, Start: p.Offset, End: fset.Position(end).Offset, Repl: repl,
		})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if repl, ok := swapOp[x.Op]; ok {
				add(x.OpPos, x.OpPos+token.Pos(len(x.Op.String())), "op"+x.Op.String(), repl)
			}
		case *ast.IfStmt:
			c := fset.Position(x.Cond.Pos()).Offset
			e := fset.Position(x.Cond.End()).Offset
			add(x.Cond.Pos(), x.Cond.End(), "negate", "!("+string(src[c:e])+")")
		case *ast.BasicLit:
			if x.Kind == token.INT {
				if v, err := strconv.Atoi(x.Value); err == nil && v > 0 {
					add(x.Pos(), x.End(), "int", strconv.Itoa(v+1))
				}
			}
		case *ast.ReturnStmt:
			if len(x.Results) == 1 {
				if id, ok := x.Results[0].(*ast.Ident); ok && (id.Name == "true" || id.Name == "false") {
					flip := "true"
					if id.Name == "true" {
						flip = "false"
					}
					add(id.Pos(), id.End(), "bool", flip)
				}
			}
		}
		return true
	})
	return out
}

func listGoFiles(root, pkg, tags string) ([]string, error) {
	args := []string{"list", "-f", "{{range .GoFiles}}{{.}}\n{{end}}"}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	cmd := exec.Command("go", append(args, pkg)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

func pick(ms []mutant, n int, seed string) []mutant {
	sort.Slice(ms, func(i, j int) bool {
		hi, hj := fnv.New64a(), fnv.New64a()
		hi.Write([]byte(seed + ms[i].ID))
		hj.Write([]byte(seed + ms[j].ID))
		return hi.Sum64() < hj.Sum64()
	})
	if len(ms) > n {
		ms = ms[:n]
	}
	return ms
}

func runMutate(root string, args []string) error {
	fs := flag.NewFlagSet("mutate", flag.ExitOnError)
	pkg := fs.String("pkg", "", "package whose production source is mutated, e.g. ./tui")
	n := fs.Int("n", 20, "mutants to run")
	seed := fs.String("seed", "1", "sampling seed")
	blocks := fs.String("blocks", "", "restrict mutation sites to the lines of the coverage blocks in this file")
	tags := fs.String("tags", "", "build tags")
	tests := fs.String("tests", "", "comma-separated packages whose tests judge each mutant (default: -pkg)")
	only := fs.String("only", "", "run exactly the mutants listed in this earlier result file (the after run of a batch)")
	skip := fs.String("skip", "", "go test -skip regexp: run the suite without these tests (the after state of a deletion batch)")
	tree := fs.String("tree", "", "repository checkout to mutate and test (default: this one)")
	out := fs.String("out", "", "write mutant<TAB>killed|survived lines here")
	timeout := fs.Duration("timeout", 15*time.Minute, "per mutant test run timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pkg == "" || *out == "" {
		return fmt.Errorf("-pkg and -out are required")
	}
	if *tree != "" {
		root = *tree
	}
	var ranges map[string][]lineRange
	if *blocks != "" {
		data, err := os.ReadFile(*blocks)
		if err != nil {
			return err
		}
		ranges = blockRanges(string(data))
	}
	files, err := listGoFiles(root, *pkg, *tags)
	if err != nil {
		return err
	}
	pkgRel := strings.TrimPrefix(*pkg, "./")
	fset := token.NewFileSet()
	var all []mutant
	sources := map[string][]byte{}
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Join(pkgRel, f))
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return err
		}
		sources[rel] = src
		af, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		var rs []lineRange
		if ranges != nil {
			rs = ranges[rel]
			if len(rs) == 0 {
				continue
			}
		}
		all = append(all, mutantsFor(fset, rel, src, af, rs)...)
	}
	work, err := os.MkdirTemp("", "testprune-mutate-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	chosen := pick(all, *n*3, *seed)
	if *only != "" {
		listed, err := os.ReadFile(*only)
		if err != nil {
			return err
		}
		ids := map[string]bool{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(listed)), "\n") {
			id, _, _ := strings.Cut(line, "\t")
			ids[id] = true
		}
		chosen = chosen[:0]
		for _, m := range all {
			if ids[m.ID] {
				chosen = append(chosen, m)
			}
		}
		if len(chosen) != len(ids) {
			return fmt.Errorf("%s lists %d mutants and %d exist in this checkout", *only, len(ids), len(chosen))
		}
		*n = len(chosen)
	}
	testPackages := []string{*pkg}
	if *tests != "" {
		testPackages = strings.Split(*tests, ",")
	}
	var results strings.Builder
	done, killed := 0, 0
	for i, m := range chosen {
		if done >= *n {
			break
		}
		mutated := append(append(append([]byte{}, sources[m.File][:m.Start]...), m.Repl...), sources[m.File][m.End:]...)
		mpath := filepath.Join(work, fmt.Sprintf("m%d.go", i))
		if err := os.WriteFile(mpath, mutated, 0o644); err != nil {
			return err
		}
		overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(root, m.File): mpath}})
		opath := filepath.Join(work, fmt.Sprintf("o%d.json", i))
		if err := os.WriteFile(opath, overlay, 0o644); err != nil {
			return err
		}
		verdict := "survived"
		for j, testPkg := range testPackages {
			bin := filepath.Join(work, fmt.Sprintf("t%d-%d.test", i, j))
			build := []string{"test", "-c", "-overlay=" + opath, "-o", bin}
			if *tags != "" {
				build = append(build, "-tags="+*tags)
			}
			invalid, err := buildMutant(root, testPkg, build)
			if err != nil {
				return fmt.Errorf("mutant %s: %w", m.ID, err)
			}
			if invalid {
				verdict = "invalid"
				break
			}
			if err := runMutantBinary(root, testPkg, bin, *skip, *timeout); err != nil {
				verdict = "killed"
				killed++
			}
			_ = os.Remove(bin)
			if verdict == "killed" {
				break
			}
		}
		if verdict == "invalid" {
			if *only != "" {
				return fmt.Errorf("mutant %s does not compile here", m.ID)
			}
			continue
		}
		done++
		fmt.Fprintf(&results, "%s\t%s\n", m.ID, verdict)
	}
	if err := os.WriteFile(*out, []byte(results.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("pkg=%s mutants=%d killed=%d survived=%d\n", *pkg, done, killed, done-killed)
	return nil
}

func runMutantBinary(root, pkg, bin, skip string, timeout time.Duration) error {
	home, err := os.MkdirTemp("", "testprune-home-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(home) }()
	testArgs := []string{"-test.failfast", "-test.timeout=" + timeout.String()}
	if skip != "" {
		testArgs = append(testArgs, "-test.skip="+skip)
	}
	cmd := exec.Command(bin, testArgs...)
	cmd.Dir = filepath.Join(root, strings.TrimPrefix(pkg, "./"))
	cmd.Env = testEnv(home)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	cmd.SysProcAttr = procGroup()
	return cmd.Run()
}

// compareMutations reports mutants the before run killed and the after run did not.
func runMutantCompare(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: testprune mutcompare BEFORE.tsv AFTER.tsv")
	}
	read := func(p string) (map[string]string, error) {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if k, v, ok := strings.Cut(line, "\t"); ok {
				m[k] = v
			}
		}
		return m, nil
	}
	before, err := read(args[0])
	if err != nil {
		return err
	}
	after, err := read(args[1])
	if err != nil {
		return err
	}
	for id := range before {
		if _, ok := after[id]; !ok {
			return fmt.Errorf("mutant %s ran before the change and not after it: the samples are not paired (run the after side with -only BEFORE.tsv)", id)
		}
	}
	var kb, ka, lost int
	for id, v := range before {
		if v == "killed" {
			kb++
		}
		if after[id] == "killed" {
			ka++
		}
		if v == "killed" && after[id] != "killed" {
			lost++
			fmt.Println("no longer killed:", id)
		}
	}
	fmt.Printf("mutants(before)=%d killed_before=%d killed_after=%d newly_surviving=%d\n", len(before), kb, ka, lost)
	if lost > 0 {
		return fmt.Errorf("%d mutants were killed before pruning and survive after it", lost)
	}
	return nil
}

var compileErrorRE = regexp.MustCompile(`\.go:\d+:\d+: `)

// buildMutant compiles a mutant's test binary. A mutation the compiler rejects
// reports invalid; any other build failure is retried and then returned, so a
// transient failure never silently drops a mutant from the sample.
func buildMutant(root, pkg string, build []string) (invalid bool, err error) {
	for attempt := range 3 {
		cmd := exec.Command("go", append(build, pkg)...)
		cmd.Dir = root
		var buf bytes.Buffer
		cmd.Stderr = &buf
		if runErr := cmd.Run(); runErr == nil {
			return false, nil
		} else if compileErrorRE.Match(buf.Bytes()) {
			if os.Getenv("TESTPRUNE_DEBUG") != "" {
				fmt.Fprintf(os.Stderr, "invalid mutant:\n%s\n", tailLines(buf.String(), 6))
			}
			return true, nil
		} else if attempt == 2 {
			return false, fmt.Errorf("build failed without a compile error: %w\n%s", runErr, tailLines(buf.String(), 10))
		}
		time.Sleep(10 * time.Second)
	}
	return false, nil
}
