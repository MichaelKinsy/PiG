package coding

import "testing"

// AgentSession.setModel defaults persist to false, including the cycle path.
func TestSessionModelSwitchDoesNotRewriteGlobalDefault(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "cycle"}[cycle], func(t *testing.T) {
			services := newTestServices(t)
			settings := services.SettingsManager()
			if err := settings.SetDefaultModelAndProvider("original-provider", "original-model"); err != nil {
				t.Fatal(err)
			}
			session, err := NewSession(services, SessionOptions{Model: fakeModel()})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			target := fakeModel()
			target.ID = "selected-model"
			if cycle {
				err = session.CycleToModel(target)
			} else {
				err = session.SetModel(target)
			}
			if err != nil {
				t.Fatal(err)
			}
			if session.Model().ID != target.ID {
				t.Fatal("model switch was not applied")
			}
			if settings.GetDefaultModel() != "original-model" || settings.GetDefaultProvider() != "original-provider" {
				t.Fatalf("model switch rewrote global defaults: %s/%s", settings.GetDefaultProvider(), settings.GetDefaultModel())
			}
		})
	}
}
