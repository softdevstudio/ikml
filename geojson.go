package ikml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var geomTypes = map[string]bool{
	"Point": true, "MultiPoint": true, "LineString": true, "MultiLineString": true,
	"Polygon": true, "MultiPolygon": true, "GeometryCollection": true,
}

// members decodes a JSON object into its members in source order.
func members(raw json.RawMessage) ([]Prop, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return nil, false
	}
	var out []Prop
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, false
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, Prop{Key: k, Value: v})
	}
	return out, true
}

func getProp(ps []Prop, key string) (json.RawMessage, bool) {
	for _, p := range ps {
		if p.Key == key {
			return p.Value, true
		}
	}
	return nil, false
}

func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func lineAt(data []byte, off int64) int {
	if off > int64(len(data)) {
		off = int64(len(data))
	}
	if off < 0 {
		off = 0
	}
	return bytes.Count(data[:off], []byte("\n")) + 1
}

type gp struct {
	d    *Doc
	data []byte
}

func parseGeoJSON(d *Doc, data []byte) {
	p := &gp{d: d, data: data}
	d.Root = &Folder{Kind: "FeatureCollection", Path: ""}
	var top json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		var se *json.SyntaxError
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &se):
			line := lineAt(data, se.Offset)
			d.issues.add("json-invalid", Loc{Line: line}, 0, "The JSON is broken at line %d: %s.", line, se.Error())
		case errors.As(err, &te):
			d.issues.add("json-invalid", Loc{Line: lineAt(data, te.Offset)}, 0, "The JSON could not be read: %s.", err.Error())
		default:
			d.issues.add("json-invalid", Loc{}, 0, "The JSON could not be read: %s.", err.Error())
		}
		d.fatal = true
		return
	}
	if top[0] == '[' {
		d.issues.add("geojson-structure", Loc{}, 0, "The file is a JSON array. GeoJSON must be a single object (usually a FeatureCollection).")
		d.fatal = true
		return
	}
	ps, ok := members(top)
	if !ok {
		d.issues.add("geojson-structure", Loc{}, 0, "The top level is not a JSON object.")
		d.fatal = true
		return
	}
	typ := p.typeOf(ps)
	d.TopType = typ
	switch {
	case typ == "FeatureCollection":
		for _, m := range ps {
			switch m.Key {
			case "type":
			case "features":
				var feats []json.RawMessage
				if json.Unmarshal(m.Value, &feats) != nil {
					d.issues.add("geojson-structure", Loc{}, 0, "\"features\" is not an array.")
					continue
				}
				for i, f := range feats {
					p.feature(f, i+1)
				}
			case "crs":
				p.crs(m.Value)
				d.TopForeign = append(d.TopForeign, m)
			case "bbox":
				d.TopBBox = true
				d.TopForeign = append(d.TopForeign, m)
			default:
				d.TopForeign = append(d.TopForeign, m)
				p.foreignMember(m.Key, "")
			}
		}
		if _, ok := getProp(ps, "features"); !ok {
			d.issues.add("geojson-structure", Loc{}, 0, "The FeatureCollection has no \"features\" member.")
		}
	case typ == "Feature":
		p.feature(top, 1)
	case geomTypes[typ]:
		pm := &Placemark{Path: "Geometry"}
		pm.Geometry = p.geometry(top, "Geometry", 0)
		d.Root.Items = append(d.Root.Items, Item{Placemark: pm})
	case typ == "":
		d.issues.add("geojson-structure", Loc{}, 0, "The JSON object has no \"type\" member, so it is not GeoJSON.")
		d.fatal = true
	default:
		d.issues.add("geojson-structure", Loc{}, 0, "Unknown GeoJSON type %q.", typ)
		d.fatal = true
	}
}

func (p *gp) typeOf(ps []Prop) string {
	raw, ok := getProp(ps, "type")
	if !ok {
		return ""
	}
	s, _ := jsonString(raw)
	return s
}

func (p *gp) foreignMember(key, loc string) {
	p.d.ForeignMembers++
	if _, seen := p.d.foreign[key]; !seen {
		p.d.foreign[key] = 0
		where := "at the top level"
		if loc != "" {
			where = "in " + loc
		}
		p.d.issues.addKey("foreign-members", key, Loc{Path: loc}, 1, "The member %q %s is not part of GeoJSON.", key, where)
	} else {
		p.d.issues.addKey("foreign-members", key, Loc{Path: loc}, 1, "")
	}
	p.d.foreign[key]++
}

