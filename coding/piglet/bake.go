package piglet

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Bake returns the Piglet definition a Piglet Binary embeds for the effective
// Piglet p, whose source file is in pigletDir. The baked definition carries the
// agent shape (extensions, skills, MCP, scoping, discovery) so the Binary can
// assemble the intended agent. Prompt files and skills are inlined; release,
// build, slot, and package metadata and extension source origins are dropped
// because the runnable members come from the embedded cell manifest. Its
// digest is the Piglet digest a Binary's records and signed manifest name.
func Bake(p *Piglet, pigletDir string) ([]byte, error) {
	baked := Clone(p)
	baked.Build = nil
	// The frontend member is fused into the Binary; its source path means
	// nothing to the Binary's own Piglet.
	baked.Slots = nil
	baked.Release = nil
	if baked.SystemPrompt != nil {
		sp, err := bakePromptRef(*baked.SystemPrompt, pigletDir, "system prompt")
		if err != nil {
			return nil, err
		}
		baked.SystemPrompt = &sp
	}
	if err := bakeSkills(baked); err != nil {
		return nil, err
	}
	baked.Packages = nil
	for i := range baked.Extensions {
		baked.Extensions[i].Origins = nil
	}
	data, err := yaml.Marshal(baked)
	if err != nil {
		return nil, err
	}
	if _, err := ParseBytes(data); err != nil {
		return nil, err
	}
	return data, nil
}

func bakePromptRef(ref PromptRef, pigletDir, label string) (PromptRef, error) {
	if ref.File == "" {
		return ref, nil
	}
	promptPath := ref.File
	if !filepath.IsAbs(promptPath) {
		promptPath = filepath.Join(pigletDir, promptPath)
	}
	data, err := os.ReadFile(promptPath)
	if err != nil {
		return PromptRef{}, fmt.Errorf("bake %s: %w", label, err)
	}
	return PromptRef{Text: string(data)}, nil
}

func bakeSkills(p *Piglet) error {
	resolved, errs := ResolveSkills(p)
	if len(errs) > 0 {
		return errs[0]
	}
	byName := map[string]string{}
	for _, s := range resolved {
		byName[s.Entry.Name] = s.Path
	}
	for i := range p.Skills {
		if p.Skills[i].Content != "" {
			p.Skills[i].Origins = nil
			continue
		}
		path := byName[p.Skills[i].Name]
		if path == "" {
			return fmt.Errorf("bake skill %q: explicit origin required", p.Skills[i].Name)
		}
		defs, err := codingagent.LoadSkillsFromPath(path)
		if err != nil {
			return fmt.Errorf("bake skill %q: %w", p.Skills[i].Name, err)
		}
		def, err := chooseSkillDef(p.Skills[i].Name, defs)
		if err != nil {
			return err
		}
		p.Skills[i].Name = def.Name
		p.Skills[i].Description = def.Description
		p.Skills[i].Content = def.Body
		p.Skills[i].Origins = nil
	}
	return nil
}

func chooseSkillDef(name string, defs []*codingagent.SkillDef) (*codingagent.SkillDef, error) {
	if len(defs) == 0 {
		return nil, fmt.Errorf("bake skill %q: no SKILL.md found", name)
	}
	if len(defs) == 1 {
		return defs[0], nil
	}
	for _, def := range defs {
		if def.Name == name {
			return def, nil
		}
	}
	return nil, fmt.Errorf("bake skill %q: origin resolved to %d skills", name, len(defs))
}
