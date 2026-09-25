package standardlogin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVariantStateDefaultsAndPersists(t *testing.T) {
	root := t.TempDir()
	if got := loadVariant(root); got.ID != defaultVariantID {
		t.Fatalf("default variant = %q, want %q", got.ID, defaultVariantID)
	}
	if err := saveVariant(root, "green"); err != nil {
		t.Fatal(err)
	}
	if got := loadVariant(root); got.ID != "green" {
		t.Fatalf("persisted variant = %q, want green", got.ID)
	}
	info, err := os.Stat(statePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
	if filepath.Dir(statePath(root)) != filepath.Join(root, "state", "pig-standard") {
		t.Fatalf("state path = %s", statePath(root))
	}
}

func TestUnknownSavedVariantFallsBackToDefault(t *testing.T) {
	root := t.TempDir()
	path := statePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"variant":"unknown"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadVariant(root); got.ID != defaultVariantID {
		t.Fatalf("unknown saved variant = %q, want %q", got.ID, defaultVariantID)
	}
}

func TestEveryVariantProducesCompleteLoginDefinition(t *testing.T) {
	for _, variant := range Variants {
		definition := LoginDefinitionFor(variant)
		if len(definition.Brand) != 5 || len(definition.Hero) != 14 || len(definition.Mascot) != 14 {
			t.Fatalf("%s grid heights = brand %d, hero %d, mascot %d", variant.ID, len(definition.Brand), len(definition.Hero), len(definition.Mascot))
		}
		for _, row := range definition.Brand {
			if len(row) != 41 {
				t.Fatalf("%s brand width = %d, want 41", variant.ID, len(row))
			}
		}
		for _, row := range definition.Hero {
			if len(row) != 32 {
				t.Fatalf("%s hero width = %d, want 32", variant.ID, len(row))
			}
		}
		for _, row := range definition.Mascot {
			if len(row) != 16 {
				t.Fatalf("%s mascot width = %d, want 16", variant.ID, len(row))
			}
		}
	}
}

func TestNoVariantDrawsTheSmallBrandWordmark(t *testing.T) {
	for _, variant := range Variants {
		definition := LoginDefinitionFor(variant)
		for y, row := range definition.Brand {
			if strings.Trim(row, ".") != "" {
				t.Fatalf("%s brand row %d draws pixels: %q", variant.ID, y, row)
			}
		}
	}
}
