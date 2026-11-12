package uiview

import (
	"encoding/json"
	"fmt"
	"slices"
)

// pig additive (D614): drawing. A mount, an update or an alternative may carry one drawing beside its view: a scene of
// closed vector shapes or a pixel grid over an image asset, with regions that turn a gesture into a declared intent.
// The host validates and bounds a drawing; it never rasterizes one and never hit-tests a region. The client does both.

const (
	ProfileScene  = "scene"
	ProfilePixels = "pixels"
)

// pig additive (D614): the bounds of a drawing.
const (
	maxShapes      = 4096    // pig additive (D614): shapes of a drawing, groups and region shapes included
	maxShapeNest   = 8       // pig additive (D614): nesting of groups
	maxShapePoints = 1024    // pig additive (D614): points of a polyline, commands of a path
	maxRegions     = 64      // pig additive (D614): regions of a drawing
	maxPixels      = 1 << 20 // pig additive (D614): width times height of a pixel grid
	maxRegionName  = 256     // pig additive (D614): bytes of a region's accessible name
)

// Viewport is the rectangle of drawing coordinates the client fits into the space it gives the drawing.
type Viewport struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Fit    string  `json:"fit"`
}

// Stroke outlines a shape.
type Stroke struct {
	Color string  `json:"color"`
	Width float64 `json:"width"`
}

// Point is one vertex of a polyline.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PathCommand is one step of a path: move and line end at (X, Y); quad bends through (X1, Y1); cubic through (X1, Y1)
// and (X2, Y2); close returns to the start of the subpath.
type PathCommand struct {
	Op string  `json:"op"`
	X  float64 `json:"x,omitempty"`
	Y  float64 `json:"y,omitempty"`
	X1 float64 `json:"x1,omitempty"`
	Y1 float64 `json:"y1,omitempty"`
	X2 float64 `json:"x2,omitempty"`
	Y2 float64 `json:"y2,omitempty"`
}

// DrawShape is one shape. Kind is group (Children), rect (X, Y, Width, Height, Radius), ellipse (CX, CY, RX, RY), line
// (X1, Y1, X2, Y2), polyline (Points, Closed), path (Commands), text (X, Y, Text, Size, Align) or image (X, Y, Width,
// Height, Asset). A member that does not belong to the kind rejects the drawing.
type DrawShape struct {
	Kind      string        `json:"kind"`
	Key       string        `json:"key,omitempty"`
	Fill      string        `json:"fill,omitempty"`
	Stroke    *Stroke       `json:"stroke,omitempty"`
	Opacity   *float64      `json:"opacity,omitempty"`
	Transform []float64     `json:"transform,omitempty"`
	Children  []DrawShape   `json:"children,omitempty"`
	X         float64       `json:"x,omitempty"`
	Y         float64       `json:"y,omitempty"`
	Width     float64       `json:"width,omitempty"`
	Height    float64       `json:"height,omitempty"`
	Radius    float64       `json:"radius,omitempty"`
	CX        float64       `json:"cx,omitempty"`
	CY        float64       `json:"cy,omitempty"`
	RX        float64       `json:"rx,omitempty"`
	RY        float64       `json:"ry,omitempty"`
	X1        float64       `json:"x1,omitempty"`
	Y1        float64       `json:"y1,omitempty"`
	X2        float64       `json:"x2,omitempty"`
	Y2        float64       `json:"y2,omitempty"`
	Points    []Point       `json:"points,omitempty"`
	Closed    bool          `json:"closed,omitempty"`
	Commands  []PathCommand `json:"commands,omitempty"`
	Text      string        `json:"text,omitempty"`
	Size      float64       `json:"size,omitempty"`
	Align     string        `json:"align,omitempty"`
	Asset     string        `json:"asset,omitempty"`
}

// Pixels is a pixel grid: an image asset shown Width by Height cells, each cell one pixel of the asset.
type Pixels struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Asset   string `json:"asset"`
	Scaling string `json:"scaling"`
}

// Region turns a gesture inside Shape (a rect, an ellipse or a closed polyline, in drawing coordinates) into Intent.
// The client hit-tests; it raises the intent with the argument its declared shape asks for.
type Region struct {
	Key      string    `json:"key"`
	Shape    DrawShape `json:"shape"`
	Gestures []string  `json:"gestures"`
	Intent   string    `json:"intent"`
	Name     string    `json:"name"`
	Role     string    `json:"role,omitempty"`
}

