package providerliteral

type cfg struct{ ProviderID string }

func bad(c cfg) bool {
	return c.ProviderID == "github-copilot" // want `provider literal "github-copilot" in shared code`
}

func badSwitch(provider string) int {
	switch provider {
	case "openai": // want `provider literal "openai"`
		return 1
	}
	return 0
}

func other(kind string) bool { return kind == "openai" }
