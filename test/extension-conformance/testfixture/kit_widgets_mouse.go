package testfixture

import "github.com/MichaelKinsy/PiG/extensions/sdk/kit"

func init() {
	text := func(t string) *kit.Text { return kit.NewText(t, 1, 0) }
	child := kitScrollChild
	const m = "pi-tui/MouseRegion/"
	registerKitWidgetCase(m+"renders its child", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(m+"every type by default", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(m+"types filter", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		node.Types = []string{"click"}
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(m+"not handled", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		node.Handled = new(false)
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(m+"capture", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		node.Capture = true
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(m+"focus and render", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		node.Focus = true
		node.Render = new(false)
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(m+"local coordinates", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		return kit.View{Root: kit.NewContainer(text("Above the region"), node, text("Below"))}
	})
	registerKitWidgetCase(m+"the child takes the event first", func() kit.View {
		node := kit.NewMouseRegion("m", child("branch"))
		return kit.View{Root: kit.NewContainer(node)}
	})
	registerKitWidgetCase(m+"right button", func() kit.View {
		node := kit.NewMouseRegion("m", child("text"))
		return kit.View{Root: kit.NewContainer(node)}
	})
}
