package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// assetPattern finds the `src` and `href` of a page's tags.
var assetPattern = regexp.MustCompile(`(?:src|href)="([^"]+)"`)

// hashless is an asset's URL with its content hashes masked, so the output is the same at every run.
var hashless = regexp.MustCompile(`\.[0-9a-f]{8,}(\.|$)`)

// doneCount is how many of items are done.
func doneCount(items []todo) int {
	count := 0
	for _, it := range items {
		if it.Done {
			count++
		}
	}
	return count
}

// pageStep is chapter IV: the page, the app's template with the base bundle, the plugins' combined script and their stylesheets. A watch
// starts at the current value; its listener gets only what changes after.
func (s *session) pageStep() error {
	status, page, err := s.request(http.MethodGet, "/", nil, s.cli())
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("the page answered %d", status)
	}
	fmt.Fprintln(s.out, "4. the page loads:")
	for _, asset := range assetPattern.FindAllStringSubmatch(string(page), -1) {
		masked := asset[1]
		// Twice: neighbouring hashes share their dot.
		for range 2 {
			masked = hashless.ReplaceAllString(masked, ".<hash>$1")
		}
		fmt.Fprintln(s.out, "  ", masked)
	}
	groceries, err := s.app.Host.Get(s.ctx, todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
	if err != nil {
		return err
	}
	defer groceries.Release()
	if _, err := itemsUntil(groceries, func([]todo) bool { return true }); err != nil {
		return err
	}
	watch, err := groceries.Member("items").Watch(s.ctx)
	if err != nil {
		return err
	}
	defer func() { _, _ = watch.Stop() }()
	current, err := itemsOf(watch.Value())
	if err != nil {
		return err
	}
	fmt.Fprintln(s.out, "   watch.value:", doneCount(current), "done")
	changed := make(chan []todo, 1)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		items, err := itemsOf(call.Value)
		if err != nil {
			return err
		}
		select {
		case changed <- items:
		default:
		}
		return nil
	})
	err = s.asRoot(serverPrincipal, func(ctx host.Ctx) error {
		_, err := groceries.Call("tick", delta.JsonObjectOf("id", 2, "done", true), ctx)
		return err
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(s.out, "   the listener's first call:", doneCount(<-changed), "done")
	return nil
}
