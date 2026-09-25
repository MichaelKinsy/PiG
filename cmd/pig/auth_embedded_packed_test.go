package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/cellpack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type packedAuthFixture struct {
	cell          cellpack.LoadedCell
	owner         string
	sibling       string
	provider      string
	siblingMarker string
	handlerMarker string
	loginMarker   string
}

func TestEmbeddedPackedAuthDiscoveryAndLoginByLanguage(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) packedAuthFixture
	}{
		{name: "go", build: buildGoPackedAuthFixture},
		{name: "rust", build: buildRustPackedAuthFixture},
		{name: "python", build: buildPythonPackedAuthFixture},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			t.Cleanup(ai.ResetOAuthProviders)
			fixture := tc.build(t)
			if fixture.cell.Extensions[0].Name != fixture.sibling || fixture.cell.Extensions[1].Name != fixture.owner {
				t.Fatalf("packed member order = %#v, want failing sibling before owner", fixture.cell.Extensions)
			}
			selected := selectEmbeddedOwnerCells([]cellpack.LoadedCell{fixture.cell}, fixture.owner)
			if len(selected) != 1 || selected[0].BinaryPath != fixture.cell.BinaryPath || selected[0].Key != fixture.cell.Key || len(selected[0].Extensions) != 1 || selected[0].Extensions[0].Name != fixture.owner {
				t.Fatalf("owner selection changed compiled artifact or packed-cell identity: selected=%#v source=%#v", selected, fixture.cell)
			}
			t.Setenv("PIG_TEST_AUTH_SIBLING_FACTORY", fixture.siblingMarker)
			t.Setenv("PIG_TEST_AUTH_HANDLER", fixture.handlerMarker)
			t.Setenv("PIG_TEST_AUTH_LOGIN", fixture.loginMarker)
			t.Setenv("PIG_TEST_AUTH_EXIT_AFTER_LOGIN", "1")

			discoveryCtx, discoveryCancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			discovery := &authContributionRegistry{
				cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{},
				configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{fixture.owner: {}, fixture.sibling: {}},
				embeddedCells: []cellpack.LoadedCell{fixture.cell}, loaded: map[string]struct{}{}, ctx: discoveryCtx, cancel: discoveryCancel,
			}
			if err := discovery.inspectEmbedded([]cellpack.LoadedCell{fixture.cell}); err != nil {
				t.Fatal(err)
			}
			target, found := discovery.target(fixture.provider)
			if !found || target.Extension != fixture.owner {
				t.Fatalf("independent discovery target = %#v, %t; want owner %q", target, found, fixture.owner)
			}
			if len(discovery.diagnostics) != 1 || discovery.diagnostics[0].Extension != fixture.sibling {
				t.Fatalf("independent discovery diagnostics = %#v, want isolated sibling failure", discovery.diagnostics)
			}
			if _, err := os.Stat(fixture.siblingMarker); err != nil {
				t.Fatalf("independent discovery did not exercise failing sibling: %v", err)
			}
			discovery.close()
			if _, registered := ai.GetOAuthProvider(fixture.provider); registered {
				t.Fatal("independent discovery shutdown left the owner provider registered")
			}
			if err := os.Remove(fixture.siblingMarker); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			registry := &authContributionRegistry{
				cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{},
				configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{fixture.owner: {}, fixture.sibling: {}},
				embeddedCells: []cellpack.LoadedCell{fixture.cell}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel,
			}
			defer registry.close()

			if err := registry.inspectEmbedded([]cellpack.LoadedCell{fixture.cell}, fixture.provider); err != nil {
				t.Fatal(err)
			}
			target, found = registry.target(fixture.provider)
			if !found || target.Extension != fixture.owner {
				t.Fatalf("discovered target = %#v, %t; want owner %q", target, found, fixture.owner)
			}
			if len(registry.diagnostics) != 1 || registry.diagnostics[0].Extension != fixture.sibling {
				t.Fatalf("targeted inspection diagnostics = %#v, want isolated sibling failure", registry.diagnostics)
			}
			if _, err := os.Stat(fixture.siblingMarker); err != nil {
				t.Fatalf("targeted discovery did not isolate failing sibling: %v", err)
			}
			if _, registered := ai.GetOAuthProvider(fixture.provider); registered {
				t.Fatal("inspection shutdown left the owner provider registered")
			}
			if _, err := os.Stat(fixture.handlerMarker); !os.IsNotExist(err) {
				t.Fatalf("registration inspection dispatched a lifecycle/tool/command handler: %v", err)
			}
			if err := os.Remove(fixture.siblingMarker); err != nil {
				t.Fatal(err)
			}

			provider, err := registry.load(fixture.provider)
			if err != nil {
				t.Fatal(err)
			}
			if provider.ID() != fixture.provider {
				t.Fatalf("loaded provider = %q, want %q", provider.ID(), fixture.provider)
			}
			if registry.host.ExtensionCount() != 1 || registry.host.Extensions()[0].Name != fixture.owner {
				t.Fatalf("owner launch registry = %#v", registry.host.Extensions())
			}
			if _, err := os.Stat(fixture.siblingMarker); !os.IsNotExist(err) {
				t.Fatalf("owner login activated failing sibling factory: %v", err)
			}
			if _, err := os.Stat(fixture.handlerMarker); !os.IsNotExist(err) {
				t.Fatalf("owner launch dispatched a lifecycle/tool/command handler: %v", err)
			}

			credentials, err := provider.Login(ai.OAuthLoginCallbacks{})
			if err != nil {
				t.Fatal(err)
			}
			if credentials.Access != "owner-access" {
				t.Fatalf("login access = %q, want owner-access", credentials.Access)
			}
			if _, err := os.Stat(fixture.loginMarker); err != nil {
				t.Fatalf("owner OAuth handler did not run: %v", err)
			}
			if _, err := os.Stat(fixture.handlerMarker); !os.IsNotExist(err) {
				t.Fatalf("login dispatched a lifecycle/tool/command handler: %v", err)
			}

			deadline := time.Now().Add(testbudget.Wait(t))
			for time.Now().Before(deadline) {
				_, registered := ai.GetOAuthProvider(fixture.provider)
				if !registered && registry.host != nil && registry.host.QuarantinedCells()[fixture.cell.Key] == "packed process exited" {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			_, registered := ai.GetOAuthProvider(fixture.provider)
			t.Fatalf("owner process failure state: provider registered=%t quarantine=%q", registered, registry.host.QuarantinedCells()[fixture.cell.Key])
		})
	}
}

