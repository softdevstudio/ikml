package ikml

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// csvInfo keeps a CSV file's own header and layout, so CSV output from a
// CSV source keeps its columns.
type csvInfo struct {
	header          []string
	latCol, lonCol  int
	delim           rune
	badRows, ragged int
	extra           int    // values past the header's columns in ragged rows
	raw             []byte // the file as read; CSV output is this file, with fixed rows rewritten
}

// csvRow is the source row behind one placemark of a CSV file.
type csvRow struct {
	values     []string // as read, before padding a ragged row
	orig       Coord
	start, end int    // the record's bytes in csvInfo.raw, line ending included
	latSpan    [2]int // the latitude cell's bytes in csvInfo.raw, quotes included
	lonSpan    [2]int
	comma      bool // the latitude was written with a decimal comma
}

var (
	latNames  = map[string]bool{"lat": true, "latitude": true, "y": true}
	lonNames  = map[string]bool{"lon": true, "lng": true, "long": true, "longitude": true, "x": true}
	nameCols  = map[string]bool{"name": true, "title": true}
	descCols  = map[string]bool{"description": true, "desc": true}
	timeCols  = map[string]bool{"time": true, "timestamp": true}
	eleCols   = map[string]bool{"ele": true, "elevation": true, "alt": true, "altitude": true}
	csvDelims = []rune{',', ';', '\t'}
)

