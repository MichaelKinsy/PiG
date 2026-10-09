package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2366
// Pi: packages/coding-agent/src/core/package-manager.ts:95 (ProgressEvent.message).
func TestPackageBatchedConcurrentUpdatesOriginal(t *testing.T) {
	f := newPackageProcessFixture(t, `(async()=>{
 if(command==='npm'&&args[0]==='view'){if(args[1]==='user-unknown')throw new Error('registry unavailable');if(!['user-old','project-old','user-current','project-current'].includes(args[1]))throw new Error('unexpected package lookup');console.log(JSON.stringify(args[1].endsWith('-old')?'2.0.0':'1.0.0'));return;}
 let kind;
 if(command==='npm'&&args[0]==='install')kind='npm';else if(command==='git'&&args[0]==='clone'){kind='git';fs.mkdirSync(args[2],{recursive:true});}else if(command==='git'&&args[0]==='checkout')return;else throw new Error('unexpected command');
 const response=await fetch(process.env.PIG_TEST_PACKAGE_BARRIER+'/'+kind,{method:'POST',signal:AbortSignal.timeout(10000)});if(!response.ok)throw new Error('barrier failed');
 })().catch(e=>{console.error(e);process.exitCode=1});`)
	userSources := []string{"npm:user-old", "npm:user-current", "npm:user-unknown", "npm:user-pinned@1.0.0", "git:github.com/example/user-repo-a", "git:github.com/example/user-repo-b", "git:github.com/example/user-repo-pinned@v1"}
	projectSources := []string{"npm:project-old", "npm:project-current", "npm:project-missing", "git:github.com/example/project-repo-a"}
	var global, project []codingagent.PackageSource
	for _, s := range userSources {
		global = append(global, codingagent.PackageSource{Source: s})
	}
	for _, s := range projectSources {
		project = append(project, codingagent.PackageSource{Source: s})
	}
	if err := f.settings.SetPackages(global); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.SetProjectPackages(project); err != nil {
		t.Fatal(err)
	}
	userRoot, projectRoot := filepath.Join(f.agent, "npm"), filepath.Join(codingagent.ProjectConfigDir(f.cwd), "npm")
	installed := map[string]string{"user-old": userRoot, "user-current": userRoot, "user-unknown": userRoot, "project-old": projectRoot, "project-current": projectRoot}
	for name, root := range installed {
		raw, _ := json.Marshal(map[string]string{"name": name, "version": "1.0.0"})
		writePackageResource(t, filepath.Join(root, "node_modules", name, "package.json"), string(raw))
	}
	expectedGit := []string{"user-repo-a", "user-repo-b", "user-repo-pinned", "project-repo-a"}
	expectedInstalls := [][]string{{"install", "user-old@latest", "user-unknown@latest", "--prefix", userRoot, "--legacy-peer-deps"}, {"install", "project-old@latest", "project-missing@latest", "--prefix", projectRoot, "--legacy-peer-deps"}}
	var mu sync.Mutex
	counts, active, maxActive := map[string]int{}, map[string]int{}, map[string]int{}
	limits := map[string]int{"npm": len(expectedInstalls), "git": len(expectedGit)}
	gates := map[string]chan struct{}{"npm": make(chan struct{}), "git": make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimPrefix(r.URL.Path, "/")
		gate, ok := gates[kind]
		if !ok {
			http.Error(w, "unknown operation", 400)
			return
		}
		mu.Lock()
		counts[kind]++
		active[kind]++
		maxActive[kind] = max(maxActive[kind], active[kind])
		if counts[kind] == limits[kind] {
			close(gate)
		}
		mu.Unlock()
		select {
		case <-gate:
		case <-r.Context().Done():
		}
		mu.Lock()
		active[kind]--
		mu.Unlock()
	}))
	defer server.Close()
	t.Setenv("PIG_TEST_PACKAGE_BARRIER", server.URL)
	var progress []packagemanager.ProgressEvent
	if err := packageManagerFor(f.cwd, f.settings, func(event packagemanager.ProgressEvent) {
		progress = append(progress, event)
	}).Update(""); err != nil {
		t.Fatal(err)
	}
	// Starts are serialized like Pi's event loop; terminal events arrive before update returns.
	labels := []string{"user npm packages", "project npm packages"}
	for _, pkg := range append(global, project...) {
		if strings.HasPrefix(pkg.Source, "git:") {
			labels = append(labels, pkg.Source)
		}
	}
	if len(progress) != 2*len(labels) {
		t.Fatalf("progress=%+v, want a start and completion for each batch/repository", progress)
	}
	for i, label := range labels {
		if i < 2 && (progress[i].Source != label || progress[i].Type != "start") {
			t.Fatalf("npm start order=%+v", progress)
		}
		var events []packagemanager.ProgressEvent
		for _, event := range progress {
			if event.Source == label {
				events = append(events, event)
			}
		}
		want := []packagemanager.ProgressEvent{
			{Type: "start", Action: "update", Source: label, Message: new("Updating " + label + "...")},
			{Type: "complete", Action: "update", Source: label},
		}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("%s progress=%+v, want %+v", label, events, want)
		}
	}
	calls := f.calls(t)
	views := map[string]bool{}
	installs := []packageProcessCall{}
	clones := []packageProcessCall{}
	for _, c := range calls {
		switch {
		case c.Command == "npm" && c.Args[0] == "view":
			if views[c.Args[1]] {
				t.Fatalf("duplicate view: %+v", c)
			}
			views[c.Args[1]] = true
		case c.Command == "npm" && c.Args[0] == "install":
			installs = append(installs, c)
		case c.Command == "git" && c.Args[0] == "clone":
			clones = append(clones, c)
		}
	}
	if len(views) != len(installed) {
		t.Fatalf("views=%v want=%v", views, installed)
	}
	for name := range installed {
		if !views[name] {
			t.Fatalf("missing view %s", name)
		}
	}
	if len(installs) != len(expectedInstalls) {
		t.Fatalf("installs=%+v", installs)
	}
	// Mock call ordinals occur before process scheduling; independent child arrival/completion order is not guaranteed by Pi. Compare every dispatched argv by its owning scope.
	for _, args := range expectedInstalls {
		requirePackageProcessCall(t, installs, "npm", args, "")
	}
	if len(clones) != len(expectedGit) {
		t.Fatalf("git updates=%+v", clones)
	}
	for _, repo := range expectedGit {
		root := f.agent
		if strings.HasPrefix(repo, "project-") {
			root = codingagent.ProjectConfigDir(f.cwd)
		}
		requirePackageProcessCall(t, clones, "git", []string{"clone", "https://github.com/example/" + repo, filepath.Join(root, "git", "github.com", "example", repo)}, "")
	}
	requirePackageProcessCall(t, calls, "git", []string{"checkout", "v1"}, filepath.Join(f.agent, "git", "github.com", "example", "user-repo-pinned"))
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(counts, limits) || maxActive["npm"] <= 1 || maxActive["git"] <= 1 || active["npm"] != 0 || active["git"] != 0 {
		t.Fatalf("counts=%v max=%v active=%v", counts, maxActive, active)
	}
	fmt.Fprintln(os.Stdout, "PACKAGE_BATCH npm=2 git=4 checks=5; both concurrent; all joined")
}
