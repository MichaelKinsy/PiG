package ai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// readMemberOrderCase is an auth.json whose credential members are not in the codec's struct order. Pi's read() returns the JSON.parse'd object, so a read
// credential serializes with the file's member order (packages/coding-agent/src/core/auth-storage.ts:443-448 AuthStorage.read, :498-507 readStoredCredential,
// :282-285 ReadOnlyAuthStorage.read at f6600b58f347). Provider-owned members the typed form does not model keep their place.
type readMemberOrderCase struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Provider string `json:"provider"`
	// Want is the entry as JSON.stringify writes the credential read from the file.
	Want string `json:"-"`
	// WritableWant is the writable store's result when it differs: it resolves a command key, as Pi's AuthStorage.read does.
	WritableWant string `json:"-"`
}

var readMemberOrderCases = []readMemberOrderCase{
	{Name: "oauth with access first (parity scenario oauth/24)", File: `{"p":{"type":"oauth","access":"a","refresh":"r","expires":123}}`, Provider: "p",
		Want: `{"type":"oauth","access":"a","refresh":"r","expires":123}`},
	{Name: "oauth with an unmodelled member between modelled ones", File: `{"p":{"access":"a","custom":{"z":1,"a":2},"type":"oauth","expires":5,"refresh":"r","projectId":"x"}}`, Provider: "p",
		Want: `{"access":"a","custom":{"z":1,"a":2},"type":"oauth","expires":5,"refresh":"r","projectId":"x"}`},
	{Name: "oauth with the unmodelled member last", File: `{"p":{"expires":5,"refresh":"r","access":"a","type":"oauth","zzz":true}}`, Provider: "p",
		Want: `{"expires":5,"refresh":"r","access":"a","type":"oauth","zzz":true}`},
	{Name: "api key with env members in file order", File: `{"p":{"env":{"B":"2","A":"1"},"key":"k","type":"api_key"}}`, Provider: "p",
		Want: `{"env":{"B":"2","A":"1"},"key":"k","type":"api_key"}`},
	{Name: "api key reference resolves in place", File: `{"p":{"key":"$ORDER_KEY","extra":1,"type":"api_key","env":{"ORDER_KEY":"v"}}}`, Provider: "p",
		Want: `{"key":"v","extra":1,"type":"api_key","env":{"ORDER_KEY":"v"}}`},
	{Name: "api key command is not resolved", File: `{"p":{"key":"!echo x","type":"api_key","note":"n"}}`, Provider: "p",
		Want: `{"key":"!echo x","type":"api_key","note":"n"}`, WritableWant: `{"key":"x","type":"api_key","note":"n"}`},
	{Name: "integer-like members come first", File: `{"p":{"type":"api_key","key":"k","b":1,"2":"x","1":"y"}}`, Provider: "p",
		Want: `{"1":"y","2":"x","type":"api_key","key":"k","b":1}`},
	{Name: "catalog members keep their place", File: `{"p":{"availableModelIds":["m"],"type":"oauth","scope":"s","gatewayConfig":{"a":1},"access":"a","refresh":"r","expires":7}}`, Provider: "p",
		Want: `{"availableModelIds":["m"],"type":"oauth","scope":"s","gatewayConfig":{"a":1},"access":"a","refresh":"r","expires":7}`},
}

func (c readMemberOrderCase) writableWant() string {
	if c.WritableWant != "" {
		return c.WritableWant
	}
	return c.Want
}

func writeReadMemberOrderFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent", "auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readMemberOrderGo reads each case through the read-only and the writable store and returns the serialized credentials.
func readMemberOrderGo(t *testing.T, c readMemberOrderCase) (readOnly, writable string) {
	t.Helper()
	t.Setenv("ORDER_KEY", "env-value")
	path := writeReadMemberOrderFile(t, c.File)
	ctx := context.Background()
	serialize := func(store CredentialStore) string {
		credential, err := store.Read(ctx, c.Provider)
		if err != nil || credential == nil {
			t.Fatalf("%s: read = %v, %v", c.Name, credential, err)
		}
		data, err := json.Marshal(credential)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	auth, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return serialize(NewReadOnlyAuthStorage(path)), serialize(auth)
}

// TestReadCredentialKeepsFileMemberOrder checks both stores against the literal JSON.stringify of Pi's read().
func TestReadCredentialKeepsFileMemberOrder(t *testing.T) {
	for _, c := range readMemberOrderCases {
		t.Run(c.Name, func(t *testing.T) {
			readOnly, writable := readMemberOrderGo(t, c)
			if readOnly != c.Want {
				t.Errorf("read-only store\n got  %s\n want %s", readOnly, c.Want)
			}
			if writable != c.writableWant() {
				t.Errorf("writable store\n got  %s\n want %s", writable, c.writableWant())
			}
		})
	}
}

// TestReadCredentialMemberOrderMatchesPi runs Pi's own read() of AuthStorage.create and ReadOnlyAuthStorage over the same files and compares the bytes.
func TestReadCredentialMemberOrderMatchesPi(t *testing.T) {
	t.Setenv("ORDER_KEY", "env-value")
	var want []struct {
		ReadOnly string `json:"readOnly"`
		Writable string `json:"writable"`
	}
	pioracle.Run(t, piCommandsWithoutTimeout+`
const { ReadOnlyAuthStorage, AuthStorage } = await load("pi-coding-agent/core/auth-storage.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "authorder-"));
	const file = path.join(root, "agent", "auth.json");
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(file, c.file);
	const readOnly = JSON.stringify(await new ReadOnlyAuthStorage(file).read(c.provider));
	const writable = JSON.stringify(await AuthStorage.create(file).read(c.provider));
	out.push({ readOnly, writable });
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, readMemberOrderCases, &want, "ORDER_KEY=env-value")
	if len(want) != len(readMemberOrderCases) {
		t.Fatalf("Pi returned %d rows for %d cases", len(want), len(readMemberOrderCases))
	}
	for i, c := range readMemberOrderCases {
		readOnly, writable := readMemberOrderGo(t, c)
		if want[i].ReadOnly != c.Want || want[i].Writable != c.writableWant() {
			t.Errorf("%s: Pi writes %s / %s, the table says %s / %s", c.Name, want[i].ReadOnly, want[i].Writable, c.Want, c.writableWant())
		}
		if readOnly != want[i].ReadOnly {
			t.Errorf("%s: read-only store\n Go %s\n Pi %s", c.Name, readOnly, want[i].ReadOnly)
		}
		if writable != want[i].Writable {
			t.Errorf("%s: writable store\n Go %s\n Pi %s", c.Name, writable, want[i].Writable)
		}
	}
}

// TestInMemoryReadKeepsDecodedMemberOrder: AuthStorage.inMemory(data).read(provider) returns the object it was seeded with (auth-storage.ts:443-448), so a
// credential decoded from JSON and seeded into the in-memory store serializes in the decoded order.
func TestInMemoryReadKeepsDecodedMemberOrder(t *testing.T) {
	t.Setenv("ORDER_KEY", "env-value")
	var want []string
	pioracle.Run(t, piCommandsWithoutTimeout+`
const { AuthStorage } = await load("pi-coding-agent/core/auth-storage.js");
const out = [];
for (const c of input) {
	const data = JSON.parse(c.file);
	out.push(JSON.stringify(await AuthStorage.inMemory(data).read(c.provider)));
}
emit(out);`, readMemberOrderCases, &want, "ORDER_KEY=env-value")
	if len(want) != len(readMemberOrderCases) {
		t.Fatalf("Pi returned %d rows for %d cases", len(want), len(readMemberOrderCases))
	}
	for i, c := range readMemberOrderCases {
		var seed map[string]Credential
		if err := json.Unmarshal([]byte(c.File), &seed); err != nil {
			t.Fatal(err)
		}
		credential, err := NewInMemoryAuthStorage(seed).Read(context.Background(), c.Provider)
		if err != nil || credential == nil {
			t.Fatalf("%s: read = %v, %v", c.Name, credential, err)
		}
		got, err := json.Marshal(credential)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want[i] {
			t.Errorf("%s: in-memory store\n Go %s\n Pi %s", c.Name, got, want[i])
		}
	}
}

// TestRefreshedCredentialKeepsFileMemberOrder: a refresh that returns the stored credential with new tokens ({ ...current, access, expires }) persists the
// members in the file's order (resolve.ts refreshStoredOAuthCredential: credentials.modify writes what oauth.refresh returned, JSON.stringify keeps its order).
func TestRefreshedCredentialKeepsFileMemberOrder(t *testing.T) {
	files := []string{
		`{"p":{"type":"oauth","access":"a","refresh":"r","expires":123}}`,
		`{"p":{"access":"a","custom":{"z":1,"a":2},"type":"oauth","expires":5,"refresh":"r"}}`,
		`{"p":{"expires":5,"refresh":"r","access":"a","type":"oauth","scope":"s"}}`,
	}
	var want []string
	pioracle.Run(t, piCommandsWithoutTimeout+`
const { AuthStorage } = await load("pi-coding-agent/core/auth-storage.js");
const { refreshStoredOAuthCredential } = await load("pi-ai/auth/resolve.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const content of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "authrefresh-"));
	const file = path.join(root, "agent", "auth.json");
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(file, content);
	const oauth = { refresh: async (c) => ({ ...c, access: "new", expires: 99 }) };
	await refreshStoredOAuthCredential(AuthStorage.create(file), "p", oauth, () => true, new AbortController().signal);
	out.push(fs.readFileSync(file, "utf-8"));
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, files, &want)
	if len(want) != len(files) {
		t.Fatalf("Pi returned %d rows for %d files", len(want), len(files))
	}
	oauth := &OAuthAuth{Refresh: func(_ context.Context, current Credential) (Credential, error) {
		current.Access = "new"
		current.SetExpiresMillis(99)
		return current, nil
	}}
	for i, content := range files {
		path := writeReadMemberOrderFile(t, content)
		store, err := NewAuthStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := RefreshStoredOAuth(context.Background(), store, "p", oauth, func(Credential) bool { return true }); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want[i] {
			t.Errorf("file %d after a refresh\n Go %s\n Pi %s", i, got, want[i])
		}
	}
}
