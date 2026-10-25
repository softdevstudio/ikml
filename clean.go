package ikml

import (
	"bytes"
	"fmt"
	"strings"
)

// CleanOptions selects optional fixes. The zero value applies only the
// safe fixes.
type CleanOptions struct {
	// FixSwapped swaps longitude and latitude where swapped-latlon was
	// reported. It is opt-in because a wrong guess moves data a long way.
	FixSwapped bool
	// DropUnusedAssets removes KMZ files that nothing refers to.
	DropUnusedAssets bool
}

// Change is one fix that Clean made.
type Change struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Location string `json:"location,omitempty"`
	Line     int    `json:"line,omitempty"`
	Count    int    `json:"count"`
}

// Clean applies safe fixes and returns a new document plus the list of
// changes. The input is not modified. The returned document is the
// re-parsed cleaned output, so Inspect on it tells the truth about what
// will be written.
//
// Safe fixes: close unclosed rings, remove consecutive duplicate points,
// rewrite "lon, lat" spacing, remove styleUrl references to styles that
// do not exist, move old or missing KML namespaces to 2.2, and drop a
// WGS 84 crs member from GeoJSON. Empty placemarks are kept. With
// FixSwapped, swapped coordinates are corrected too.
func Clean(d *Doc, opt CleanOptions) (*Doc, []Change, error) {
	if d.fatal {
		return nil, nil, ErrUnreadable
	}
	c := d.clone()
	cl := &cleaner{d: c, opt: opt}
	swapped := map[*Placemark]bool{}
	if opt.FixSwapped {
		for _, pm := range c.Placemarks() {
			if pm.Geometry != nil && isSwapped(pm.Geometry) && !hasBadTuples(pm.Geometry) {
				swapped[pm] = true
			}
		}
	}
	for _, pm := range c.Placemarks() {
		if pm.Geometry != nil {
			cl.geometry(pm, pm.Geometry, swapped[pm])
		}
	}
	if c.Format == FormatGeoJSON {
		if c.HasCRS && isWGS84(c.CRSName) {
			c.dropCRS = true
			cl.add("geojson-crs", Loc{}, 1, "Removed the \"crs\" member. The coordinates were already WGS 84, which is what RFC 7946 assumes.")
		}
		rewound := 0
		for _, pm := range c.Placemarks() {
			if pm.Geometry == nil {
				continue
			}
			for _, l := range pm.Geometry.leaves() {
				if l.Type != "Polygon" {
					continue
				}
				for i, r := range l.Rings {
					if len(r) >= 4 && wrongWinding(r, i == 0 && !l.NoOuter) {
						rewound++
					}
				}
			}
		}
		if rewound > 0 {
			cl.add("winding", Loc{}, rewound, fmt.Sprintf("Rewound %d ring(s) to the RFC 7946 order (outer rings counterclockwise, holes clockwise).", rewound))
		}
		var buf bytes.Buffer
		if err := WriteGeoJSON(c, &buf); err != nil {
			return nil, nil, err
		}
		out, err := Open(d.Filename, buf.Bytes())
		if err != nil {
			return nil, nil, err
		}
		return out, cl.changes, nil
	}

	// KML and KMZ: edit the original bytes.
	for _, pm := range c.Placemarks() {
		if pm.StyleURL == "" {
			continue
		}
		if id, local := localStyleID(pm.StyleURL); local && c.Styles[id] == nil && c.StyleMaps[id] == nil {
			c.edits = append(c.edits, edit{pm.styleRange[0], pm.styleRange[1], ""})
			cl.add("style-missing", Loc{Path: pm.Path, Line: pm.styleLine}, 1, fmt.Sprintf("Removed the reference to the missing style %s.", pm.StyleURL))
		}
	}
	for _, pr := range c.pairs {
		if id, local := localStyleID(pr.url); local && c.Styles[id] == nil && c.StyleMaps[id] == nil {
			c.edits = append(c.edits, edit{pr.rng[0], pr.rng[1], ""})
			cl.add("style-missing", pr.loc, 1, fmt.Sprintf("Removed the style map's reference to the missing style %s.", pr.url))
		}
	}
	if tag := c.raw[c.rootTag[0]:c.rootTag[1]]; bytes.HasPrefix(tag, []byte("<kml")) {
		s := string(tag)
		switch c.Namespace {
		case nsKML20, nsKML21:
			s = strings.ReplaceAll(s, c.Namespace, NSKML22)
			c.edits = append(c.edits, edit{c.rootTag[0], c.rootTag[1], s})
			cl.add("old-namespace", Loc{Line: 1}, 1, fmt.Sprintf("Changed the namespace from %s to %s.", c.Namespace, NSKML22))
		case "":
			if len(s) > 4 && (s[4] == ' ' || s[4] == '>' || s[4] == '\t' || s[4] == '\n' || s[4] == '\r') && !strings.Contains(s, "xmlns=") {
				s = `<kml xmlns="` + NSKML22 + `"` + s[4:]
				c.edits = append(c.edits, edit{c.rootTag[0], c.rootTag[1], s})
				cl.add("no-namespace", Loc{Line: 1}, 1, "Added the KML 2.2 namespace to the <kml> root.")
			}
		}
	}
	if opt.DropUnusedAssets && c.Format == FormatKMZ {
		var keep []Asset
		for _, a := range c.Assets {
			if a.Used {
				keep = append(keep, a)
				continue
			}
			cl.add("unused-asset", Loc{Path: a.Path}, 1, fmt.Sprintf("Removed %s (%s), which nothing referred to.", a.Path, humanBytes(a.Size)))
		}
		c.Assets = keep
	}
	var buf bytes.Buffer
	var err error
	if c.Format == FormatKMZ {
		err = WriteKMZ(c, &buf, nil)
	} else {
		err = WriteKML(c, &buf)
	}
	if err != nil {
		return nil, nil, err
	}
	out, err := Open(d.Filename, buf.Bytes())
	if err != nil {
		return nil, nil, err
	}
	return out, cl.changes, nil
}

