package ctxbackground

import (
	"context"
	"net/http"
)

func work(context.Context) {}

func bad() {
	work(context.Background()) // want `context.Background\(\) in library code: the caller cannot cancel the work it starts`
	work(context.TODO())       // want `context.TODO\(\) in library code`
}

// ctxmapping reports a root context inside a function that has one.
func hasContext(ctx context.Context) { work(context.Background()) }

func handler(w http.ResponseWriter, r *http.Request) { work(context.Background()) }

var root = context.Background()

func init() { work(context.Background()) }

func good(ctx context.Context) { work(ctx) }
