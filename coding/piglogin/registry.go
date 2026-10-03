package piglogin

import (
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pig divergence (D2): extensions add sprites to the catalogue (ctx.ui.registerSprite). They follow the built-in sprites in
// /sprite in the order they were first registered, and leave it when their extension unloads. A saved selection of a
// sprite whose extension is not loaded draws the default sprite and stays saved, so the sprite returns with its extension.
var registry struct {
	mu      sync.Mutex
	entries []registeredSprite
}

type registeredSprite struct {
	owner   string
	variant Variant
}

// Register adds an extension's sprite, or replaces the sprite with the same ID that the same extension registered. An ID
// taken by a built-in sprite or by another extension's sprite is an error.
// pig divergence (D2): extension sprites join the built-in ones in /sprite; Pi has no sprites.
func Register(owner string, definition extension.ValidatedSpriteDefinition) error {
	id := definition.ID()
	for _, variant := range Variants {
		if variant.ID == id {
			return fmt.Errorf("sprite %q is a built-in sprite", id)
		}
	}
	variant := variantFromDefinition(definition)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for i, entry := range registry.entries {
		if entry.variant.ID != id {
			continue
		}
		if entry.owner != owner {
			return fmt.Errorf("sprite %q is registered by another extension", id)
		}
		registry.entries[i].variant = variant
		return nil
	}
	registry.entries = append(registry.entries, registeredSprite{owner: owner, variant: variant})
	return nil
}

// Unregister removes every sprite owner registered.
func Unregister(owner string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.entries = slices.DeleteFunc(registry.entries, func(entry registeredSprite) bool { return entry.owner == owner })
}

// All lists every sprite /sprite offers: the built-in catalogue, then the registered sprites.
func All() []Variant {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	all := make([]Variant, 0, len(Variants)+len(registry.entries))
	all = append(all, Variants...)
	for _, entry := range registry.entries {
		all = append(all, entry.variant)
	}
	return all
}

func registeredByID(id string) (Variant, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, entry := range registry.entries {
		if entry.variant.ID == id {
			return entry.variant, true
		}
	}
	return Variant{}, false
}

// variantFromDefinition is the catalogue entry of a registered sprite: its grids and its palette over the default sprite's
// colors, and the default's wordmark.
func variantFromDefinition(definition extension.ValidatedSpriteDefinition) Variant {
	variant := Default()
	variant.ID, variant.Name, variant.Tagline = definition.ID(), definition.Name(), definition.Tagline()
	variant.Sprite = definition.Mascot()
	variant.PaletteOverrides = definition.Palette()
	return variant
}