// Drawing is the drawing of a view. Profile "scene" carries Shapes; "pixels" carries Pixels. Node names the view node
// the drawing fills; without it the drawing follows the view.
type Drawing struct {
	Profile  string      `json:"profile"`
	Node     string      `json:"node,omitempty"`
	Viewport *Viewport   `json:"viewport,omitempty"`
	Shapes   []DrawShape `json:"shapes,omitempty"`
	Pixels   *Pixels     `json:"pixels,omitempty"`
	Regions  []Region    `json:"regions,omitempty"`

	// shapes, text and bytes are measured once by check: the shape count, the longest text and the encoded size.
	shapes, text, bytes int
}

// Shape member groups; a kind takes exactly the groups listed for it.
const (
	hasXY = 1 << iota
	hasSize
	hasRadius
	hasEllipse
	hasLine
	hasPoints
	hasCommands
	hasText
	hasAsset
	hasChildren
)

var shapeMembers = map[string]int{
	"group": hasChildren, "rect": hasXY | hasSize | hasRadius, "ellipse": hasEllipse, "line": hasLine, "polyline": hasPoints,
	"path": hasCommands, "text": hasXY | hasText, "image": hasXY | hasSize | hasAsset,
}

func (s *DrawShape) members() int {
	m := 0
	set := func(bit int, on bool) {
		if on {
			m |= bit
		}
	}
	set(hasXY, s.X != 0 || s.Y != 0)
	set(hasSize, s.Width != 0 || s.Height != 0)
	set(hasRadius, s.Radius != 0)
	set(hasEllipse, s.CX != 0 || s.CY != 0 || s.RX != 0 || s.RY != 0)
	set(hasLine, s.X1 != 0 || s.Y1 != 0 || s.X2 != 0 || s.Y2 != 0)
	set(hasPoints, s.Points != nil || s.Closed)
	set(hasCommands, s.Commands != nil)
	set(hasText, s.Text != "" || s.Size != 0 || s.Align != "")
	set(hasAsset, s.Asset != "")
	set(hasChildren, s.Children != nil)
	return m
}

