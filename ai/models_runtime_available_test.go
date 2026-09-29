package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
)

func TestModelsGetAvailableRejectsBeforeOtherChecksSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		models := CreateModels()
		started, release, failed := make(chan struct{}), make(chan struct{}), make(chan struct{})
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "slow", auth: &ProviderAuth{APIKey: &APIKeyAuth{Name: "Slow", Check: func(context.Context, APIKeyAuthInput) (*AuthCheck, error) {
			close(started)
			<-release
			return &AuthCheck{Type: CredentialAPIKey}, nil
		}}}}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "fast", auth: &ProviderAuth{APIKey: &APIKeyAuth{Name: "Fast", Check: func(context.Context, APIKeyAuthInput) (*AuthCheck, error) {
			<-started
			close(failed)
			return nil, errors.New("fast failure")
		}}}}))
		done := make(chan error, 1)
		go func() { _, err := models.GetAvailable(t.Context()); done <- err }()
		<-failed
		synctest.Wait()
		completed := false
		select {
		case err := <-done:
			completed = true
			if err == nil || !strings.Contains(err.Error(), "fast failure") {
				t.Errorf("error=%v", err)
			}
		default:
			t.Error("getAvailable waited for unrelated checks after a rejection")
		}
		close(release)
		if !completed {
			<-done
		}
		models.operations.Wait()
	})
}
