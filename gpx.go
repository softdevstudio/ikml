package ikml

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GPX namespaces.
const (
	NSGPX10 = "http://www.topografix.com/GPX/1/0"
	NSGPX11 = "http://www.topografix.com/GPX/1/1"
)

// gpxInfo keeps what a GPX file holds beyond the shared model: the parts
// GPX output copies verbatim, and the counts the loss reports need.
type gpxInfo struct {
	raw     []byte // the file as UTF-8; GPX output is this file, with fixed coordinates spliced in
	version string
	meta    bool // the file has metadata (GPX 1.1 <metadata>, or GPX 1.0's name, author, time and the like)
	ext     bool // the root has <extensions>
	unknown int  // root children that aren't GPX

	metaNames   []string
	ptExt       map[string]int // element names inside point extensions, by points carrying them
	ptExtPoints int
	ptOther     map[string]int // other per-point children of track points
	rteDetail   map[string]int // route point children other than ele
	featExt     int            // waypoints, routes and tracks with extensions
	featOther   map[string]int // waypoint, route and track children with no place in the model
	partial     int            // segments where only some points have a time
	segmented   int            // tracks with more than one segment
	badPoints   int            // points dropped for unreadable lat/lon
}

// gpx10Meta are the GPX 1.0 root children that GPX 1.1 moved into <metadata>.
var gpx10Meta = map[string]bool{"name": true, "desc": true, "author": true, "email": true, "url": true, "urlname": true, "time": true, "keywords": true, "bounds": true}

// coordSpans returns the byte ranges of the lat and lon values in the start
// tag at raw[start:end], as offsets into raw.
func coordSpans(raw []byte, start, end int) [2][2]int {
	out := [2][2]int{{-1, -1}, {-1, -1}}
	t := raw[start:end]
	i := 1 // past '<'
	for i < len(t) && !isXMLSpace(t[i]) && t[i] != '>' && t[i] != '/' {
		i++ // element name
	}
	for i < len(t) {
		for i < len(t) && isXMLSpace(t[i]) {
			i++
		}
		if i >= len(t) || t[i] == '>' || t[i] == '/' {
			break
		}
		ns := i
		for i < len(t) && !isXMLSpace(t[i]) && t[i] != '=' && t[i] != '>' {
			i++
		}
		name := string(t[ns:i])
		for i < len(t) && isXMLSpace(t[i]) {
			i++
		}
		if i >= len(t) || t[i] != '=' {
			break
		}
		i++
		for i < len(t) && isXMLSpace(t[i]) {
			i++
		}
		if i >= len(t) || (t[i] != '"' && t[i] != '\'') {
			break
		}
		q := t[i]
		i++
		vs := i
		for i < len(t) && t[i] != q {
			i++
		}
		if i >= len(t) {
			break
		}
		switch name {
		case "lat":
			out[0] = [2]int{start + vs, start + i}
		case "lon":
			out[1] = [2]int{start + vs, start + i}
		}
		i++
	}
	return out
}

func isXMLSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// isGPX reports whether XML data has a gpx root element.
func isGPX(data []byte) bool {
	raw, _, err := toUTF8(data)
	if err != nil {
		return false
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	for i := 0; i < 10000; i++ {
		t, err := dec.Token()
		if err != nil {
			return false
		}
		if se, ok := t.(xml.StartElement); ok {
			return se.Name.Local == "gpx"
		}
	}
	return false
}

type gpxp struct {
	d    *Doc
	raw  []byte
	dec  *xml.Decoder
	info *gpxInfo
	n    map[string]int // wpt, rte, trk counters for paths
}

func (p *gpxp) line() int {
	l, _ := p.dec.InputPos()
	return l
}

// next returns the next token and the offset where it starts.
func (p *gpxp) next() (xml.Token, int, error) {
	off := int(p.dec.InputOffset())
	t, err := p.dec.Token()
	return t, off, err
}

func openGPX(d *Doc, data []byte) {
	d.Root = &Folder{Kind: "gpx", Path: "/gpx"}
	raw, enc, err := toUTF8(data)
	if err != nil {
		d.issues.add("xml-invalid", Loc{Line: 1}, 0, "%s", err.Error())
		d.fatal = true
		return
	}
	if enc != "" {
		d.encoding = enc
		d.issues.add("encoding-converted", Loc{Line: 1}, 0, "The file is encoded as %s. iKML converted it to UTF-8; cleaned output is UTF-8.", enc)
	}
	info := &gpxInfo{raw: raw, ptExt: map[string]int{}, ptOther: map[string]int{}, rteDetail: map[string]int{}, featOther: map[string]int{}}
	d.gpx = info
	p := &gpxp{d: d, raw: raw, dec: xml.NewDecoder(bytes.NewReader(raw)), info: info, n: map[string]int{}}
	if err := p.parse(); err != nil {
		line := 0
		msg := err.Error()
		var se *xml.SyntaxError
		if errors.As(err, &se) {
			line = se.Line
			msg = se.Msg
		}
		d.fatal = true
		if line > 0 {
			d.issues.add("xml-invalid", Loc{Line: line}, 0, "The GPX is broken at line %d: %s.%s iKML shows only what it could read before that point.", line, msg, xmlHint(msg))
		} else {
			d.issues.add("xml-invalid", Loc{}, 0, "The GPX could not be read: %s.", msg)
		}
	}
}

func (p *gpxp) parse() error {
	// The root element.
	for {
		t, off, err := p.next()
		if err != nil {
			return err
		}
		se, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		_ = off
		p.d.Namespace = se.Name.Space
		for _, a := range se.Attr {
			if a.Name.Local == "version" && a.Name.Space == "" {
				p.info.version = a.Value
			}
		}
		switch {
		case p.info.version == "1.0" || p.info.version == "1.1":
		case p.info.version == "":
			p.d.issues.add("gpx-version", Loc{Path: "/gpx", Line: p.line()}, 0, "The <gpx> root has no version attribute. GPX 1.1 is assumed.")
		default:
			p.d.issues.add("gpx-version", Loc{Path: "/gpx", Line: p.line()}, 0, "The GPX version is %q. iKML reads it as GPX 1.1, the version it knows.", p.info.version)
		}
		break
	}
	for {
		t, off, err := p.next()
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		if err != nil {
			return err
		}
		switch v := t.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			switch v.Name.Local {
			case "wpt", "rte", "trk":
				if err := p.feature(v, off); err != nil {
					return err
				}
			case "metadata", "extensions":
				names, err := p.skipNames()
				if err != nil {
					return err
				}
				if v.Name.Local == "metadata" {
					p.info.meta = true
					p.info.metaNames = append(p.info.metaNames, names...)
				} else {
					p.info.ext = true
				}
			default:
				if err := p.dec.Skip(); err != nil {
					return err
				}
				if gpx10Meta[v.Name.Local] {
					// GPX 1.0 keeps the file's name, author, time and the
					// like directly under the root.
					p.info.meta = true
					p.info.metaNames = append(p.info.metaNames, v.Name.Local)
					continue
				}
				p.info.unknown++
				p.d.issues.add("unknown-element", Loc{Path: "/gpx", Line: p.line()}, 0, "<%s> is not part of GPX. iKML keeps it in GPX output and leaves it out of conversions.", v.Name.Local)
			}
		}
	}
}

// rawName returns the element name of a raw start tag such as "<gpx ...>".
func rawName(tag string) string {
	s := strings.TrimPrefix(tag, "<")
	if i := strings.IndexAny(s, " \t\r\n/>"); i >= 0 {
		return s[:i]
	}
	return s
}

