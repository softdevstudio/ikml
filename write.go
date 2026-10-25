package ikml

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrUnreadable is returned when writing a document that could not be
// read completely (see Doc.Readable).
var ErrUnreadable = errors.New("the file could not be read completely, so iKML will not write a converted copy; run 'ikml check' to see why")

// formatFloat writes the shortest decimal that parses back to exactly v.
func formatFloat(v float64) string {
	a := math.Abs(v)
	if a == 0 || (a >= 1e-6 && a < 1e21) {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func kmlTuple(c Coord) string {
	s := formatFloat(c.Lon) + "," + formatFloat(c.Lat)
	if c.Has3 {
		s += "," + formatFloat(c.Alt)
	}
	return s
}

func kmlCoords(cs []Coord) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = kmlTuple(c)
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------- GeoJSON

// WriteGeoJSON writes the document as an RFC 7946 FeatureCollection.
// Rings are closed and rewound (outer counterclockwise). For KML input,
// what can be kept is written as properties: name, description, folder,
// ExtendedData fields, timestamp/begin/end, coordTimes for tracks, and
// simplestyle-spec styling. LossReport lists what cannot be kept.
func WriteGeoJSON(d *Doc, w io.Writer) error {
	if d.fatal {
		return ErrUnreadable
	}
	bw := bufio.NewWriter(w)
	gw := &gjw{w: bw, d: d}
	gw.doc()
	if gw.err != nil {
		return gw.err
	}
	return bw.Flush()
}

type gjw struct {
	w   *bufio.Writer
	d   *Doc
	err error
}

func (g *gjw) s(s string) {
	if g.err == nil {
		_, g.err = g.w.WriteString(s)
	}
}

func jstr(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func (g *gjw) doc() {
	d := g.d
	g.s(`{"type":"FeatureCollection"`)
	if d.Format == FormatGeoJSON {
		for _, m := range d.TopForeign {
			if m.Key == "crs" && d.dropCRS {
				continue
			}
			g.s("," + jstr(m.Key) + ":" + compact(m.Value))
		}
	}
	g.s(`,"features":[`)
	for i, pm := range d.Placemarks() {
		if i > 0 {
			g.s(",")
		}
		g.s("\n")
		g.feature(pm)
	}
	g.s("\n]}\n")
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

func (g *gjw) feature(pm *Placemark) {
	g.s(`{"type":"Feature"`)
	if g.d.Format == FormatGeoJSON {
		if pm.RawID != nil {
			g.s(`,"id":` + compact(pm.RawID))
		}
		for _, m := range pm.Foreign {
			g.s("," + jstr(m.Key) + ":" + compact(m.Value))
		}
		if pm.Props != nil {
			g.s(`,"properties":{`)
			for i, m := range pm.Props {
				if i > 0 {
					g.s(",")
				}
				g.s(jstr(m.Key) + ":" + compact(m.Value))
			}
			g.s("}")
		} else {
			g.s(`,"properties":null`)
		}
	} else {
		if pm.ID != "" {
			g.s(`,"id":` + jstr(pm.ID))
		}
		g.s(`,"properties":{`)
		g.kmlProps(pm)
		g.s("}")
	}
	g.s(`,"geometry":`)
	if geo := toGeoJSONGeom(pm.Geometry); geo != nil {
		g.geom(geo)
	} else {
		g.s("null")
	}
	g.s("}")
}

type propw struct {
	g    *gjw
	n    int
	seen map[string]bool
}

func (p *propw) add(k, raw string) {
	key := k
	for i := 2; p.seen[key]; i++ {
		key = fmt.Sprintf("%s_%d", k, i)
	}
	p.seen[key] = true
	if p.n > 0 {
		p.g.s(",")
	}
	p.n++
	p.g.s(jstr(key) + ":" + raw)
}

func (g *gjw) kmlProps(pm *Placemark) {
	p := &propw{g: g, seen: map[string]bool{}}
	if pm.Name != "" {
		p.add("name", jstr(pm.Name))
	}
	if pm.Description != "" {
		p.add("description", jstr(pm.Description))
	}
	if len(pm.FolderPath) > 0 {
		p.add("folder", jstr(strings.Join(pm.FolderPath, " / ")))
	}
	if pm.TimeStamp != "" {
		p.add("timestamp", jstr(pm.TimeStamp))
	}
	if pm.Begin != "" {
		p.add("begin", jstr(pm.Begin))
	}
	if pm.End != "" {
		p.add("end", jstr(pm.End))
	}
	for _, f := range pm.Fields {
		p.add(f.Name, jstr(f.Value))
	}
	if pm.Geometry != nil {
		var times [][]string
		for _, l := range pm.Geometry.leaves() {
			if l.Type == "Track" {
				times = append(times, l.When)
			}
		}
		switch {
		case len(times) == 1:
			b, _ := json.Marshal(nonNil(times[0]))
			p.add("coordTimes", string(b))
		case len(times) > 1:
			for i := range times {
				times[i] = nonNil(times[i])
			}
			b, _ := json.Marshal(times)
			p.add("coordTimes", string(b))
		}
	}
	for _, kv := range simpleStyle(g.d, pm) {
		p.add(kv[0], kv[1])
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// resolveStyle returns the effective normal style of a placemark.
func resolveStyle(d *Doc, pm *Placemark) *Style {
	var base *Style
	if id, ok := localStyleID(pm.StyleURL); ok && pm.StyleURL != "" {
		base = d.normalStyle(id, 0)
	}
	if pm.InlineStyle == nil {
		return base
	}
	if base == nil {
		return pm.InlineStyle
	}
	m := *base
	in := pm.InlineStyle
	if in.HasIcon {
		m.HasIcon, m.IconColor, m.IconHref, m.IconScale = true, in.IconColor, in.IconHref, in.IconScale
	}
	if in.HasLine {
		m.HasLine, m.LineColor, m.LineWidth = true, in.LineColor, in.LineWidth
	}
	if in.HasPoly {
		m.HasPoly, m.PolyColor, m.PolyFill, m.PolyOutline = true, in.PolyColor, in.PolyFill, in.PolyOutline
	}
	return &m
}

func (d *Doc) normalStyle(id string, depth int) *Style {
	if depth > 4 {
		return nil
	}
	if s := d.Styles[id]; s != nil {
		return s
	}
	if sm := d.StyleMaps[id]; sm != nil {
		if sm.NormalInline != nil {
			return sm.NormalInline
		}
		if nid, ok := localStyleID(sm.Normal); ok {
			return d.normalStyle(nid, depth+1)
		}
	}
	return nil
}

// kmlColor converts KML aabbggrr to "#rrggbb" and an opacity.
func kmlColor(s string) (string, float64, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 8 {
		return "", 0, false
	}
	if _, err := strconv.ParseUint(s, 16, 32); err != nil {
		return "", 0, false
	}
	a, _ := strconv.ParseUint(s[0:2], 16, 8)
	return "#" + strings.ToLower(s[6:8]+s[4:6]+s[2:4]), math.Round(float64(a)/255*100) / 100, true
}

func simpleStyle(d *Doc, pm *Placemark) [][2]string {
	st := resolveStyle(d, pm)
	if st == nil || pm.Geometry == nil {
		return nil
	}
	var hasPt, hasLine, hasPoly bool
	for _, l := range pm.Geometry.leaves() {
		switch l.Type {
		case "Point":
			hasPt = true
		case "LineString", "LinearRing", "Track":
			hasLine = true
		case "Polygon":
			hasPoly = true
		}
	}
	var out [][2]string
	if (hasLine || hasPoly) && st.HasLine {
		if c, a, ok := kmlColor(st.LineColor); ok {
			out = append(out, [2]string{"stroke", jstr(c)}, [2]string{"stroke-opacity", formatFloat(a)})
		}
		if w, err := strconv.ParseFloat(st.LineWidth, 64); err == nil && !math.IsInf(w, 0) && !math.IsNaN(w) {
			out = append(out, [2]string{"stroke-width", formatFloat(w)})
		}
	}
	if hasPoly && st.HasPoly {
		if c, a, ok := kmlColor(st.PolyColor); ok {
			if st.PolyFill == "0" || strings.EqualFold(st.PolyFill, "false") {
				a = 0
			}
			out = append(out, [2]string{"fill", jstr(c)}, [2]string{"fill-opacity", formatFloat(a)})
		} else if st.PolyFill == "0" || strings.EqualFold(st.PolyFill, "false") {
			out = append(out, [2]string{"fill-opacity", "0"})
		}
	}
	if hasPt && st.HasIcon {
		if c, _, ok := kmlColor(st.IconColor); ok {
			out = append(out, [2]string{"marker-color", jstr(c)})
		}
	}
	return out
}

// gjGeom is a geometry in GeoJSON terms.
type gjGeom struct {
	Type   string
	Pos    []Coord
	Lines  [][]Coord
	Polys  [][][]Coord
	Parts  []*gjGeom
	Source *Geometry
}

func toGeoJSONGeom(g *Geometry) *gjGeom {
	if g == nil {
		return nil
	}
	switch g.Type {
	case "Point":
		if len(g.Coords) == 0 {
			return nil
		}
		return &gjGeom{Type: "Point", Pos: g.Coords[:1], Source: g}
	case "LineString", "LinearRing", "Track":
		return &gjGeom{Type: "LineString", Pos: g.Coords, Source: g}
	case "Polygon":
		rings := make([][]Coord, 0, len(g.Rings))
		for i, r := range g.Rings {
			rings = append(rings, rfcRing(r, i == 0 && !g.NoOuter))
		}
		return &gjGeom{Type: "Polygon", Polys: [][][]Coord{rings}, Source: g}
	case "Model":
		return nil
	case "MultiPoint", "MultiLineString", "MultiPolygon", "MultiGeometry", "MultiTrack", "GeometryCollection":
		var parts []*gjGeom
		for _, p := range g.Parts {
			if c := toGeoJSONGeom(p); c != nil {
				parts = append(parts, c)
			}
		}
		if len(parts) == 0 {
			return nil
		}
		if g.Type == "GeometryCollection" {
			return &gjGeom{Type: "GeometryCollection", Parts: parts, Source: g}
		}
		// Merge into a Multi* type when every part has the same simple type.
		same := true
		for _, p := range parts {
			if p.Type != parts[0].Type || p.Type == "GeometryCollection" || strings.HasPrefix(p.Type, "Multi") {
				same = false
			}
		}
		if same {
			m := &gjGeom{Type: "Multi" + parts[0].Type, Source: g}
			for _, p := range parts {
				switch p.Type {
				case "Point":
					m.Pos = append(m.Pos, p.Pos...)
				case "LineString":
					m.Lines = append(m.Lines, p.Pos)
				case "Polygon":
					m.Polys = append(m.Polys, p.Polys[0])
				}
			}
			return m
		}
		// Flatten nested collections (RFC 7946 advises against nesting).
		var flat []*gjGeom
		var rec func(ps []*gjGeom)
		rec = func(ps []*gjGeom) {
			for _, p := range ps {
				if p.Type == "GeometryCollection" {
					rec(p.Parts)
				} else {
					flat = append(flat, p)
				}
			}
		}
		rec(parts)
		return &gjGeom{Type: "GeometryCollection", Parts: flat, Source: g}
	}
	return nil
}

// rfcRing returns a closed copy of r wound the RFC 7946 way.
func rfcRing(r []Coord, outer bool) []Coord {
	out := append([]Coord(nil), r...)
	if len(out) > 0 && !ringClosed(out) {
		out = append(out, out[0])
	}
	if len(out) >= 4 && wrongWinding(out, outer) {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

func (g *gjw) pos(c Coord) {
	s := "[" + formatFloat(c.Lon) + "," + formatFloat(c.Lat)
	if c.Has3 {
		s += "," + formatFloat(c.Alt)
	}
	g.s(s + "]")
}

func (g *gjw) list(cs []Coord) {
	g.s("[")
	for i, c := range cs {
		if i > 0 {
			g.s(",")
		}
		g.pos(c)
	}
	g.s("]")
}

func (g *gjw) rings(rs [][]Coord) {
	g.s("[")
	for i, r := range rs {
		if i > 0 {
			g.s(",")
		}
		g.list(r)
	}
	g.s("]")
}

func (g *gjw) geom(x *gjGeom) {
	g.s(`{"type":"` + x.Type + `"`)
	if g.d.Format == FormatGeoJSON && x.Source != nil {
		for _, m := range x.Source.Foreign {
			g.s("," + jstr(m.Key) + ":" + compact(m.Value))
		}
	}
	switch x.Type {
	case "Point":
		g.s(`,"coordinates":`)
		g.pos(x.Pos[0])
	case "LineString", "MultiPoint":
		g.s(`,"coordinates":`)
		g.list(x.Pos)
	case "MultiLineString":
		g.s(`,"coordinates":`)
		g.rings(x.Lines)
	case "Polygon":
		g.s(`,"coordinates":`)
		g.rings(x.Polys[0])
	case "MultiPolygon":
		g.s(`,"coordinates":[`)
		for i, p := range x.Polys {
			if i > 0 {
				g.s(",")
			}
			g.rings(p)
		}
		g.s("]")
	case "GeometryCollection":
		g.s(`,"geometries":[`)
		for i, p := range x.Parts {
			if i > 0 {
				g.s(",")
			}
			g.geom(p)
		}
		g.s("]")
	}
	g.s("}")
}

// -------------------------------------------------------------------- KML

// WriteKML writes the document as KML 2.2. KML and KMZ input is written
// back byte for byte, apart from changes made by Clean, so unknown
// elements, comments and formatting survive. GeoJSON input is converted.
func WriteKML(d *Doc, w io.Writer) error {
	if d.fatal {
		return ErrUnreadable
	}
	if d.raw != nil {
		_, err := w.Write(d.spliced())
		return err
	}
	return writeGeneratedKML(d, w)
}

// KMZOptions controls WriteKMZ.
type KMZOptions struct {
	// Extra files to add to the archive, keyed by their path inside it.
	// The CLI uses this to bundle files that a plain .kml refers to.
	Extra map[string][]byte
}

// WriteKMZ writes a KMZ archive: the KML document plus, for KMZ input,
// every file that was inside the original archive (Clean may have dropped
// unused ones).
func WriteKMZ(d *Doc, w io.Writer, opt *KMZOptions) error {
	if d.fatal {
		return ErrUnreadable
	}
	var kml bytes.Buffer
	if err := WriteKML(d, &kml); err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(name string, data []byte, method uint16) error {
		h := &zip.FileHeader{Name: name, Method: method, Modified: mod}
		f, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	main := d.MainPath
	if main == "" {
		main = "doc.kml"
	}
	if err := add(main, kml.Bytes(), zip.Deflate); err != nil {
		return err
	}
	for _, a := range d.Assets {
		e := d.assetData[a.Path]
		if e == nil || e.data == nil {
			continue
		}
		if err := add(a.Path, e.data, zip.Deflate); err != nil {
			return err
		}
	}
	if opt != nil {
		keys := make([]string, 0, len(opt.Extra))
		for k := range opt.Extra {
			if _, exists := d.assetData[k]; !exists && k != main {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := add(k, opt.Extra[k], zip.Deflate); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

type edit struct {
	start, end int
	text       string
}

// spliced returns the KML bytes with the document's pending edits applied.
func (d *Doc) spliced() []byte {
	if len(d.edits) == 0 {
		return d.raw
	}
	es := append([]edit(nil), d.edits...)
	sort.SliceStable(es, func(i, j int) bool { return es[i].start < es[j].start })
	var b bytes.Buffer
	b.Grow(len(d.raw))
	pos := 0
	for _, e := range es {
		if e.start < pos {
			continue // overlapping edit; keep the first
		}
		b.Write(d.raw[pos:e.start])
		b.WriteString(e.text)
		pos = e.end
	}
	b.Write(d.raw[pos:])
	return b.Bytes()
}

func xmlEsc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\t', '\n', '\r':
			b.WriteRune(r)
		default:
			if r < 0x20 || r == 0xFFFE || r == 0xFFFF {
				continue // not allowed in XML 1.0
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isNCName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == '-' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r > 0x7F
		if i == 0 && (r == '-' || r == '.' || r >= '0' && r <= '9') {
			ok = false
		}
		if !ok {
			return false
		}
	}
	return true
}

type kw struct {
	w     *bufio.Writer
	d     *Doc
	err   error
	gx    bool
	depth int
}

func (k *kw) s(s string) {
	if k.err == nil {
		_, k.err = k.w.WriteString(s)
	}
}

func (k *kw) line(s string) {
	k.s(strings.Repeat("  ", k.depth) + s + "\n")
}

func writeGeneratedKML(d *Doc, w io.Writer) error {
	bw := bufio.NewWriter(w)
	k := &kw{w: bw, d: d}
	pms := d.Placemarks()
	for _, pm := range pms {
		if pm.Geometry != nil {
			for _, l := range pm.Geometry.leaves() {
				if l.Type == "Track" {
					k.gx = true
				}
			}
		}
		if trackTimes(pm) != nil {
			k.gx = true
		}
	}
	k.s(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	if k.gx {
		k.s(`<kml xmlns="` + NSKML22 + `" xmlns:gx="` + NSGx + `">` + "\n")
	} else {
		k.s(`<kml xmlns="` + NSKML22 + `">` + "\n")
	}
	k.depth = 1
	k.line("<Document>")
	k.depth++
	if d.Filename != "" {
		k.line("<name>" + xmlEsc(d.Filename) + "</name>")
	}
	// Rebuild folders from the "folder" property. A stack keeps document
	// order exactly: folders open and close as the path changes.
	var open []string
	for _, pm := range pms {
		var path []string
		if fp := folderProp(pm); fp != "" {
			path = strings.Split(fp, " / ")
		}
		common := 0
		for common < len(open) && common < len(path) && open[common] == path[common] {
			common++
		}
		for len(open) > common {
			k.depth--
			k.line("</Folder>")
			open = open[:len(open)-1]
		}
		for _, name := range path[common:] {
			k.line("<Folder>")
			k.depth++
			k.line("<name>" + xmlEsc(name) + "</name>")
			open = append(open, name)
		}
		k.placemark(pm)
	}
	for range open {
		k.depth--
		k.line("</Folder>")
	}
	k.depth--
	k.line("</Document>")
	k.s("</kml>\n")
	if k.err != nil {
		return k.err
	}
	return bw.Flush()
}

func folderProp(pm *Placemark) string {
	if pm.Props != nil {
		if raw, ok := getProp(pm.Props, "folder"); ok {
			s, _ := jsonString(raw)
			return s
		}
		return ""
	}
	return strings.Join(pm.FolderPath, " / ")
}

// reserved GeoJSON properties that map to KML elements.
var mappedProps = map[string]bool{
	"name": true, "description": true, "folder": true, "timestamp": true, "begin": true, "end": true,
	"coordTimes": true, "stroke": true, "stroke-width": true, "stroke-opacity": true, "fill": true,
	"fill-opacity": true, "marker-color": true,
}

func propStr(pm *Placemark, key string) (string, bool) {
	raw, ok := getProp(pm.Props, key)
	if !ok {
		return "", false
	}
	return jsonString(raw)
}

func trackTimes(pm *Placemark) [][]string {
	if pm.Props == nil || pm.Geometry == nil {
		return nil
	}
	raw, ok := getProp(pm.Props, "coordTimes")
	if !ok {
		return nil
	}
	switch pm.Geometry.Type {
	case "LineString":
		var t []string
		if json.Unmarshal(raw, &t) == nil && len(t) == len(pm.Geometry.Coords) && len(t) > 0 {
			return [][]string{t}
		}
	case "MultiLineString":
		var t [][]string
		if json.Unmarshal(raw, &t) == nil && len(t) == len(pm.Geometry.Parts) {
			for i, p := range pm.Geometry.Parts {
				if len(t[i]) != len(p.Coords) {
					return nil
				}
			}
			return t
		}
	}
	return nil
}

// cssColor converts "#rrggbb" or "#rgb" and an opacity to KML aabbggrr.
func cssColor(c string, op float64) (string, bool) {
	c = strings.TrimPrefix(strings.TrimSpace(c), "#")
	if len(c) == 3 {
		c = string([]byte{c[0], c[0], c[1], c[1], c[2], c[2]})
	}
	if len(c) != 6 {
		return "", false
	}
	if _, err := strconv.ParseUint(c, 16, 32); err != nil {
		return "", false
	}
	if math.IsNaN(op) || op < 0 {
		op = 0
	}
	if op > 1 {
		op = 1
	}
	a := int(math.Round(op * 255))
	return strings.ToLower(fmt.Sprintf("%02x%s%s%s", a, c[4:6], c[2:4], c[0:2])), true
}

func propNum(pm *Placemark, key string, def float64) float64 {
	raw, ok := getProp(pm.Props, key)
	if !ok {
		return def
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return def
	}
	return f
}

func (k *kw) placemark(pm *Placemark) {
	open := "<Placemark>"
	idData := ""
	if pm.ID != "" {
		if isNCName(pm.ID) {
			open = `<Placemark id="` + xmlEsc(pm.ID) + `">`
		} else {
			idData = pm.ID
		}
	}
	k.line(open)
	k.depth++
	if pm.Name != "" {
		k.line("<name>" + xmlEsc(pm.Name) + "</name>")
	}
	if pm.Description != "" {
		k.line("<description>" + xmlEsc(pm.Description) + "</description>")
	}
	ts, b, e := pm.TimeStamp, pm.Begin, pm.End
	if pm.Props != nil {
		ts, _ = propStr(pm, "timestamp")
		b, _ = propStr(pm, "begin")
		e, _ = propStr(pm, "end")
	}
	if ts != "" {
		k.line("<TimeStamp><when>" + xmlEsc(ts) + "</when></TimeStamp>")
	} else if b != "" || e != "" {
		s := "<TimeSpan>"
		if b != "" {
			s += "<begin>" + xmlEsc(b) + "</begin>"
		}
		if e != "" {
			s += "<end>" + xmlEsc(e) + "</end>"
		}
		k.line(s + "</TimeSpan>")
	}
	if pm.Props != nil {
		k.inlineStyle(pm)
	}
	// ExtendedData
	type kv struct{ k, v string }
	var data []kv
	if idData != "" {
		data = append(data, kv{"id", idData})
	}
	if pm.Props != nil {
		times := trackTimes(pm)
		for _, m := range pm.Props {
			if mappedProps[m.Key] {
				if m.Key == "coordTimes" && times == nil {
					data = append(data, kv{m.Key, compact(m.Value)})
					continue
				}
				if m.Key == "name" || m.Key == "description" || m.Key == "folder" || m.Key == "timestamp" || m.Key == "begin" || m.Key == "end" {
					if _, ok := jsonString(m.Value); !ok {
						if string(m.Value) != "null" {
							data = append(data, kv{m.Key, compact(m.Value)})
						}
					}
				}
				continue
			}
			if string(m.Value) == "null" {
				continue
			}
			if s, ok := jsonString(m.Value); ok {
				data = append(data, kv{m.Key, s})
			} else {
				data = append(data, kv{m.Key, compact(m.Value)})
			}
		}
	} else {
		for _, f := range pm.Fields {
			data = append(data, kv{f.Name, f.Value})
		}
	}
	if len(data) > 0 {
		k.line("<ExtendedData>")
		k.depth++
		for _, x := range data {
			k.line(`<Data name="` + xmlEsc(x.k) + `"><value>` + xmlEsc(x.v) + `</value></Data>`)
		}
		k.depth--
		k.line("</ExtendedData>")
	}
	if pm.Geometry != nil {
		var times [][]string
		if pm.Props != nil {
			times = trackTimes(pm)
		}
		k.geometry(pm.Geometry, times, false)
	}
	k.depth--
	k.line("</Placemark>")
}

func (k *kw) inlineStyle(pm *Placemark) {
	var parts []string
	if c, ok := propStr(pm, "marker-color"); ok {
		if col, ok := cssColor(c, 1); ok {
			parts = append(parts, "<IconStyle><color>"+col+"</color></IconStyle>")
		}
	}
	if c, ok := propStr(pm, "stroke"); ok {
		if col, ok := cssColor(c, propNum(pm, "stroke-opacity", 1)); ok {
			s := "<LineStyle><color>" + col + "</color>"
			if _, has := getProp(pm.Props, "stroke-width"); has {
				s += "<width>" + formatFloat(propNum(pm, "stroke-width", 1)) + "</width>"
			}
			parts = append(parts, s+"</LineStyle>")
		}
	}
	if c, ok := propStr(pm, "fill"); ok {
		if col, ok := cssColor(c, propNum(pm, "fill-opacity", 0.6)); ok {
			parts = append(parts, "<PolyStyle><color>"+col+"</color></PolyStyle>")
		}
	}
	if len(parts) > 0 {
		k.line("<Style>" + strings.Join(parts, "") + "</Style>")
	}
}

func altMode(cs []Coord) string {
	for _, c := range cs {
		if c.Has3 && c.Alt != 0 {
			return "<altitudeMode>absolute</altitudeMode>"
		}
	}
	return ""
}

func (k *kw) geometry(g *Geometry, times [][]string, inMulti bool) {
	switch g.Type {
	case "Point":
		if len(g.Coords) == 0 {
			return
		}
		k.line("<Point>" + altMode(g.Coords) + "<coordinates>" + kmlTuple(g.Coords[0]) + "</coordinates></Point>")
	case "LineString", "LinearRing":
		if len(times) == 1 && g.Type == "LineString" {
			k.track(g.Coords, times[0])
			return
		}
		k.line("<" + g.Type + ">" + altMode(g.Coords) + "<coordinates>" + kmlCoords(g.Coords) + "</coordinates></" + g.Type + ">")
	case "Track":
		k.track(g.Coords, g.When)
	case "Polygon":
		var all []Coord
		for _, r := range g.Rings {
			all = append(all, r...)
		}
		k.line("<Polygon>" + altMode(all))
		k.depth++
		for i, r := range g.Rings {
			tag := "innerBoundaryIs"
			if i == 0 && !g.NoOuter {
				tag = "outerBoundaryIs"
			}
			k.line("<" + tag + "><LinearRing><coordinates>" + kmlCoords(r) + "</coordinates></LinearRing></" + tag + ">")
		}
		k.depth--
		k.line("</Polygon>")
	case "Model":
	default: // Multi*, GeometryCollection, MultiGeometry, MultiTrack
		if g.Type == "MultiLineString" && len(times) == len(g.Parts) && len(times) > 0 {
			k.line("<gx:MultiTrack>")
			k.depth++
			for i, p := range g.Parts {
				k.track(p.Coords, times[i])
			}
			k.depth--
			k.line("</gx:MultiTrack>")
			return
		}
		k.line("<MultiGeometry>")
		k.depth++
		for _, p := range g.Parts {
			k.geometry(p, nil, true)
		}
		k.depth--
		k.line("</MultiGeometry>")
	}
}

func (k *kw) track(cs []Coord, when []string) {
	k.line("<gx:Track>" + altMode(cs))
	k.depth++
	for _, w := range when {
		k.line("<when>" + xmlEsc(w) + "</when>")
	}
	for _, c := range cs {
		s := formatFloat(c.Lon) + " " + formatFloat(c.Lat)
		if c.Has3 {
			s += " " + formatFloat(c.Alt)
		}
		k.line("<gx:coord>" + s + "</gx:coord>")
	}
	k.depth--
	k.line("</gx:Track>")
}