type cleaner struct {
	d       *Doc
	opt     CleanOptions
	changes []Change
	idx     map[string]int
}

func (cl *cleaner) add(code string, loc Loc, n int, msg string) {
	if cl.idx == nil {
		cl.idx = map[string]int{}
	}
	key := code + "|" + loc.String() + "|" + msg
	if i, ok := cl.idx[key]; ok {
		cl.changes[i].Count += n
		return
	}
	cl.idx[key] = len(cl.changes)
	cl.changes = append(cl.changes, Change{Code: code, Message: msg, Location: loc.Path, Line: loc.Line, Count: n})
}

func dedup(cs []Coord) []Coord {
	if len(cs) < 2 {
		return cs
	}
	out := []Coord{cs[0]}
	for _, c := range cs[1:] {
		if c != out[len(out)-1] {
			out = append(out, c)
		}
	}
	return out
}

// fixLine returns the cleaned line and how many duplicates were removed.
func fixLine(cs []Coord) ([]Coord, int) {
	d := dedup(cs)
	if len(d) == len(cs) || len(d) < 2 {
		return cs, 0
	}
	return d, len(cs) - len(d)
}

// fixRing returns the cleaned ring, duplicates removed, and whether the
// ring was closed.
func fixRing(r []Coord) ([]Coord, int, bool) {
	cand := dedup(r)
	closed := false
	if len(cand) >= 3 && !ringClosed(cand) {
		cand = append(cand, cand[0])
		closed = true
	}
	if len(cand) >= 4 {
		removed := len(r) - len(cand)
		if closed {
			removed++
		}
		return cand, removed, closed
	}
	// Removing duplicates would leave too few points: only close it.
	if len(r) >= 3 && !ringClosed(r) {
		return append(append([]Coord(nil), r...), r[0]), 0, true
	}
	return r, 0, false
}

func swapAll(cs []Coord) []Coord {
	out := make([]Coord, len(cs))
	for i, c := range cs {
		out[i] = c
		out[i].Lon, out[i].Lat = c.Lat, c.Lon
	}
	return out
}