func buildGoPackedAuthFixture(t *testing.T) packedAuthFixture {
	t.Helper()
	owner, sibling := "owner-go-auth", "sibling-go-auth"
	provider := "provider-go-auth"
	ownerRoot := t.TempDir()
	siblingRoot := t.TempDir()
	writeGoModule(t, ownerRoot, "example.com/owner/goauth", fmt.Sprintf(`package owner
import (
 "os"
 "time"
 sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)
func mark(env, text string) { if path := os.Getenv(env); path != "" { f, _ := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); if f != nil { _, _ = f.WriteString(text+"\n"); _ = f.Close() } } }
func Extension() *sdk.Extension {
 ext := sdk.New(%q)
 ext.Tool("must-not-run", "probe", sdk.Schema{"type":"object"}, func(sdk.Context, map[string]any) (any,error) { mark("PIG_TEST_AUTH_HANDLER", "tool"); return nil,nil })
 ext.Command("must-not-run", "probe", func(sdk.Context,string) error { mark("PIG_TEST_AUTH_HANDLER", "command"); return nil })
 ext.OnSessionStart(func(sdk.Context,map[string]any) (any,error) { mark("PIG_TEST_AUTH_HANDLER", "session"); return nil,nil })
 ext.RegisterProvider(%q, sdk.ProviderConfig{"name":%q, "oauth":&sdk.OAuthProvider{Name:%q, Login:func(*sdk.OAuthLoginCallbacks)(sdk.OAuthCredentials,error){ mark("PIG_TEST_AUTH_LOGIN", "login"); if os.Getenv("PIG_TEST_AUTH_EXIT_AFTER_LOGIN") == "1" { go func(){ time.Sleep(150*time.Millisecond); os.Exit(0) }() }; return sdk.OAuthCredentials{Access:"owner-access"},nil }}})
 return ext
}
`, owner, provider, provider, provider))
	writeGoModule(t, siblingRoot, "example.com/sibling/goauth", fmt.Sprintf(`package sibling
import ("os"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension() *sdk.Extension { if path:=os.Getenv("PIG_TEST_AUTH_SIBLING_FACTORY"); path!="" { f,_:=os.OpenFile(path,os.O_CREATE|os.O_WRONLY|os.O_APPEND,0600); if f!=nil { _,_=f.WriteString("factory\n"); _=f.Close() } }; panic("sibling factory failure"); return sdk.New(%q) }
`, sibling))
	cell, err := runtimecell.BuildGoPackedCell(t.Context(), t.TempDir(), "go-auth-cell", []runtimecell.GoExtension{
		{Name: owner, Root: ownerRoot, ModulePath: "example.com/owner/goauth", Package: "example.com/owner/goauth", Factory: "Extension", Hash: "owner"},
		{Name: sibling, Root: siblingRoot, ModulePath: "example.com/sibling/goauth", Package: "example.com/sibling/goauth", Factory: "Extension", Hash: "sibling"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return newPackedAuthFixture(t, "go", cell.Key, string(subprocess.CellStrategyPackedGo), cell.BinaryPath, owner, sibling, provider)
}

func writeGoModule(t *testing.T, root, module, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildRustPackedAuthFixture(t *testing.T) packedAuthFixture {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo not found: %v", err)
	}
	owner, sibling := "owner-rust-auth", "sibling-rust-auth"
	provider := "provider-rust-auth"
	ownerRoot := writeRustAuthCrate(t, "owner_rust_auth", fmt.Sprintf(`use pig_sdk::{empty_schema, CommandResult, Extension, OAuthCredentials, OAuthProvider, ToolResult};
use serde_json::json;
fn mark(env: &str, text: &str) { if let Ok(path)=std::env::var(env) { use std::io::Write; if let Ok(mut f)=std::fs::OpenOptions::new().create(true).append(true).open(path) { let _=writeln!(f,"{}",text); } } }
pub fn new_extension() -> Extension {
 let mut ext=Extension::new(%q);
 ext.tool("must-not-run","probe",empty_schema(),|_,_| { mark("PIG_TEST_AUTH_HANDLER","tool"); ToolResult::text("no") });
 ext.command("must-not-run","probe",|_,_| { mark("PIG_TEST_AUTH_HANDLER","command"); CommandResult::Ok });
 ext.on_event("session_start",false,|_,_| { mark("PIG_TEST_AUTH_HANDLER","session"); None });
 ext.register_oauth_provider(%q,json!({"name":%q}),OAuthProvider{name:%q.to_string(),is_subscription:false,login:Box::new(|_| { mark("PIG_TEST_AUTH_LOGIN","login"); if std::env::var("PIG_TEST_AUTH_EXIT_AFTER_LOGIN").as_deref()==Ok("1") { std::thread::spawn(|| { std::thread::sleep(std::time::Duration::from_millis(150)); std::process::exit(0); }); } Ok(OAuthCredentials{access:"owner-access".to_string(),..Default::default()}) }),refresh_token:None,get_api_key:None,credential_store:None});
 ext
}
`, owner, provider, provider, provider))
	siblingRoot := writeRustAuthCrate(t, "sibling_rust_auth", fmt.Sprintf(`use pig_sdk::Extension;
pub fn new_extension() -> Extension { if let Ok(path)=std::env::var("PIG_TEST_AUTH_SIBLING_FACTORY") { use std::io::Write; if let Ok(mut f)=std::fs::OpenOptions::new().create(true).append(true).open(path) { let _=writeln!(f,"factory"); } } panic!("sibling factory failure"); #[allow(unreachable_code)] Extension::new(%q) }
`, sibling))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	cell, err := runtimecell.BuildRustPackedCell(ctx, t.TempDir(), "rust-auth-cell", []runtimecell.RustExtension{
		{Name: owner, Root: ownerRoot, Package: "owner_rust_auth", Factory: "new_extension", Hash: "owner"},
		{Name: sibling, Root: siblingRoot, Package: "sibling_rust_auth", Factory: "new_extension", Hash: "sibling"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return newPackedAuthFixture(t, "rust", cell.Key, string(subprocess.CellStrategyPackedRust), cell.BinaryPath, owner, sibling, provider)
}

func writeRustAuthCrate(t *testing.T, name, source string) string {
	t.Helper()
	root := t.TempDir()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-rs"))
	if err != nil {
		t.Fatal(err)
	}
	cargo := fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", name, filepath.ToSlash(sdkRoot))
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "lib.rs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func buildPythonPackedAuthFixture(t *testing.T) packedAuthFixture {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not found: %v", err)
	}
	owner, sibling := "owner-python-auth", "sibling-python-auth"
	provider := "provider-python-auth"
	ownerRoot := t.TempDir()
	ownerSource := fmt.Sprintf(`import os, pathlib, threading
import pig_sdk
def mark(env,text):
    path=os.environ.get(env)
    if path:
        with open(path,"a") as f: f.write(text+"\n")
def new_extension():
    ext=pig_sdk.Extension(%q)
    ext.tool("must-not-run","probe",{"type":"object"},lambda ctx,args: mark("PIG_TEST_AUTH_HANDLER","tool"))
    ext.command("must-not-run","probe",lambda ctx,args: mark("PIG_TEST_AUTH_HANDLER","command"))
    ext.on_event("session_start",lambda ctx,data: mark("PIG_TEST_AUTH_HANDLER","session"))
    def login(_cb):
        mark("PIG_TEST_AUTH_LOGIN","login")
        if os.environ.get("PIG_TEST_AUTH_EXIT_AFTER_LOGIN")=="1": threading.Timer(0.15,lambda: os._exit(0)).start()
        return pig_sdk.OAuthCredentials(access="owner-access")
    ext.register_oauth_provider(%q,{"name":%q},pig_sdk.OAuthProvider(name=%q,login=login))
    return ext
`, owner, provider, provider, provider)
	if err := os.WriteFile(filepath.Join(ownerRoot, "owner_python_auth.py"), []byte(ownerSource), 0o644); err != nil {
		t.Fatal(err)
	}
	siblingRoot := t.TempDir()
	siblingSource := fmt.Sprintf(`import os
import pig_sdk
def new_extension():
    path=os.environ.get("PIG_TEST_AUTH_SIBLING_FACTORY")
    if path:
        with open(path,"a") as f: f.write("factory\n")
    raise RuntimeError("sibling factory failure")
    return pig_sdk.Extension(%q)
`, sibling)
	if err := os.WriteFile(filepath.Join(siblingRoot, "sibling_python_auth.py"), []byte(siblingSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cell, err := runtimecell.BuildPythonPackedCell(t.Context(), t.TempDir(), "python-auth-cell", []runtimecell.PythonExtension{
		{Name: owner, Root: ownerRoot, Package: "owner_python_auth", Factory: "new_extension", Hash: "owner"},
		{Name: sibling, Root: siblingRoot, Package: "sibling_python_auth", Factory: "new_extension", Hash: "sibling"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return newPackedAuthFixture(t, "python", cell.Key, string(subprocess.CellStrategyPackedPython), cell.BinaryPath, owner, sibling, provider)
}

func newPackedAuthFixture(t *testing.T, language, key, strategy, binary, owner, sibling, provider string) packedAuthFixture {
	t.Helper()
	return packedAuthFixture{
		cell: cellpack.LoadedCell{Language: language, Key: key, Strategy: strategy, BinaryPath: binary, Extensions: []cellpack.ExtEntry{
			{Name: sibling, Hash: "sibling"},
			{Name: owner, Hash: "owner"},
		}},
		owner: owner, sibling: sibling, provider: provider,
		siblingMarker: filepath.Join(t.TempDir(), "sibling-factory"),
		handlerMarker: filepath.Join(t.TempDir(), "handler"),
		loginMarker:   filepath.Join(t.TempDir(), "login"),
	}
}
