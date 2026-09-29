package ai

import (
	"errors"
	"testing"
	"testing/synctest"
)

func TestImagesModelsRefreshAllJoinsAndRetainsFailedCatalog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		models := CreateImagesModels()
		entered := make(chan string, 2)
		first, second := make(chan struct{}), make(chan struct{})
		firstReleased, secondReleased := false, false
		defer func() {
			if !firstReleased {
				close(first)
			}
			if !secondReleased {
				close(second)
			}
		}()
		for _, id := range []string{"first", "second"} {
			models.SetProvider(CreateImagesProvider(CreateImagesProviderOptions{ID: id, Models: []ImagesModel{imageRuntimeTestModel(id, "old")}, Auth: imageRuntimeTestProvider(id, "", nil, nil).Auth, RefreshModels: func() ([]ImagesModel, error) {
				entered <- id
				if id == "first" {
					<-first
					return []ImagesModel{imageRuntimeTestModel(id, "new")}, nil
				}
				<-second
				return nil, errors.New("unavailable")
			}}))
		}
		done := make(chan error, 1)
		go func() { done <- models.Refresh() }()
		<-entered
		<-entered
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("returned before providers settled: %v", err)
		default:
		}
		firstReleased = true
		close(first)
		synctest.Wait()
		if models.GetModel("first", "new") == nil {
			t.Fatal("successful provider did not publish")
		}
		select {
		case err := <-done:
			t.Fatalf("returned while failed provider was pending: %v", err)
		default:
		}
		secondReleased = true
		close(second)
		if err := <-done; err != nil {
			t.Fatalf("best-effort refresh rejected: %v", err)
		}
		if models.GetModel("second", "old") == nil {
			t.Fatal("failure discarded the last-known catalog")
		}
	})
}
