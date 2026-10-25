package ikml

import "math"

// Shape is a simplified geometry for drawing a preview: "point", "line"
// or "polygon", with [lon, lat] pairs. Polygons list their rings.
type Shape struct {
	Kind  string         `json:"k"`
	Rings [][][2]float64 `json:"c"`
	Name  string         `json:"n,omitempty"`
}

// Preview returns the document's geometries thinned to about maxPoints
// positions in total, for drawing a quick map. Positions outside the
// valid range are left out.
func Preview(d *Doc, maxPoints int) []Shape {
	total := 0
	for _, pm := range d.Placemarks() {
		if pm.Geometry != nil {
			for _, l := range pm.Geometry.coordLists() {
				total += len(l)
			}
		}
	}
	step := 1
	if maxPoints > 0 && total > maxPoints {
		step = int(math.Ceil(float64(total) / float64(maxPoints)))
	}
	thin := func(cs []Coord, keepEnds bool) [][2]float64 {
		var out [][2]float64
		for i, c := range cs {
			if i%step != 0 && !(keepEnds && i == len(cs)-1) {
				continue
			}
			if math.Abs(c.Lon) > 180 || math.Abs(c.Lat) > 90 || math.IsNaN(c.Lon) || math.IsNaN(c.Lat) {
				continue
			}
			out = append(out, [2]float64{c.Lon, c.Lat})
		}
		return out
	}
	out := []Shape{}
	for _, pm := range d.Placemarks() {
		if pm.Geometry == nil {
			continue
		}
		for _, l := range pm.Geometry.leaves() {
			switch l.Type {
			case "Point":
				if p := thin(l.Coords, false); len(p) > 0 {
					out = append(out, Shape{Kind: "point", Rings: [][][2]float64{p[:1]}, Name: pm.Name})
				}
			case "LineString", "LinearRing", "Track":
				if p := thin(l.Coords, true); len(p) > 0 {
					out = append(out, Shape{Kind: "line", Rings: [][][2]float64{p}, Name: pm.Name})
				}
			case "Polygon":
				s := Shape{Kind: "polygon", Name: pm.Name}
				for _, r := range l.Rings {
					if p := thin(r, true); len(p) > 0 {
						s.Rings = append(s.Rings, p)
					}
				}
				if len(s.Rings) > 0 {
					out = append(out, s)
				}
			}
		}
	}
	return out
}