func normHeader(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// csvHeader splits the first line with the delimiter that gives the most
// columns and finds the latitude and longitude columns. ok is false when
// the line doesn't look like a header row at all.
func csvHeader(data []byte) (header []string, delim rune, lat, lon int, ok bool) {
	first := data
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = bytes.TrimSuffix(first, []byte("\r"))
	// The delimiter that finds a latitude and a longitude column wins;
	// among equals, the one giving the most columns.
	best, bestCoords := 0, false
	lat, lon = -1, -1
	for _, d := range csvDelims {
		r := csv.NewReader(bytes.NewReader(first))
		r.Comma = d
		r.LazyQuotes = true
		rec, err := r.Read()
		if err != nil || len(rec) < 2 {
			continue
		}
		la, lo := coordCols(rec)
		coords := la >= 0 && lo >= 0
		if (coords && !bestCoords) || (coords == bestCoords && len(rec) > best) {
			best, bestCoords, header, delim, lat, lon = len(rec), coords, rec, d, la, lo
		}
	}
	if best < 2 {
		return nil, 0, -1, -1, false
	}
	return header, delim, lat, lon, true
}

func coordCols(header []string) (lat, lon int) {
	lat, lon = -1, -1
	for i, h := range header {
		n := normHeader(h)
		if latNames[n] && lat < 0 {
			lat = i
		}
		if lonNames[n] && lon < 0 {
			lon = i
		}
	}
	return lat, lon
}

// looksLikeCSV reports whether data, which is neither XML nor JSON, should
// be read as CSV: a header with coordinate columns, or any delimited header
// in a file named .csv.
func looksLikeCSV(name string, data []byte) bool {
	_, _, lat, lon, ok := csvHeader(data)
	if !ok {
		return false
	}
	return (lat >= 0 && lon >= 0) || strings.HasSuffix(strings.ToLower(name), ".csv")
}

func csvNumber(s string, decimalComma bool) (float64, bool) {
	s = strings.TrimSpace(s)
	if decimalComma {
		s = strings.Replace(s, ",", ".", 1)
	}
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func openCSV(d *Doc, data []byte) {
	d.Root = &Folder{Kind: "csv", Path: "/"}
	raw := data
	bom := 0
	if bytes.HasPrefix(data, []byte("\xEF\xBB\xBF")) {
		bom = 3
		data = data[3:]
	}
	header, delim, lat, lon, _ := csvHeader(data)
	info := &csvInfo{header: header, delim: delim, latCol: lat, lonCol: lon, raw: raw}
	// A decimal comma is possible only when the comma isn't the delimiter.
	commaOK := ',' != delim
	d.csv = info
	if lat < 0 || lon < 0 {
		hint := ""
		if bytes.IndexByte(data, '\r') >= 0 && bytes.IndexByte(data, '\n') < 0 {
			hint = " The file ends its lines with CR only (old Mac style); save it with LF or CRLF line endings."
		}
		d.issues.add("csv-no-coordinates", Loc{Line: 1}, 0, "The header row (%s) has no latitude and longitude columns. Name them lat and lon (or latitude and longitude) so iKML can place the rows.%s", strings.Join(header, ", "), hint)
		d.fatal = true
		return
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delim
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	if _, err := r.Read(); err != nil {
		d.issues.add("csv-no-coordinates", Loc{Line: 1}, 0, "The header row can't be read: %v.", err)
		d.fatal = true
		return
	}
	roles := make([]string, len(header))
	for i, h := range header {
		n := normHeader(h)
		switch {
		case i == lat || i == lon:
			roles[i] = "coord"
		case nameCols[n]:
			roles[i] = "name"
		case descCols[n]:
			roles[i] = "description"
		case timeCols[n]:
			roles[i] = "time"
		case eleCols[n]:
			roles[i] = "ele"
		}
	}
	// Only the first column of each role is used; repeats stay fields.
	seen := map[string]bool{}
	for i, r := range roles {
		if r == "" || r == "coord" {
			continue
		}
		if seen[r] {
			roles[i] = ""
		}
		seen[r] = true
	}
	// Byte offset of each line, so field positions become offsets.
	lineStart := []int{0}
	for i, b := range data {
		if b == '\n' {
			lineStart = append(lineStart, i+1)
		}
	}
	offset := func(line, col int) int {
		if line < 1 || line > len(lineStart) {
			return -1
		}
		return bom + lineStart[line-1] + col - 1
	}
	rowNo := 0
	for {
		start := bom + int(r.InputOffset())
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		end := bom + int(r.InputOffset())
		line, _ := r.FieldPos(0)
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				line = pe.Line
			}
			d.issues.add("csv-bad-row", Loc{Line: line}, 0, "Row at line %d can't be read: %v. It is left out.", line, err)
			info.badRows++
			continue
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		orig := append([]string(nil), rec...)
		rowNo++
		// The byte range of field i, without its delimiter or line ending.
		span := func(i int) [2]int {
			l, c := r.FieldPos(i)
			a := offset(l, c)
			b := len(bytes.TrimRight(raw[:end], "\r\n"))
			if i+1 < len(orig) {
				l2, c2 := r.FieldPos(i + 1)
				b = offset(l2, c2) - 1
			}
			if a < start || b < a || b > end {
				return [2]int{-1, -1}
			}
			return [2]int{a, b}
		}
		if len(rec) != len(header) {
			info.ragged++
			if len(rec) > len(header) {
				info.extra += len(rec) - len(header)
			}
			d.issues.add("csv-ragged-row", Loc{Line: line}, 0, "Row at line %d has %d values; the header has %d columns.", line, len(rec), len(header))
			for len(rec) < len(header) {
				rec = append(rec, "")
			}
			rec = rec[:len(header)]
		}
		la, okLat := csvNumber(rec[lat], commaOK)
		lo, okLon := csvNumber(rec[lon], commaOK)
		if !okLat || !okLon {
			info.badRows++
			d.issues.add("csv-bad-row", Loc{Line: line}, 0, "Row at line %d has %s=%q and %s=%q, which aren't both numbers. It is left out.", line, header[lat], rec[lat], header[lon], rec[lon])
			continue
		}
		c := Coord{Lon: lo, Lat: la}
		pm := &Placemark{Path: fmt.Sprintf("/row[%d]", rowNo), Line: line}
		for i, v := range rec {
			switch roles[i] {
			case "coord":
			case "name":
				pm.Name = strings.TrimSpace(v)
			case "description":
				pm.Description = strings.TrimSpace(v)
			case "time":
				pm.TimeStamp = strings.TrimSpace(v)
			case "ele":
				if a, ok := csvNumber(v, commaOK); ok {
					c.Alt, c.Has3 = a, true
				} else if strings.TrimSpace(v) != "" {
					pm.Fields = append(pm.Fields, Field{Name: header[i], Value: v, Source: "csv"})
				}
			default:
				pm.Fields = append(pm.Fields, Field{Name: header[i], Value: v, Source: "csv"})
			}
		}
		pm.Geometry = &Geometry{Type: "Point", Coords: []Coord{c}, Line: line}
		pm.csv = &csvRow{values: orig, orig: c, start: start, end: end, latSpan: span(lat), lonSpan: span(lon),
			comma: commaOK && strings.Contains(rec[lat], ",")}
		d.Root.Items = append(d.Root.Items, Item{Placemark: pm})
	}
}

// writeCSVSource writes a CSV source as it was, byte for byte, except for
// the latitude and longitude cells that clean changed, which are replaced
// in place.
func writeCSVSource(d *Doc, w io.Writer) error {
	info := d.csv
	var edits []edit
	for _, pm := range d.Placemarks() {
		row := pm.csv
		if row == nil || pm.Geometry == nil || len(pm.Geometry.Coords) == 0 {
			continue
		}
		c := pm.Geometry.Coords[0]
		cell := func(v float64, sp [2]int) {
			if sp[0] < 0 {
				return
			}
			t := formatFloat(v)
			if row.comma {
				t = strings.Replace(t, ".", ",", 1)
			}
			if sp[1] > sp[0] && info.raw[sp[0]] == '"' {
				t = `"` + t + `"`
			}
			edits = append(edits, edit{sp[0], sp[1], t})
		}
		if c.Lat != row.orig.Lat {
			cell(c.Lat, row.latSpan)
		}
		if c.Lon != row.orig.Lon {
			cell(c.Lon, row.lonSpan)
		}
	}
	sort.Slice(edits, func(a, b int) bool { return edits[a].start < edits[b].start })
	bw := bufio.NewWriter(w)
	pos := 0
	for _, e := range edits {
		if e.start < pos {
			continue
		}
		bw.Write(info.raw[pos:e.start])
		bw.WriteString(e.text)
		pos = e.end
	}
	bw.Write(info.raw[pos:])
	return bw.Flush()
}

// WriteCSV writes the points of d as CSV, one row per point. A CSV source
// keeps its own columns; other formats get name, lat, lon, then ele, time
// and description when any point has them, then every attribute.
func WriteCSV(d *Doc, w io.Writer) error {
	if d.fatal {
		return ErrUnreadable
	}
	if d.csv != nil {
		return writeCSVSource(d, w)
	}
	cw := csv.NewWriter(w)
	type point struct {
		c              Coord
		name, desc, ts string
		fields         []Field
	}
	var pts []point
	var names []string
	seen := map[string]bool{}
	hasEle, hasTime, hasDesc := false, false, false
	for _, pm := range d.Placemarks() {
		if pm.Geometry == nil {
			continue
		}
		name, desc, ts := gpxText(pm)
		fields := csvFields(pm)
		added := false
		for _, l := range pm.Geometry.leaves() {
			if l.Type != "Point" || len(l.Coords) == 0 {
				continue
			}
			if !added {
				// Columns come only from placemarks that give a row.
				added = true
				for _, f := range fields {
					if !seen[f.Name] {
						seen[f.Name] = true
						names = append(names, f.Name)
					}
				}
			}
			p := point{c: l.Coords[0], name: name, desc: desc, ts: ts, fields: fields}
			hasEle = hasEle || p.c.Has3
			hasTime = hasTime || ts != ""
			hasDesc = hasDesc || desc != ""
			pts = append(pts, p)
		}
	}
	reserved := map[string]bool{"name": true, "lat": true, "lon": true}
	head := []string{"name", "lat", "lon"}
	if hasEle {
		head, reserved["ele"] = append(head, "ele"), true
	}
	if hasTime {
		head, reserved["time"] = append(head, "time"), true
	}
	if hasDesc {
		head, reserved["description"] = append(head, "description"), true
	}
	// An attribute named like a fixed column gets a suffix.
	cols := make([]string, len(names))
	for i, n := range names {
		c := n
		for k := 2; reserved[c]; k++ {
			c = fmt.Sprintf("%s_%d", n, k)
		}
		reserved[c] = true
		cols[i] = c
	}
	cw.Write(append(head, cols...))
	for _, p := range pts {
		row := []string{p.name, formatFloat(p.c.Lat), formatFloat(p.c.Lon)}
		if hasEle {
			e := ""
			if p.c.Has3 {
				e = formatFloat(p.c.Alt)
			}
			row = append(row, e)
		}
		if hasTime {
			row = append(row, p.ts)
		}
		if hasDesc {
			row = append(row, p.desc)
		}
		vals := map[string]string{}
		for _, f := range p.fields {
			if _, dup := vals[f.Name]; !dup {
				vals[f.Name] = f.Value
			}
		}
		for _, n := range names {
			row = append(row, vals[n])
		}
		cw.Write(row)
	}
	cw.Flush()
	return cw.Error()
}

// csvFields lists a placemark's attributes as text: GeoJSON properties
// other than the mapped ones (objects and arrays as JSON), or its fields.
func csvFields(pm *Placemark) []Field {
	if pm.Props == nil {
		return pm.Fields
	}
	var out []Field
	for _, m := range pm.Props {
		if mappedProps[m.Key] || string(m.Value) == "null" {
			continue
		}
		v, ok := jsonString(m.Value)
		if !ok {
			v = compact(m.Value)
		}
		out = append(out, Field{Name: m.Key, Value: v})
	}
	return out
}

// lossToCSV explains what writing d as CSV leaves out.
func lossToCSV(d *Doc) []Loss {
	out := []Loss{}
	if d.Format == FormatGPX {
		return lossFromGPX(d, "csv")
	}
	shapes, nested, styled, folders, spans := 0, 0, 0, 0, 0
	for _, pm := range d.Placemarks() {
		if pm.Geometry != nil {
			for _, l := range pm.Geometry.leaves() {
				if l.Type != "Point" {
					shapes++
				}
			}
		}
		if pm.Props != nil {
			for _, m := range pm.Props {
				if mappedProps[m.Key] {
					continue
				}
				if b := bytes.TrimSpace(m.Value); len(b) > 0 && (b[0] == '{' || b[0] == '[') {
					nested++
				}
			}
			for _, k := range []string{"stroke", "fill", "marker-color"} {
				if _, ok := getProp(pm.Props, k); ok {
					styled++
					break
				}
			}
			if fp := folderProp(pm); fp != "" {
				folders++
			}
			if b, _ := propStr(pm, "begin"); b != "" {
				spans++
			}
		} else {
			if pm.StyleURL != "" || pm.InlineStyle != nil {
				styled++
			}
			if len(pm.FolderPath) > 0 {
				folders++
			}
			if pm.Begin != "" || pm.End != "" {
				spans++
			}
		}
	}
	if shapes > 0 {
		out = append(out, Loss{What: "Lines, tracks and polygons", Count: shapes, Outcome: "lost", Detail: "A CSV row holds one point. Only points are written."})
	}
	if nested > 0 {
		out = append(out, Loss{What: "Nested properties", Count: nested, Outcome: "changed", Detail: "Object and array values are written as JSON text in their column."})
	}
	if styled > 0 {
		out = append(out, Loss{What: "Styles", Count: styled, Outcome: "lost", Detail: "CSV has no colors or icons."})
	}
	if folders > 0 {
		out = append(out, Loss{What: "Folders", Count: folders, Outcome: "lost", Detail: "Every row is written at the top level, in document order."})
	}
	if spans > 0 {
		out = append(out, Loss{What: "Time spans", Count: spans, Outcome: "lost", Detail: "Only a single time per point has a column. TimeSpan begin and end values are dropped."})
	}
	if n := len(d.NetworkLinks) + len(d.Overlays); n > 0 {
		out = append(out, Loss{What: "Network links and overlays", Count: n, Outcome: "lost", Detail: "CSV can't hold them."})
	}
	return out
}

// lossFromCSV explains what converting a CSV file to another format changes.
func lossFromCSV(d *Doc, target string) []Loss {
	out := []Loss{}
	if target == "csv" {
		return out
	}
	if info := d.csv; info != nil {
		if info.badRows > 0 {
			out = append(out, Loss{What: "Rows without coordinates", Count: info.badRows, Outcome: "lost", Detail: "Rows whose latitude or longitude can't be read are left out (csv-bad-row)."})
		}
		if info.extra > 0 {
			out = append(out, Loss{What: "Values past the last column", Count: info.extra, Outcome: "lost", Detail: "Ragged rows with more values than the header has columns lose the extra values (csv-ragged-row)."})
		}
	}
	fields, cols := 0, map[string]bool{}
	for _, pm := range d.Placemarks() {
		fields += len(pm.Fields)
		for _, f := range pm.Fields {
			cols[f.Name] = true
		}
	}
	if fields > 0 {
		where := "properties"
		if target == "kml" || target == "kmz" {
			where = "ExtendedData values"
		}
		if target == "gpx" {
			out = append(out, Loss{What: "Other column values", Count: fields, Outcome: "lost", Detail: fmt.Sprintf("GPX waypoints keep a name, description, time and elevation. The values of %d other %s are dropped.", len(cols), plural(len(cols), "column", "columns"))})
			return out
		}
		out = append(out, Loss{What: "Other column values", Count: fields, Outcome: "changed", Detail: fmt.Sprintf("%d other %s", len(cols), plural(len(cols), "column", "columns")) + ": every other column is written as " + where + " under its header name. CSV values are text, so numbers come out as strings."})
	}
	return out
}
