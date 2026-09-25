package standardlogin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const defaultVariantID = "hpe-agentic"

type state struct {
	Variant string `json:"variant"`
}

func Extension() *sdk.Extension {
	ext := sdk.New("piglogin")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		return nil, ctx.SetLogin(LoginDefinitionFor(loadVariant(ctx.ConfigHome())))
	})
	ext.Command("sprite", "Select the PiG Standard login sprite.", selectSprite)
	return ext
}

func selectSprite(ctx sdk.Context, args string) error {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return selectSpriteInteractively(ctx)
	case len(fields) == 1 && fields[0] == "list":
		ctx.Notify(spriteList(), "info")
		return nil
	case len(fields) == 2 && fields[0] == "set":
		return activateVariant(ctx, fields[1])
	default:
		return fmt.Errorf("usage: /sprite [list|set <id>]")
	}
}

func selectSpriteInteractively(ctx sdk.Context) error {
	options := make([]string, len(Variants))
	for i, variant := range Variants {
		options[i] = fmt.Sprintf("%s: %s", variant.Name, variant.Tagline)
	}
	selected, ok, err := ctx.Select("Choose a PiG Standard sprite", options)
	if err != nil || !ok {
		return err
	}
	for i, option := range options {
		if selected == option {
			return activateVariant(ctx, Variants[i].ID)
		}
	}
	return fmt.Errorf("unknown sprite selection %q", selected)
}

func activateVariant(ctx sdk.Context, id string) error {
	variant, ok := variantByID(id)
	if !ok {
		return fmt.Errorf("unknown sprite %q; available: %s", id, variantIDs())
	}
	if err := saveVariant(ctx.ConfigHome(), variant.ID); err != nil {
		return err
	}
	return ctx.SetLogin(LoginDefinitionFor(variant))
}

func variantByID(id string) (Variant, bool) {
	for _, variant := range Variants {
		if variant.ID == id {
			return variant, true
		}
	}
	return Variant{}, false
}

func variantIDs() string {
	ids := make([]string, len(Variants))
	for i, variant := range Variants {
		ids[i] = variant.ID
	}
	return strings.Join(ids, ", ")
}

func spriteList() string {
	lines := make([]string, len(Variants))
	for i, variant := range Variants {
		lines[i] = fmt.Sprintf("%s: %s: %s", variant.ID, variant.Name, variant.Tagline)
	}
	return strings.Join(lines, "\n")
}

func loadVariant(configHome string) Variant {
	data, err := os.ReadFile(statePath(configHome))
	if err != nil {
		return FindVariant(defaultVariantID)
	}
	var saved state
	if json.Unmarshal(data, &saved) != nil {
		return FindVariant(defaultVariantID)
	}
	return FindVariant(saved.Variant)
}

func saveVariant(configHome, id string) error {
	dir := filepath.Dir(statePath(configHome))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create login state directory: %w", err)
	}
	data, err := json.Marshal(state{Variant: id})
	if err != nil {
		return fmt.Errorf("encode login state: %w", err)
	}
	if err := os.WriteFile(statePath(configHome), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write login state: %w", err)
	}
	return nil
}

func statePath(configHome string) string {
	return filepath.Join(configHome, "state", "pig-standard", "login.json")
}
