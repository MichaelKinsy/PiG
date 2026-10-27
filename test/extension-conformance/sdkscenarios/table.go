package sdkscenarios

import "slices"

// All is the table: every scenario, in group order.
func All() []Scenario {
	var all []Scenario
	all = append(all, providersAuth()...)
	all = append(all, hostEvents()...)
	all = append(all, abortRule()...)
	all = append(all, registrations()...)
	all = append(all, contextScenarios()...)
	return slices.Clone(all)
}