func (p *gp) crs(raw json.RawMessage) {
	p.d.HasCRS = true
	name := ""
	var c struct {
		Properties struct {
			Name string `json:"name"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &c) == nil {
		name = c.Properties.Name
	}
	p.d.CRSName = name
	if isWGS84(name) {
		p.d.issues.add("geojson-crs", Loc{}, 0, "The file has a \"crs\" member (%s). RFC 7946 removed it; the coordinates are already WGS 84, so it can simply be dropped.", quoteOr(name, "no name"))
	} else {
		p.d.issues.add("geojson-crs", Loc{}, 0, "The file has a \"crs\" member (%s). RFC 7946 removed it and most tools will treat the numbers as WGS 84 degrees, which may put the data in the wrong place.", quoteOr(name, "no name"))
	}
}

func isWGS84(name string) bool {
	n := strings.ToUpper(name)
	return n == "" || strings.HasSuffix(n, "CRS84") || strings.HasSuffix(n, "EPSG::4326") || strings.HasSuffix(n, "EPSG:4326") || n == "EPSG:4326"
}

func (p *gp) feature(raw json.RawMessage, n int) {
	d := p.d
	pm := &Placemark{}
	label := fmt.Sprintf("Feature #%d", n)
	ps, ok := members(raw)
	if !ok {
		d.issues.add("geojson-structure", Loc{Path: label}, 0, "This entry in \"features\" is not a JSON object.")
		return
	}
	if t := p.typeOf(ps); t != "Feature" {
		d.issues.add("geojson-structure", Loc{Path: label}, 0, "This entry in \"features\" has type %q, not \"Feature\".", t)
	}
	if props, ok := getProp(ps, "properties"); ok {
		if pp, ok := members(props); ok {
			pm.Props = pp
			for _, m := range pp {
				f := Field{Name: m.Key, Source: "property", Raw: m.Value}
				if s, ok := jsonString(m.Value); ok {
					f.Value = s
				} else {
					f.Value = string(m.Value)
					f.Type = jsonKind(m.Value)
				}
				switch m.Key {
				case "name":
					if s, ok := jsonString(m.Value); ok {
						pm.Name = s
					}
				case "description":
					if s, ok := jsonString(m.Value); ok {
						pm.Description = s
					}
				}
				pm.Fields = append(pm.Fields, f)
			}
		} else if string(props) != "null" {
			d.issues.add("geojson-structure", Loc{Path: label}, 0, "\"properties\" must be an object or null.")
		}
	} else {
		d.issues.add("geojson-structure", Loc{Path: label}, 0, "The Feature has no \"properties\" member. GeoJSON requires it, even if it is null.")
	}
	if pm.Name != "" {
		label = fmt.Sprintf("Feature #%d '%s'", n, pm.Name)
	}
	pm.Path = label
	for _, m := range ps {
		switch m.Key {
		case "type", "properties", "geometry":
		case "id":
			pm.RawID = m.Value
			if s, ok := jsonString(m.Value); ok {
				pm.ID = s
			} else {
				pm.ID = string(m.Value)
			}
		case "bbox":
			pm.BBox = true
			pm.Foreign = append(pm.Foreign, m)
		default:
			pm.Foreign = append(pm.Foreign, m)
			p.foreignMember(m.Key, label)
		}
	}
	if g, ok := getProp(ps, "geometry"); ok {
		if string(g) != "null" {
			pm.Geometry = p.geometry(g, label, 0)
		}
	} else {
		d.issues.add("geojson-structure", Loc{Path: label}, 0, "The Feature has no \"geometry\" member. GeoJSON requires it, even if it is null.")
	}
	if pm.ID != "" {
		if _, dup := d.ids[pm.ID]; dup {
			d.issues.add("duplicate-ids", Loc{Path: label}, 0, "The id %s is used more than once (first at %s).", pm.ID, d.idLoc[pm.ID])
		} else {
			d.ids[pm.ID] = 1
			d.idLoc[pm.ID] = label
		}
	}
	d.Root.Items = append(d.Root.Items, Item{Placemark: pm})
}

func jsonKind(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	}
	return "number"
}

func (p *gp) geometry(raw json.RawMessage, loc string, depth int) *Geometry {
	d := p.d
	ps, ok := members(raw)
	if !ok {
		d.issues.add("geojson-structure", Loc{Path: loc}, 0, "The geometry is not a JSON object.")
		return nil
	}
	typ := p.typeOf(ps)
	if !geomTypes[typ] {
		d.issues.add("geojson-structure", Loc{Path: loc}, 0, "Unknown geometry type %q.", typ)
		return nil
	}
	g := &Geometry{Type: typ, slot: -1}
	for _, m := range ps {
		switch m.Key {
		case "type", "coordinates", "geometries":
		case "bbox":
			g.Foreign = append(g.Foreign, m)
		default:
			g.Foreign = append(g.Foreign, m)
			p.foreignMember(m.Key, loc)
		}
	}
	here := loc + " > " + typ
	if typ == "GeometryCollection" {
		if depth > 0 {
			d.issues.add("geojson-nested-collection", Loc{Path: loc}, 0, "A GeometryCollection is nested inside another GeometryCollection. RFC 7946 advises against this and some tools reject it.")
		}
		raws, _ := getProp(ps, "geometries")
		var parts []json.RawMessage
		if json.Unmarshal(raws, &parts) != nil {
			d.issues.add("geojson-structure", Loc{Path: loc}, 0, "The GeometryCollection has no \"geometries\" array.")
			return g
		}
		if depth > 64 {
			d.issues.add("geojson-structure", Loc{Path: loc}, 0, "Geometry collections are nested more than 64 levels deep.")
			return g
		}
		for _, part := range parts {
			if pg := p.geometry(part, here, depth+1); pg != nil {
				g.Parts = append(g.Parts, pg)
			}
		}
		return g
	}
	craw, ok := getProp(ps, "coordinates")
	if !ok {
		d.issues.add("geojson-structure", Loc{Path: loc}, 0, "The %s has no \"coordinates\" member.", typ)
		return g
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(craw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return g
	}
	bad := 0
	pos := func(x any) (Coord, bool) {
		arr, ok := x.([]any)
		if !ok || len(arr) < 2 {
			bad++
			return Coord{}, false
		}
		var vals [3]float64
		for i := 0; i < len(arr) && i < 3; i++ {
			num, ok := arr[i].(json.Number)
			if !ok {
				bad++
				return Coord{}, false
			}
			f, err := strconv.ParseFloat(string(num), 64)
			if err != nil {
				bad++
				return Coord{}, false
			}
			vals[i] = f
		}
		if len(arr) > 3 {
			d.ExtraValues++
		}
		return Coord{Lon: vals[0], Lat: vals[1], Alt: vals[2], Has3: len(arr) >= 3}, true
	}
	list := func(x any) []Coord {
		arr, ok := x.([]any)
		if !ok {
			bad++
			return nil
		}
		var out []Coord
		for _, e := range arr {
			if c, ok := pos(e); ok {
				out = append(out, c)
			}
		}
		return out
	}
	rings := func(x any) [][]Coord {
		arr, ok := x.([]any)
		if !ok {
			bad++
			return nil
		}
		var out [][]Coord
		for _, e := range arr {
			out = append(out, list(e))
		}
		return out
	}
	arrOf := func(x any) []any {
		arr, ok := x.([]any)
		if !ok {
			bad++
		}
		return arr
	}
	switch typ {
	case "Point":
		if c, ok := pos(v); ok {
			g.Coords = []Coord{c}
		}
	case "LineString":
		g.Coords = list(v)
	case "Polygon":
		g.Rings = rings(v)
		g.hasOuter = len(g.Rings) > 0
	case "MultiPoint":
		for _, e := range arrOf(v) {
			if c, ok := pos(e); ok {
				g.Parts = append(g.Parts, &Geometry{Type: "Point", Coords: []Coord{c}, slot: -1})
			}
		}
	case "MultiLineString":
		for _, e := range arrOf(v) {
			g.Parts = append(g.Parts, &Geometry{Type: "LineString", Coords: list(e), slot: -1})
		}
	case "MultiPolygon":
		for _, e := range arrOf(v) {
			r := rings(e)
			g.Parts = append(g.Parts, &Geometry{Type: "Polygon", Rings: r, hasOuter: len(r) > 0, slot: -1})
		}
	}
	if bad > 0 {
		g.badTuples = bad
		d.issues.add("coord-parse", Loc{Path: loc}, bad, "The %s has %s. A position must be an array of two or three numbers, like [34.78, 32.08].", typ, count(bad, "position that is not an array of numbers", "positions that are not arrays of numbers"))
	}
	return g
}