// validColor accepts "#rrggbb", "#rrggbbaa" and a theme color token.
func validColor(c string) bool {
	if len(c) == 0 || len(c) > 32 {
		return false
	}
	if c[0] == '#' {
		if len(c) != 7 && len(c) != 9 {
			return false
		}
		for i := 1; i < len(c); i++ {
			if !(c[i] >= '0' && c[i] <= '9' || c[i] >= 'a' && c[i] <= 'f') {
				return false
			}
		}
		return true
	}
	for i := range len(c) {
		ch := c[i]
		if !(ch >= 'a' && ch <= 'z' || i > 0 && (ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9')) {
			return false
		}
	}
	return true
}

// check validates one shape and what it holds, counting every shape into d.shapes. It stops at the first shape past
// the bound, so its work is bounded by maxShapes however large the record is.
func (d *Drawing) checkShape(s *DrawShape, depth int, assets map[string]struct{}) error {
	d.shapes++
	if d.shapes > maxShapes {
		return fmt.Errorf("more than %d shapes", maxShapes)
	}
	if depth > maxShapeNest {
		return fmt.Errorf("shapes nest deeper than %d", maxShapeNest)
	}
	allowed, ok := shapeMembers[s.Kind]
	if !ok {
		return fmt.Errorf("unknown shape kind %q", s.Kind)
	}
	if s.members()&^allowed != 0 {
		return fmt.Errorf("shape %q carries a member of another kind", s.Kind)
	}
	if len(s.Key) > 128 || (s.Fill != "" && !validColor(s.Fill)) || (s.Stroke != nil && (!validColor(s.Stroke.Color) || s.Stroke.Width < 0)) {
		return fmt.Errorf("shape %q has a malformed key, fill or stroke", s.Kind)
	}
	if (s.Opacity != nil && (*s.Opacity < 0 || *s.Opacity > 1)) || (s.Transform != nil && len(s.Transform) != 6) {
		return fmt.Errorf("shape %q: opacity is 0..1 and transform six numbers", s.Kind)
	}
	if s.Width < 0 || s.Height < 0 || s.Radius < 0 || s.RX < 0 || s.RY < 0 || s.Size < 0 {
		return fmt.Errorf("shape %q has a negative size", s.Kind)
	}
	switch s.Kind {
	case "group":
		for i := range s.Children {
			if err := d.checkShape(&s.Children[i], depth+1, assets); err != nil {
				return err
			}
		}
	case "polyline":
		if len(s.Points) < 2 || len(s.Points) > maxShapePoints {
			return fmt.Errorf("a polyline has 2..%d points", maxShapePoints)
		}
	case "path":
		if len(s.Commands) == 0 || len(s.Commands) > maxShapePoints {
			return fmt.Errorf("a path has 1..%d commands", maxShapePoints)
		}
		for i, c := range s.Commands {
			if !slices.Contains([]string{"move", "line", "quad", "cubic", "close"}, c.Op) || (i == 0 && c.Op != "move") {
				return fmt.Errorf("path command %d is %q; a path starts with move and uses move, line, quad, cubic and close", i, c.Op)
			}
		}
	case "text":
		if s.Text == "" || (s.Align != "" && s.Align != "start" && s.Align != "center" && s.Align != "end") {
			return fmt.Errorf("a text shape needs text, and align is start, center or end")
		}
		if len(s.Text) > d.text {
			d.text = len(s.Text)
		}
	case "image":
		if _, ok := assets[s.Asset]; !ok {
			return fmt.Errorf("image shape names asset %q, which is not declared", s.Asset)
		}
	}
	return nil
}

// check validates the drawing against the content it belongs to: its assets and its declared intents.
func (d *Drawing) check(intents, assets map[string]struct{}) error {
	d.shapes, d.text = 0, 0
	if d.Viewport != nil {
		v := d.Viewport
		if v.Width <= 0 || v.Height <= 0 || (v.Fit != "contain" && v.Fit != "cover" && v.Fit != "stretch") {
			return fmt.Errorf("viewport needs a positive size and fit contain, cover or stretch")
		}
	}
	switch d.Profile {
	case ProfileScene:
		if d.Pixels != nil || len(d.Shapes) == 0 {
			return fmt.Errorf("profile scene carries shapes and no pixels")
		}
		for i := range d.Shapes {
			if err := d.checkShape(&d.Shapes[i], 1, assets); err != nil {
				return err
			}
		}
	case ProfilePixels:
		p := d.Pixels
		if p == nil || d.Shapes != nil {
			return fmt.Errorf("profile pixels carries pixels and no shapes")
		}
		if p.Width < 1 || p.Height < 1 || p.Width > maxPixels || p.Height > maxPixels || p.Width*p.Height > maxPixels {
			return fmt.Errorf("a pixel grid is at least 1 by 1 and at most %d pixels", maxPixels)
		}
		if _, ok := assets[p.Asset]; !ok {
			return fmt.Errorf("pixels names asset %q, which is not declared", p.Asset)
		}
		if p.Scaling != "nearest" && p.Scaling != "smooth" {
			return fmt.Errorf("pixels scaling is nearest or smooth")
		}
	default:
		return fmt.Errorf("unknown drawing profile %q", d.Profile)
	}
	if len(d.Regions) > maxRegions {
		return fmt.Errorf("%d regions, at most %d", len(d.Regions), maxRegions)
	}
	keys := make(map[string]struct{}, len(d.Regions))
	for i := range d.Regions {
		g := &d.Regions[i]
		if _, dup := keys[g.Key]; dup || g.Key == "" || len(g.Key) > 128 {
			return fmt.Errorf("region key %q is empty, too long or repeated", g.Key)
		}
		keys[g.Key] = struct{}{}
		if g.Name == "" || len(g.Name) > maxRegionName || (g.Role != "" && !slices.Contains(roles, g.Role)) {
			return fmt.Errorf("region %q needs a name of at most %d bytes and a known role", g.Key, maxRegionName)
		}
		if _, ok := intents[g.Intent]; !ok {
			return fmt.Errorf("region %q raises intent %q, which is not declared", g.Key, g.Intent)
		}
		if len(g.Gestures) == 0 {
			return fmt.Errorf("region %q has no gesture", g.Key)
		}
		for j, gesture := range g.Gestures {
			if !slices.Contains([]string{"activate", "press", "release", "drag"}, gesture) || slices.Contains(g.Gestures[:j], gesture) {
				return fmt.Errorf("region %q gesture %q is unknown or repeated", g.Key, gesture)
			}
		}
		k := g.Shape.Kind
		if k != "rect" && k != "ellipse" && !(k == "polyline" && g.Shape.Closed) {
			return fmt.Errorf("region %q is a rect, an ellipse or a closed polyline", g.Key)
		}
		if err := d.checkShape(&g.Shape, 1, assets); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	d.bytes = len(raw)
	return nil
}
