package pigletbuild

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
)

func buildPigletComponentPlan(cells []subprocess.CellSpec, delivery Plan, inputs []buildInput) (pigletartifact.Plan, error) {
	deliveryByName := make(map[string]ExtPlan, len(delivery.Ext))
	for _, extension := range delivery.Ext {
		deliveryByName[extension.Extension] = extension
	}
	inputByName := make(map[string]buildInput)
	for _, input := range inputs {
		if input.Kind == "extension" {
			inputByName[input.Name] = input
		}
	}

	components := make([]pigletartifact.ComponentInput, 0, len(delivery.Ext))
	for _, cell := range cells {
		for _, config := range cell.Extensions {
			extension, ok := deliveryByName[config.Name]
			if !ok {
				return pigletartifact.Plan{}, fmt.Errorf("component plan: extension %q has no delivery decision", config.Name)
			}
			input, ok := inputByName[config.Name]
			if !ok {
				return pigletartifact.Plan{}, fmt.Errorf("component plan: extension %q has no locked input", config.Name)
			}

			component := pigletartifact.ComponentInput{
				Kind:     "extension",
				Name:     config.Name,
				Language: cell.Language,
				Origin: pigletartifact.Origin{
					Source:  input.Source,
					Version: input.Version,
					Digest:  input.Digest,
					Package: input.Package,
				},
			}
			switch extension.Decision {
			case DecisionFuse:
				component.Fusible = true
			case DecisionSidecar:
				component.Materialization = sidecarMaterialization(cell.Language, extension)
				if cell.Language == string(Python) {
					component.Runtime = &pigletartifact.RuntimeRequirement{Name: "python"}
				}
			default:
				return pigletartifact.Plan{}, fmt.Errorf("component plan: extension %q has unsupported delivery decision %q", config.Name, extension.Decision)
			}
			components = append(components, component)
		}
	}
	return pigletartifact.BuildPlan(components)
}

func cellComponentDisposition(cell subprocess.CellSpec, plan pigletartifact.Plan) (pigletartifact.Realization, pigletartifact.Materialization, error) {
	components := make(map[string]pigletartifact.Component, len(plan.Components))
	for _, component := range plan.Components {
		if component.Kind == pigletartifact.ComponentKindExtension {
			components[component.Name] = component
		}
	}
	var realization pigletartifact.Realization
	var materialization pigletartifact.Materialization
	for i, config := range cell.Extensions {
		component, ok := components[config.Name]
		if !ok {
			return "", "", fmt.Errorf("component plan: extension %q is absent", config.Name)
		}
		if i == 0 {
			realization = component.Realization
			materialization = component.Materialization
			continue
		}
		if component.Realization != realization {
			return "", "", fmt.Errorf("component plan: cell %q has mixed realization", cell.Key)
		}
		if component.Materialization != materialization {
			return "", "", fmt.Errorf("component plan: cell %q has mixed materialization", cell.Key)
		}
	}
	if len(cell.Extensions) == 0 {
		return "", "", fmt.Errorf("component plan: cell %q has no extensions", cell.Key)
	}
	return realization, materialization, nil
}

func sidecarMaterialization(language string, extension ExtPlan) pigletartifact.Materialization {
	if language == string(Python) {
		return pigletartifact.MaterializationExternal
	}
	allNative := len(extension.Compat) > 0
	for _, compatibility := range extension.Compat {
		if compatibility != CompatNative {
			allNative = false
			break
		}
	}
	if allNative {
		return pigletartifact.MaterializationBinary
	}
	return pigletartifact.MaterializationExternal
}