// skipNames skips the current element and returns the names of its child
// elements, in order, without repeats.
func (p *gpxp) skipNames() ([]string, error) {
	var names []string
	seen := map[string]bool{}
	depth := 1
	for depth > 0 {
		t, err := p.dec.Token()
		if err != nil {
			return names, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			if depth == 1 && !seen[v.Name.Local] {
				seen[v.Name.Local] = true
				names = append(names, v.Name.Local)
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return names, nil
}

// leafNames skips the current element (an extensions element) and adds the
// names of the elements inside it that hold text to into.
func (p *gpxp) leafNames(into map[string]bool) error {
	type frame struct {
		name  string
		child bool
	}
	stack := []frame{{name: "extensions"}}
	for len(stack) > 0 {
		t, err := p.dec.Token()
		if err != nil {
			return err
		}
		switch v := t.(type) {
		case xml.StartElement:
			stack[len(stack)-1].child = true
			stack = append(stack, frame{name: v.Name.Local})
		case xml.EndElement:
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !f.child && len(stack) > 0 {
				into[f.name] = true
			}
		}
	}
	return nil
}

func (p *gpxp) text() (string, error) {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		t, err := p.dec.Token()
		if err != nil {
			return b.String(), err
		}
		switch v := t.(type) {
		case xml.CharData:
			if depth == 1 {
				b.Write(v)
			}
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// gpxFields are the waypoint, route and track children kept as fields.
var gpxFields = map[string]bool{"cmt": true, "src": true, "sym": true, "type": true, "number": true}

func latLon(se xml.StartElement) (Coord, string) {
	var lat, lon string
	var hasLat, hasLon bool
	for _, a := range se.Attr {
		if a.Name.Space != "" {
			continue // d:lat and the like are not the point's position
		}
		switch a.Name.Local {
		case "lat":
			lat, hasLat = a.Value, true
		case "lon":
			lon, hasLon = a.Value, true
		}
	}
	if !hasLat || !hasLon {
		return Coord{}, "it has no lat or lon attribute"
	}
	la, e1 := strconv.ParseFloat(strings.TrimSpace(lat), 64)
	lo, e2 := strconv.ParseFloat(strings.TrimSpace(lon), 64)
	if e1 != nil || e2 != nil {
		return Coord{}, fmt.Sprintf("lat=%q lon=%q are not numbers", lat, lon)
	}
	return Coord{Lon: lo, Lat: la}, ""
}

func (p *gpxp) feature(se xml.StartElement, start int) error {
	kind := se.Name.Local
	p.n[kind]++
	path := fmt.Sprintf("/gpx/%s[%d]", kind, p.n[kind])
	line := p.line()
	pm := &Placemark{Path: path, Line: line}
	innerStart := int(p.dec.InputOffset())
	var segs []*Geometry
	var wptCoord Coord
	wptOK := false
	if kind == "wpt" {
		c, why := latLon(se)
		if why != "" {
			p.d.issues.add("coord-parse", Loc{Path: path, Line: line}, 0, "The waypoint's position can't be read: %s. It is left out of every output.", why)
			p.info.badPoints++
			return p.dec.Skip()
		}
		wptCoord, wptOK = c, true
	}
	var rte *Geometry
	if kind == "rte" {
		rte = &Geometry{Type: "LineString", Line: line}
	}
	hasExt := false
	for {
		t, off, err := p.next()
		if err != nil {
			return err
		}
		switch v := t.(type) {
		case xml.EndElement:
			if hasExt {
				p.info.featExt++
			}
			switch kind {
			case "wpt":
				if wptOK {
					pm.Geometry = &Geometry{Type: "Point", Coords: []Coord{wptCoord}, Line: line,
						ptSpan: [][2][2]int{coordSpans(p.raw, start, innerStart)}, ptOrig: []Coord{wptCoord}}
				}
			case "rte":
				if len(rte.Coords) > 0 {
					pm.Geometry = rte
				}
			case "trk":
				switch len(segs) {
				case 0:
				case 1:
					pm.Geometry = segs[0]
				default:
					pm.Geometry = &Geometry{Type: "MultiTrack", Parts: segs, Line: line}
					p.info.segmented++
				}
			}
			p.d.Root.Items = append(p.d.Root.Items, Item{Placemark: pm})
			return nil
		case xml.StartElement:
			name := v.Name.Local
			switch {
			case kind == "rte" && name == "rtept":
				if err := p.point(v, off, pm, rte, true); err != nil {
					return err
				}
			case kind == "trk" && name == "trkseg":
				seg, err := p.segment(pm, len(segs)+1)
				if err != nil {
					return err
				}
				if seg != nil {
					segs = append(segs, seg)
				}
			case name == "name":
				s, err := p.text()
				if err != nil {
					return err
				}
				pm.Name = s
			case name == "desc":
				s, err := p.text()
				if err != nil {
					return err
				}
				pm.Description = s
			case kind == "wpt" && name == "ele":
				s, err := p.text()
				if err != nil {
					return err
				}
				if a, e := strconv.ParseFloat(s, 64); e == nil {
					wptCoord.Alt, wptCoord.Has3 = a, true
				} else {
					p.d.issues.add("coord-parse", Loc{Path: path, Line: p.line()}, 0, "The elevation %q is not a number. It is left out of conversions.", s)
				}
			case kind == "wpt" && name == "time":
				s, err := p.text()
				if err != nil {
					return err
				}
				if _, e := time.Parse(time.RFC3339Nano, s); e != nil {
					p.d.issues.add("gpx-time-invalid", Loc{Path: path, Line: p.line()}, 0, "The time %q is not an RFC 3339 time such as 2026-05-01T07:30:00Z.", s)
				}
				pm.TimeStamp = s
			case gpxFields[name]:
				s, err := p.text()
				if err != nil {
					return err
				}
				pm.Fields = append(pm.Fields, Field{Name: name, Value: s, Source: "gpx"})
			case name == "link":
				href := ""
				for _, a := range v.Attr {
					if a.Name.Local == "href" {
						href = a.Value
					}
				}
				names, err := p.skipNames()
				if err != nil {
					return err
				}
				for _, n := range names {
					p.info.featOther["link "+n]++ // link text and type
				}
				if href != "" {
					pm.Fields = append(pm.Fields, Field{Name: "link", Value: href, Source: "gpx"})
				}
			case name == "extensions":
				hasExt = true
				if err := p.dec.Skip(); err != nil {
					return err
				}
			default:
				p.info.featOther[name]++
				if err := p.dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// segment reads one trkseg. It returns nil for a segment with no points.
func (p *gpxp) segment(pm *Placemark, n int) (*Geometry, error) {
	g := &Geometry{Type: "Track", Line: p.line()}
	for {
		t, off, err := p.next()
		if err != nil {
			return nil, err
		}
		switch v := t.(type) {
		case xml.EndElement:
			if len(g.Coords) == 0 {
				return nil, nil
			}
			timed := 0
			for _, w := range g.When {
				if w != "" {
					timed++
				}
			}
			// The times that are there are checked, also in a segment
			// where only some points have one.
			loc := Loc{Path: fmt.Sprintf("%s/trkseg[%d]", pm.Path, n), Line: g.Line}
			backwards, invalid := 0, 0
			var prev time.Time
			for _, w := range g.When {
				if w == "" {
					continue
				}
				tm, e := time.Parse(time.RFC3339Nano, w)
				if e != nil {
					invalid++
					continue
				}
				if !prev.IsZero() && tm.Before(prev) {
					backwards++
				}
				prev = tm
			}
			if invalid > 0 {
				p.d.issues.add("gpx-time-invalid", loc, invalid, "%s in this segment %s not an RFC 3339 time such as 2026-05-01T07:30:00Z.", count(invalid, "time", "times"), plural(invalid, "is", "are"))
			}
			if backwards > 0 {
				p.d.issues.add("gpx-time-backwards", loc, backwards, "%s in this segment %s earlier than the point before. Speeds and durations come out wrong.", count(backwards, "point", "points"), plural(backwards, "is", "are"))
			}
			if timed != len(g.When) {
				if timed > 0 {
					p.info.partial++
				}
				g.Type = "LineString"
				g.When = nil
			}
			return g, nil
		case xml.StartElement:
			if v.Name.Local != "trkpt" {
				if v.Name.Local == "extensions" {
					p.info.featExt++
				}
				if err := p.dec.Skip(); err != nil {
					return nil, err
				}
				continue
			}
			if err := p.point(v, off, pm, g, false); err != nil {
				return nil, err
			}
		}
	}
}

// point reads one rtept or trkpt into g, keeping its children verbatim.
func (p *gpxp) point(se xml.StartElement, start int, pm *Placemark, g *Geometry, route bool) error {
	line := p.line()
	c, why := latLon(se)
	inner := int(p.dec.InputOffset())
	spans := coordSpans(p.raw, start, inner)
	when := ""
	ext := map[string]bool{}
	for {
		t, off, err := p.next()
		if err != nil {
			return err
		}
		switch v := t.(type) {
		case xml.EndElement:
			if why != "" {
				p.d.issues.add("coord-parse", Loc{Path: pm.Path, Line: line}, 0, "A point's position can't be read: %s. It is left out of every output.", why)
				p.info.badPoints++
				return nil
			}
			_ = off
			g.Coords = append(g.Coords, c)
			g.When = append(g.When, when)
			g.ptSpan = append(g.ptSpan, spans)
			g.ptOrig = append(g.ptOrig, c)
			if len(ext) > 0 {
				p.info.ptExtPoints++
				for k := range ext {
					p.info.ptExt[k]++
				}
			}
			return nil
		case xml.StartElement:
			name := v.Name.Local
			switch name {
			case "ele":
				s, err := p.text()
				if err != nil {
					return err
				}
				if a, e := strconv.ParseFloat(s, 64); e == nil {
					c.Alt, c.Has3 = a, true
				}
			case "time":
				s, err := p.text()
				if err != nil {
					return err
				}
				when = s
				if route {
					p.info.rteDetail["time"]++
				}
			case "extensions":
				if err := p.leafNames(ext); err != nil {
					return err
				}
			default:
				if route {
					p.info.rteDetail[name]++
				} else {
					p.info.ptOther[name]++
				}
				if err := p.dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// WriteGPX writes d as GPX 1.1. A GPX source is written back with its
// metadata, extensions and point details unchanged; other formats are
// written from the model (see LossReport for what that leaves out).
func WriteGPX(d *Doc, w io.Writer) error {
	if d.fatal {
		return ErrUnreadable
	}
	bw := bufio.NewWriter(w)
	if d.gpx != nil {
		writeGPXSource(d, bw)
	} else {
		writeGeneratedGPX(d, bw)
	}
	return bw.Flush()
}

// writeGPXSource writes the file as it was, with the coordinates that
// clean changed spliced into their lat and lon attributes. Everything else,
// namespaces, comments, attributes and extensions included, stays byte for
// byte.
func writeGPXSource(d *Doc, bw *bufio.Writer) {
	raw := d.gpx.raw
	var edits []edit
	for _, pm := range d.Placemarks() {
		if pm.Geometry == nil {
			continue
		}
		for _, l := range pm.Geometry.leaves() {
			for i, c := range l.Coords {
				if i >= len(l.ptSpan) || i >= len(l.ptOrig) {
					continue
				}
				o, sp := l.ptOrig[i], l.ptSpan[i]
				if c.Lat != o.Lat && sp[0][0] >= 0 {
					edits = append(edits, edit{sp[0][0], sp[0][1], formatFloat(c.Lat)})
				}
				if c.Lon != o.Lon && sp[1][0] >= 0 {
					edits = append(edits, edit{sp[1][0], sp[1][1], formatFloat(c.Lon)})
				}
			}
		}
	}
	sort.Slice(edits, func(a, b int) bool { return edits[a].start < edits[b].start })
	pos := 0
	for _, e := range edits {
		if e.start < pos {
			continue
		}
		bw.Write(raw[pos:e.start])
		bw.WriteString(e.text)
		pos = e.end
	}
	bw.Write(raw[pos:])
}

// gpxText returns a placemark's name, description and timestamp, from
// GeoJSON properties when the source is GeoJSON.
func gpxText(pm *Placemark) (name, desc, ts string) {
	if pm.Props != nil {
		name, _ = propStr(pm, "name")
		desc, _ = propStr(pm, "description")
		ts, _ = propStr(pm, "timestamp")
		return
	}
	return pm.Name, pm.Description, pm.TimeStamp
}

type gpxLine struct {
	coords []Coord
	when   []string
}

// gpxShapes splits a placemark into the points and lines GPX can hold.
func gpxShapes(pm *Placemark) (points []Coord, lines []gpxLine) {
	if pm.Geometry == nil {
		return nil, nil
	}
	times := trackTimes(pm)
	leaves := pm.Geometry.leaves()
	li := 0
	for _, l := range leaves {
		switch l.Type {
		case "Point":
			if len(l.Coords) > 0 {
				points = append(points, l.Coords[0])
			}
		case "LineString", "LinearRing", "Track":
			if len(l.Coords) == 0 {
				continue
			}
			ln := gpxLine{coords: l.Coords}
			switch {
			case l.Type == "Track" && len(l.When) == len(l.Coords):
				ln.when = l.When
			case l.Type == "LineString" && times != nil && li < len(times):
				ln.when = times[li]
			}
			if l.Type == "LineString" {
				li++
			}
			lines = append(lines, ln)
		}
	}
	return points, lines
}

func writeGeneratedGPX(d *Doc, bw *bufio.Writer) {
	bw.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	bw.WriteString(`<gpx version="1.1" creator="iKML" xmlns="` + NSGPX11 + `">` + "\n")
	pms := d.Placemarks()
	text := func(tag, s string) string {
		if s == "" {
			return ""
		}
		return "<" + tag + ">" + xmlEsc(s) + "</" + tag + ">"
	}
	ele := func(c Coord) string {
		if !c.Has3 {
			return ""
		}
		return "<ele>" + formatFloat(c.Alt) + "</ele>"
	}
	for _, pm := range pms {
		points, _ := gpxShapes(pm)
		name, desc, ts := gpxText(pm)
		for _, c := range points {
			bw.WriteString(`  <wpt lat="` + formatFloat(c.Lat) + `" lon="` + formatFloat(c.Lon) + `">` + ele(c) + text("time", ts) + text("name", name) + text("desc", desc) + "</wpt>\n")
		}
	}
	for _, pm := range pms {
		_, lines := gpxShapes(pm)
		if len(lines) == 0 {
			continue
		}
		name, desc, _ := gpxText(pm)
		bw.WriteString("  <trk>" + text("name", name) + text("desc", desc) + "\n")
		for _, ln := range lines {
			bw.WriteString("    <trkseg>\n")
			for i, c := range ln.coords {
				t := ""
				if ln.when != nil && i < len(ln.when) {
					t = text("time", ln.when[i])
				}
				bw.WriteString(`      <trkpt lat="` + formatFloat(c.Lat) + `" lon="` + formatFloat(c.Lon) + `">` + ele(c) + t + "</trkpt>\n")
			}
			bw.WriteString("    </trkseg>\n")
		}
		bw.WriteString("  </trk>\n")
	}
	bw.WriteString("</gpx>\n")
}

// counted renders a name to count map as "hr (1800), cad (1800)", largest first.
func counted(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%s (%d)", k, m[k])
	}
	return out
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// lossFromGPX explains what converting a GPX file to GeoJSON, KML, KMZ or
// CSV leaves out.
func lossFromGPX(d *Doc, target string) []Loss {
	g := d.gpx
	out := []Loss{}
	if g.badPoints > 0 {
		out = append(out, Loss{What: "Points without a position", Count: g.badPoints, Outcome: "lost", Detail: "Waypoints, route points and track points whose lat or lon can't be read are left out."})
	}
	if target == "csv" {
		lines := 0
		for _, pm := range d.Placemarks() {
			if pm.Geometry != nil && pm.Geometry.Type != "Point" {
				lines++
			}
		}
		if lines > 0 {
			out = append(out, Loss{What: "Routes and tracks", Count: lines, Outcome: "lost", Detail: "A CSV row holds one point. Only waypoints are written."})
		}
	}
	if g.ptExtPoints > 0 && target != "csv" {
		out = append(out, Loss{What: "Track point extensions", Count: g.ptExtPoints, Outcome: "lost",
			Detail: fmt.Sprintf("%s carry extensions, such as heart rate, cadence, temperature or power from a watch or an app. They have no place in %s and are dropped. The number after each name is how many points have it.", count(g.ptExtPoints, "point", "points"), targetName(target)),
			Items:  firstN(counted(g.ptExt), 10)})
	}
	if n := sum(g.ptOther); n > 0 && target != "csv" {
		out = append(out, Loss{What: "Other track point details", Count: n, Outcome: "lost",
			Detail: "Track point elements other than the position, elevation and time (for example satellites, accuracy, a name or a symbol) are dropped.", Items: firstN(counted(g.ptOther), 10)})
	}
	if n := sum(g.rteDetail); n > 0 && target != "csv" {
		out = append(out, Loss{What: "Route point details", Count: n, Outcome: "lost",
			Detail: "A route becomes one line, so names, times and symbols on its points are dropped.", Items: firstN(counted(g.rteDetail), 10)})
	}
	if g.partial > 0 && target != "csv" {
		out = append(out, Loss{What: "Times in partly timed segments", Count: g.partial, Outcome: "lost",
			Detail: "In these track segments only some points have a time. A track needs a time for every point, so the segment is written as a plain line without times."})
	}
	if g.meta {
		out = append(out, Loss{What: "Metadata", Count: 1, Outcome: "lost", Detail: "The file's <metadata> (author, copyright, links, time, bounds) is dropped.", Items: uniqueSorted(g.metaNames)})
	}
	ext := g.featExt
	if g.ext {
		ext++
	}
	if ext > 0 {
		out = append(out, Loss{What: "Extensions on the file, waypoints, routes, tracks and segments", Count: ext, Outcome: "lost", Detail: "<extensions> outside track points, such as a track's color in Garmin's extension, are dropped."})
	}
	if n := sum(g.featOther); n > 0 {
		out = append(out, Loss{What: "Other waypoint, route and track details", Count: n, Outcome: "lost",
			Detail: "Elements such as magnetic variation, fix type, satellites and accuracy, and GPX 1.0's url and urlname, are dropped.", Items: firstN(counted(g.featOther), 10)})
	}
	if g.unknown > 0 {
		out = append(out, Loss{What: "Elements outside GPX", Count: g.unknown, Outcome: "lost", Detail: "Elements under the root that aren't part of GPX are dropped."})
	}
	fields := 0
	for _, pm := range d.Placemarks() {
		if target == "csv" && (pm.Geometry == nil || pm.Geometry.Type != "Point") {
			continue // only waypoints become rows
		}
		fields += len(pm.Fields)
	}
	if fields > 0 {
		where := "properties"
		if target == "kml" || target == "kmz" {
			where = "ExtendedData values"
		}
		if target == "csv" {
			where = "columns"
		}
		out = append(out, Loss{What: "Comments, sources, symbols, types and links", Count: fields, Outcome: "kept", Detail: "These GPX elements are written as " + where + " with the same names."})
	}
	if g.segmented > 0 && target != "csv" {
		detail := "Each segment becomes one line of a MultiLineString, with its times in coordTimes."
		if target == "kml" || target == "kmz" {
			detail = "Each segment becomes one gx:Track inside a MultiGeometry, with its times."
		}
		out = append(out, Loss{What: "Tracks with several segments", Count: g.segmented, Outcome: "kept", Detail: detail})
	}
	return out
}

func targetName(t string) string {
	switch t {
	case "geojson":
		return "GeoJSON"
	case "csv":
		return "CSV"
	}
	return "KML"
}

// lossToGPX explains what writing a KML, KMZ or GeoJSON file as GPX leaves out.
func lossToGPX(d *Doc) []Loss {
	out := []Loss{}
	polys, fields, styled, folders, spans, lineStamps := 0, 0, 0, 0, 0, 0
	for _, pm := range d.Placemarks() {
		if pm.Geometry != nil {
			for _, l := range pm.Geometry.leaves() {
				if l.Type == "Polygon" || l.Type == "Model" {
					polys++
				}
			}
		}
		if pm.Props != nil {
			for _, m := range pm.Props {
				if !mappedProps[m.Key] && string(m.Value) != "null" {
					fields++
				}
			}
			for _, k := range []string{"stroke", "fill", "marker-color"} {
				if _, ok := getProp(pm.Props, k); ok {
					styled++
					break
				}
			}
			if b, _ := propStr(pm, "begin"); b != "" {
				spans++
			} else if e, _ := propStr(pm, "end"); e != "" {
				spans++
			}
			if fp := folderProp(pm); fp != "" {
				folders++
			}
		} else {
			fields += len(pm.Fields)
			if pm.StyleURL != "" || pm.InlineStyle != nil {
				styled++
			}
			if pm.Begin != "" || pm.End != "" {
				spans++
			}
			if len(pm.FolderPath) > 0 {
				folders++
			}
		}
		_, _, ts := gpxText(pm)
		if ts != "" && pm.Geometry != nil && pm.Geometry.Type != "Point" {
			lineStamps++
		}
	}
	if polys > 0 {
		out = append(out, Loss{What: "Polygons and 3D models", Count: polys, Outcome: "lost", Detail: "GPX holds points, routes and tracks only. Areas and models are left out."})
	}
	if fields > 0 {
		out = append(out, Loss{What: "Attribute values", Count: fields, Outcome: "lost", Detail: "ExtendedData and GeoJSON properties have no general place in GPX and are dropped. Names and descriptions are kept."})
	}
	if styled > 0 {
		out = append(out, Loss{What: "Styles", Count: styled, Outcome: "lost", Detail: "GPX has no colors, widths or icons."})
	}
	if folders > 0 {
		out = append(out, Loss{What: "Folders", Count: folders, Outcome: "lost", Detail: "GPX has no folders. Every waypoint and track is written at the top level, in document order."})
	}
	if spans > 0 {
		out = append(out, Loss{What: "Time spans", Count: spans, Outcome: "lost", Detail: "GPX has one time per point. TimeSpan begin and end values are dropped."})
	}
	if lineStamps > 0 {
		out = append(out, Loss{What: "Timestamps on lines", Count: lineStamps, Outcome: "lost", Detail: "A line's single TimeStamp has no place on a GPX track, which takes a time per point."})
	}
	if n := len(d.NetworkLinks) + len(d.Overlays); n > 0 {
		out = append(out, Loss{What: "Network links and overlays", Count: n, Outcome: "lost", Detail: "GPX can't hold them."})
	}
	// GPX lists every waypoint before any track.
	moved, lineSeen := 0, false
	for _, pm := range d.Placemarks() {
		points, lines := gpxShapes(pm)
		if len(points) > 0 && lineSeen {
			moved++
		}
		if len(lines) > 0 {
			lineSeen = true
		}
	}
	if moved > 0 {
		out = append(out, Loss{What: "Order", Count: moved, Outcome: "changed", Detail: "GPX lists every waypoint before any track, so points that came after a line move up."})
	}
	return out
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
