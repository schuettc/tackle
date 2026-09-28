package serve

// Tests for the casebook mark: a static favicon and an animated mark.svg.
//
// Three invariants:
//  1. favicon.svg is a static 64×64 ink tile with the signal-coloured glyph.
//  2. mark.svg stripped of its SMIL <animate> elements has the same shape
//     elements (d, stroke, fill and related geometry attrs) as favicon.svg —
//     the resting frame equals the static drawing (PALETTE.md).
//  3. mark.svg uses SMIL only: it has <animate> and neither <script> nor
//     CSS @keyframes.

import (
	"encoding/xml"
	"io"
	"sort"
	"strings"
	"testing"
)

// shapeGeomAttrs are the attributes that define a shape's geometry and colour.
// Animation-support attrs (stroke-dasharray, stroke-dashoffset) are
// deliberately excluded so the resting-frame comparison holds.
var shapeGeomAttrs = map[string]bool{
	"d":               true,
	"fill":            true,
	"stroke":          true,
	"stroke-width":    true,
	"stroke-linejoin": true,
	"stroke-linecap":  true,
	"rx":              true,
	"ry":              true,
	"width":           true,
	"height":          true,
}

// shapeElem captures a shape element tag and its geometry/colour attributes.
type shapeElem struct {
	tag   string
	attrs map[string]string
}

// key returns a deterministic string representation for comparison.
func (s shapeElem) key() string {
	var parts []string
	for k, v := range s.attrs {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return s.tag + "{" + strings.Join(parts, ";") + "}"
}

// extractShapeElems parses SVG data and returns the ordered list of shape
// elements (rect, path, circle, etc.) with their geometry/colour attrs.
// <animate> children are ignored; svg/g/defs containers are transparent.
func extractShapeElems(data []byte) ([]shapeElem, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var elems []shapeElem
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		tag := start.Name.Local
		switch tag {
		case "animate", "animateTransform", "animateMotion",
			"svg", "defs", "g", "title", "desc", "metadata":
			// transparent containers or animation elements — don't collect
			continue
		case "rect", "path", "circle", "ellipse", "line",
			"polyline", "polygon", "use":
			el := shapeElem{tag: tag, attrs: make(map[string]string)}
			for _, a := range start.Attr {
				if shapeGeomAttrs[a.Name.Local] {
					el.attrs[a.Name.Local] = a.Value
				}
			}
			elems = append(elems, el)
		}
	}
	return elems, nil
}

// TestFaviconIsAStaticTileAndGlyph checks that favicon.svg is:
//   - 64×64 viewBox
//   - has an ink tile rect rx=14 fill=#14161d
//   - contains no <animate> (must be static)
//   - has at least one path stroked in the signal colour #d98f66
func TestFaviconIsAStaticTileAndGlyph(t *testing.T) {
	data, err := assets.ReadFile("assets/favicon.svg")
	if err != nil {
		t.Fatalf("favicon.svg not found: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, `viewBox="0 0 64 64"`) {
		t.Error(`favicon.svg: missing viewBox="0 0 64 64"`)
	}
	if !strings.Contains(s, `rx="14"`) {
		t.Error(`favicon.svg: ink tile must have rx="14"`)
	}
	if !strings.Contains(s, `fill="#14161d"`) {
		t.Error(`favicon.svg: ink tile must have fill="#14161d"`)
	}
	if strings.Contains(s, "<animate") {
		t.Error("favicon.svg must be static (no <animate> elements)")
	}
	if !strings.Contains(s, `stroke="#d98f66"`) {
		t.Error("favicon.svg: no signal-coloured path found (stroke=\"#d98f66\")")
	}
}

// TestAnimatedMarkRestingFrameEqualsFavicon strips every <animate> from
// mark.svg, then compares its shape elements (geometry and colour attrs) to
// favicon.svg's. The two files must show the same glyph; mark.svg just adds
// motion on top.
func TestAnimatedMarkRestingFrameEqualsFavicon(t *testing.T) {
	favData, err := assets.ReadFile("assets/favicon.svg")
	if err != nil {
		t.Fatalf("favicon.svg not found: %v", err)
	}
	markData, err := assets.ReadFile("assets/mark.svg")
	if err != nil {
		t.Fatalf("mark.svg not found: %v", err)
	}

	favShapes, err := extractShapeElems(favData)
	if err != nil {
		t.Fatalf("parsing favicon.svg: %v", err)
	}
	markShapes, err := extractShapeElems(markData)
	if err != nil {
		t.Fatalf("parsing mark.svg: %v", err)
	}

	if len(favShapes) != len(markShapes) {
		t.Errorf("shape element count: favicon.svg=%d mark.svg=%d", len(favShapes), len(markShapes))
		for i, s := range favShapes {
			t.Logf("  favicon[%d] %s", i, s.key())
		}
		for i, s := range markShapes {
			t.Logf("  mark[%d]    %s", i, s.key())
		}
		return
	}

	for i := range favShapes {
		fk := favShapes[i].key()
		mk := markShapes[i].key()
		if fk != mk {
			t.Errorf("shape[%d] mismatch:\n  favicon: %s\n  mark:    %s", i, fk, mk)
		}
	}
}

// TestAnimatedMarkIsSMILOnly checks that mark.svg:
//   - has at least one <animate> element (SMIL animation present)
//   - has no <script> (no JS)
//   - has no @keyframes (no CSS animation)
func TestAnimatedMarkIsSMILOnly(t *testing.T) {
	data, err := assets.ReadFile("assets/mark.svg")
	if err != nil {
		t.Fatalf("mark.svg not found: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, "<animate") {
		t.Error("mark.svg: no SMIL <animate> element found")
	}
	if strings.Contains(s, "<script") {
		t.Error("mark.svg must not contain <script> (SMIL only)")
	}
	if strings.Contains(s, "@keyframes") {
		t.Error("mark.svg must not contain CSS @keyframes (SMIL only)")
	}
}
