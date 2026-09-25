package piglet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/secretresolver"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var secretResolverSequence atomic.Uint64

func TestAC7PigletSecretContract(t *testing.T) {
	scheme := fmt.Sprintf("ac7fixture-%d", secretResolverSequence.Add(1))
	if err := secretresolver.Register(scheme, func(_ context.Context, opaque string) ([]byte, error) {
		if opaque == "denied" {
			return nil, errors.New("unauthorized")
		}
		return []byte("REF-SECRET"), nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AC7_ENV_SECRET", "ENV-SECRET")
	file := filepath.Join(t.TempDir(), "secret")
	writeOwnerOnlySecret(t, file, []byte("FILE-SECRET\n"))
	pigletYAML := "name: secrets\nsecrets:\n" +
		"  - name: env-secret\n    from: {env: AC7_ENV_SECRET}\n" +
		"  - name: file-secret\n    from: {file: " + file + "}\n" +
		"  - name: ref-secret\n    from: {ref: " + scheme + ":key}\n" +
		"agentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: env-secret\n      target: {env: ENV_TOKEN}\n    - secretRef: file-secret\n      target: {env: FILE_TOKEN}\n    - secretRef: ref-secret\n      target: {env: REF_TOKEN}\n"
	p, err := ParseBytes([]byte(pigletYAML))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveRequiredSecrets(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := resolved.AgentEnvironment(p)
	if err != nil {
		t.Fatal(err)
	}
	if environment["ENV_TOKEN"] != "ENV-SECRET" || environment["FILE_TOKEN"] != "FILE-SECRET" || environment["REF_TOKEN"] != "REF-SECRET" {
		t.Fatalf("agent environment = %#v", environment)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ENV-SECRET", "FILE-SECRET", "REF-SECRET"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("Piglet inspection leaked secret value %q", secret)
		}
	}
}

func TestAC7PigletSecretsRejectLiteralAndInvalidForms(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"multiple source arms", "name: bad\nsecrets:\n  - name: token\n    from: {env: TOKEN, ref: x:y}\n", "exactly one"},
		{"relative file", "name: bad\nsecrets:\n  - name: token\n    from: {file: ./secret}\n", "absolute or home-relative"},
		{"undeclared consumer", "name: bad\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: missing\n      target: {env: TOKEN}\n", "not declared"},
		{"duplicate target", "name: bad\nsecrets:\n  - name: a\n    from: {env: A}\n  - name: b\n    from: {env: B}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: a\n      target: {env: TOKEN}\n    - secretRef: b\n      target: {env: TOKEN}\n", "duplicates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseBytes([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAC7PigletSecretsFailClosedAtResolution(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		p, err := ParseBytes([]byte("name: missing\nsecrets:\n  - name: token\n    from: {env: AC7_MISSING}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: token\n      target: {env: TOKEN}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveRequiredSecrets(context.Background(), p); err == nil || !strings.Contains(err.Error(), "missing or empty") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("unsafe file permissions", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "secret")
		writeOwnerOnlySecret(t, file, []byte("SECRET"))
		testenv.GrantOthersRead(t, file)
		p, err := ParseBytes([]byte("name: unsafe\nsecrets:\n  - name: token\n    from: {file: " + file + "}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: token\n      target: {env: TOKEN}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveRequiredSecrets(context.Background(), p); err == nil || !strings.Contains(err.Error(), "owner-only") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing resolver", func(t *testing.T) {
		p, err := ParseBytes([]byte("name: noresolver\nsecrets:\n  - name: token\n    from: {ref: no-such-resolver:key}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: token\n      target: {env: TOKEN}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveRequiredSecrets(context.Background(), p); err == nil || !strings.Contains(err.Error(), "install the package") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestAC7PigletSecretInspectionRedactsSourceDetails(t *testing.T) {
	p := &Piglet{
		Name: "inspect",
		Secrets: []SecretDeclaration{
			{Name: "file-secret", From: SecretSource{File: "/private/machine/secret"}},
			{Name: "ref-secret", From: SecretSource{Ref: "provider:opaque-private-id"}},
		},
	}
	value, err := pigletJSONValue(p)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	output := string(encoded)
	if strings.Contains(output, "/private/machine/secret") || strings.Contains(output, "opaque-private-id") {
		t.Fatalf("inspection leaked secret source details: %s", output)
	}
	if !strings.Contains(output, `"name":"file-secret"`) || !strings.Contains(output, `"source":"file"`) || !strings.Contains(output, `"source":"ref"`) {
		t.Fatalf("inspection metadata = %s", output)
	}
}

func TestResolvedInnerSecretIsConsumedAndRemovedFromEnvironment(t *testing.T) {
	p, err := ParseBytes([]byte("name: inner\nsecrets:\n  - name: token\n    from: {env: HOST_ONLY}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: token\n      target: {env: TOKEN}\n"))
	if err != nil {
		t.Fatal(err)
	}
	environmentName := resolvedSecretEnvironmentName("token")
	t.Setenv(agentEnvironmentActiveEnv, "sha256:active")
	t.Setenv(environmentName, "INNER-SECRET")
	resolved, err := ResolveRequiredSecrets(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Clear()
	if _, exists := os.LookupEnv(environmentName); exists {
		t.Fatalf("synthetic secret environment %s remained available", environmentName)
	}
}
