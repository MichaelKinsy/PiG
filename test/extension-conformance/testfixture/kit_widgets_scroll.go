package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// kitScrollChild is the child the scroll-view and mouse-region cases wrap: a text of wrapping rows, a markdown, an empty
// container, or a branch summary (which a click toggles).
func kitScrollChild(kind string) kit.Node {
	const lines = "Alpha line of the log\nBeta line of the log\nGamma line of the log that is long enough to wrap at the narrow widths\nDelta"
	const summary = "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`."
	const markdown = "# Title\n\n- one\n- two with a long line that wraps at narrow widths\n\n`code`"
	switch kind {
	case "region":
		return kit.NewText("Region content\nsecond row", 1, 0)
	case "md":
		return kit.NewMarkdown(markdown, 1, 0)
	case "empty":
		return kit.NewContainer()
	case "branch":
		node := kit.NewBranchSummary(summary)
		node.ID = "b"
		return node
	}
	return kit.NewText(lines, 1, 0)
}

func init() {
	text := func(t string) *kit.Text { return kit.NewText(t, 1, 0) }
	child := kitScrollChild
	const s = "pi-tui/ScrollView/"
	registerKitWidgetCase(s+"default", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"scrollbar always", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.Scrollbar = "always"
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"scrollbar auto", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.Scrollbar = "auto"
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"scrollbar colors", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.Scrollbar = "always"
		node.ScrollbarTrackColor = "dim"
		node.ScrollbarThumbColor = "accent"
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"follow end", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.Follow = "end"
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"primary and contain", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.Primary = true
		node.Overscroll = "contain"
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"hide delay", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.Scrollbar = "auto"
		node.ScrollbarHideDelayMs = new(50)
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"scroll top clamps", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.ScrollTop = new(3)
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"markdown child", func() kit.View {
		node := kit.NewScrollView("s", child("md"))
		node.Scrollbar = "always"
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"after other rows", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		node.Scrollbar = "always"
		return kit.View{Root: kit.NewContainer(text("Header"), node, text("Footer"))}
	})
	registerKitWidgetCase(s+"empty child", func() kit.View {
		node := kit.NewScrollView("s", child("empty"))
		node.Scrollbar = "always"
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(s+"wheel and click pass", func() kit.View {
		node := kit.NewScrollView("s", child("text"))
		return kit.View{Root: kit.NewContainer(node, text("after"))}
	})
}