func sameCoords(a, b []Coord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (cl *cleaner) geometry(pm *Placemark, g *Geometry, swap bool) {
	if swap {
		cl.add("swapped-latlon", Loc{Path: pm.Path, Line: pm.Line}, 1, "Swapped longitude and latitude back into the right order.")
	}
	for _, l := range g.leaves() {
		loc := Loc{Path: pm.Path, Line: l.Line}
		if loc.Line == 0 {
			loc.Line = pm.Line
		}
		switch l.Type {
		case "Point", "LineString", "LinearRing":
			orig := l.Coords
			cs := orig
			if swap {
				cs = swapAll(cs)
			}
			type note struct {
				code string
				n    int
				msg  string
			}
			var notes []note
			if l.Type == "LineString" {
				var n int
				cs, n = fixLine(cs)
				if n > 0 {
					notes = append(notes, note{"duplicate-points", n, "Removed repeated points from a line."})
				}
			}
			if l.Type == "LinearRing" {
				var n int
				var closed bool
				cs, n, closed = fixRing(cs)
				if n > 0 {
					notes = append(notes, note{"duplicate-points", n, "Removed repeated points from a ring."})
				}
				if closed {
					notes = append(notes, note{"ring-unclosed", 1, "Closed a ring by repeating its first point at the end."})
				}
			}
			if l.spacing {
				notes = append(notes, note{"coord-spacing", 1, "Rewrote coordinates that had spaces next to the commas."})
			}
			if (!sameCoords(cs, orig) || l.spacing) && cl.setSlot(l.slot, cs, l.badTuples) {
				l.Coords = cs
				for _, n := range notes {
					cl.add(n.code, loc, n.n, n.msg)
				}
			}
		case "Polygon":
			for i, r := range l.Rings {
				name := "the outer ring"
				if i > 0 || l.NoOuter {
					name = "an inner ring"
				}
				cs := r
				if swap {
					cs = swapAll(cs)
				}
				var n int
				var closed bool
				cs, n, closed = fixRing(cs)
				bad := 0
				if i < len(l.ringBad) {
					bad = l.ringBad[i]
				}
				slotID := -1
				if i < len(l.ringSlots) {
					slotID = l.ringSlots[i]
				}
				if cl.d.Format == FormatGeoJSON {
					bad, slotID = 0, -2
				}
				if !sameCoords(cs, r) || l.spacing {
					if !cl.setSlot(slotID, cs, bad) {
						continue
					}
					l.Rings[i] = cs
					if n > 0 {
						cl.add("duplicate-points", loc, n, "Removed repeated points from "+name+".")
					}
					if closed {
						cl.add("ring-unclosed", loc, 1, "Closed "+name+" by repeating its first point at the end.")
					}
					if l.spacing {
						cl.add("coord-spacing", loc, 1, "Rewrote coordinates that had spaces next to the commas.")
					}
				}
			}
		case "Track":
			if swap {
				l.Coords = swapAll(l.Coords)
				for i, s := range l.pointSlots {
					if i < len(l.Coords) && cl.d.raw != nil {
						c := l.Coords[i]
						t := formatFloat(c.Lon) + " " + formatFloat(c.Lat)
						if c.Has3 {
							t += " " + formatFloat(c.Alt)
						}
						sl := cl.d.slots[s]
						cl.d.edits = append(cl.d.edits, edit{sl.start, sl.end, t})
					}
				}
			}
		}
	}
}

// setSlot schedules a rewrite of one <coordinates> element. It returns
// false when the element cannot be rewritten safely (it holds tuples that
// could not be read, which a rewrite would silently drop).
func (cl *cleaner) setSlot(slotID int, cs []Coord, bad int) bool {
	if cl.d.Format == FormatGeoJSON {
		return true // the GeoJSON writer uses the model directly
	}
	if slotID < 0 || bad > 0 {
		return false
	}
	sl := cl.d.slots[slotID]
	cl.d.edits = append(cl.d.edits, edit{sl.start, sl.end, kmlCoords(cs)})
	return true
}

func hasBadTuples(g *Geometry) bool {
	for _, l := range g.leaves() {
		if l.badTuples > 0 {
			return true
		}
	}
	return false
}
