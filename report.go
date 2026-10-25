package ikml

import (
	"math"
	"sort"
	"strings"
)

// Report is what Inspect returns: structure, counts and issues.
type Report struct {
	File      string `json:"file"`
	Format    Format `json:"format"`
	Size      int    `json:"size"`
	Namespace string `json:"namespace,omitempty"`
	MainPath  string `json:"mainDocument,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Readable  bool   `json:"readable"`

	Stats  Stats       `json:"stats"`
	Tree   []TreeNode  `json:"tree"`
	Fields []FieldStat `json:"fields,omitempty"`
	Styles []StyleStat `json:"styles,omitempty"`

	Assets       []Asset          `json:"assets,omitempty"`
	NetworkLinks []NetworkLink    `json:"networkLinks,omitempty"`
	Overlays     []Overlay        `json:"overlays,omitempty"`
	Unknown      []UnknownElement `json:"unknownElements,omitempty"`

	Issues   []Issue `json:"issues"`
	Errors   int     `json:"errors"`
	Warnings int     `json:"warnings"`
	Infos    int     `json:"infos"`
}

// Stats are whole-file counts.
type Stats struct {
	Folders      int            `json:"folders"`
	Placemarks   int            `json:"placemarks"`
	Empty        int            `json:"emptyPlacemarks"`
	Geometries   map[string]int `json:"geometries"`
	Coordinates  int            `json:"coordinates"`
	Styles       int            `json:"styles"`
	StyleMaps    int            `json:"styleMaps"`
	Schemas      int            `json:"schemas"`
	NetworkLinks int            `json:"networkLinks"`
	Overlays     int            `json:"overlays"`
	Models       int            `json:"models"`
	Views        int            `json:"views"`
	Tracks       int            `json:"tracks"`
	Timestamped  int            `json:"timestamped"`
	Assets       int            `json:"assets"`
	AssetBytes   int64          `json:"assetBytes"`
	BBox         []float64      `json:"bbox,omitempty"` // west, south, east, north
}

// TreeNode is one folder in the structure tree, with what it holds
// directly (not counting sub-folders).
type TreeNode struct {
	Depth      int            `json:"depth"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Placemarks int            `json:"placemarks"`
	Geometries map[string]int `json:"geometries,omitempty"`
	Other      int            `json:"other,omitempty"`
}

// FieldStat is one attribute name and how many placemarks have it.
type FieldStat struct {
	Name   string `json:"name"`
	Count  int    `json:"count"`
	Type   string `json:"type,omitempty"`
	Source string `json:"source"`
}

// StyleStat is one shared style and how many placemarks use it.
type StyleStat struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // Style or StyleMap
	Uses  int    `json:"uses"`
	Parts string `json:"parts"`
}

// Readable reports whether the whole file could be read. It is false
// after xml-invalid, json-invalid, a broken or oversized KMZ, and similar.
func (d *Doc) Readable() bool { return !d.fatal }

// Inspect describes the document: structure tree, statistics, attribute
// fields, styles, assets, and every issue found.
func Inspect(d *Doc) Report {
	r := Report{
		File: d.Filename, Format: d.Format, Size: d.Size, Namespace: d.Namespace,
		MainPath: d.MainPath, Encoding: d.encoding, Readable: !d.fatal,
		Assets: d.Assets, NetworkLinks: d.NetworkLinks, Overlays: d.Overlays, Unknown: d.Unknown,
	}
	s := &r.Stats
	s.Geometries = map[string]int{}
	s.Styles, s.StyleMaps, s.Schemas = len(d.Styles), len(d.StyleMaps), len(d.Schemas)
	s.NetworkLinks, s.Overlays, s.Models, s.Views = len(d.NetworkLinks), len(d.Overlays), d.Models, d.Views
	for _, a := range d.Assets {
		s.Assets++
		s.AssetBytes += a.Size
	}
	bb := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	fieldIdx := map[string]int{}
	uses := map[string]int{}
	var rec func(f *Folder, depth int)
	rec = func(f *Folder, depth int) {
		node := TreeNode{Depth: depth, Kind: f.Kind, Name: f.Name, Other: f.other}
		ni := len(r.Tree)
		r.Tree = append(r.Tree, node)
		for _, it := range f.Items {
			if it.Folder != nil {
				s.Folders++
				rec(it.Folder, depth+1)
				continue
			}
			pm := it.Placemark
			s.Placemarks++
			r.Tree[ni].Placemarks++
			if pm.TimeStamp != "" || pm.Begin != "" || pm.End != "" {
				s.Timestamped++
			}
			if pm.StyleURL != "" {
				if id, ok := localStyleID(pm.StyleURL); ok {
					uses[id]++
				}
			}
			for _, fl := range pm.Fields {
				key := fl.Source + "\x00" + fl.Name
				if i, ok := fieldIdx[key]; ok {
					r.Fields[i].Count++
				} else {
					fieldIdx[key] = len(r.Fields)
					typ := fl.Type
					if fl.Source == "SimpleData" {
						typ = schemaType(d, fl)
					}
					r.Fields = append(r.Fields, FieldStat{Name: fl.Name, Count: 1, Type: typ, Source: fl.Source})
				}
			}
			if pm.Geometry == nil {
				s.Empty++
				continue
			}
			for _, l := range pm.Geometry.leaves() {
				s.Geometries[l.Type]++
				if r.Tree[ni].Geometries == nil {
					r.Tree[ni].Geometries = map[string]int{}
				}
				r.Tree[ni].Geometries[l.Type]++
				if l.Type == "Track" {
					s.Tracks++
				}
				for _, list := range l.coordLists() {
					s.Coordinates += len(list)
					for _, c := range list {
						if math.Abs(c.Lon) > 180 || math.Abs(c.Lat) > 90 {
							continue
						}
						bb[0], bb[1] = math.Min(bb[0], c.Lon), math.Min(bb[1], c.Lat)
						bb[2], bb[3] = math.Max(bb[2], c.Lon), math.Max(bb[3], c.Lat)
					}
				}
			}
			if pm.Geometry.Type == "MultiGeometry" || pm.Geometry.Type == "GeometryCollection" || strings.HasPrefix(pm.Geometry.Type, "Multi") {
				s.Geometries[pm.Geometry.Type]++
			}
		}
	}
	if d.Root != nil {
		rec(d.Root, 0)
	}
	if !math.IsInf(bb[0], 1) {
		s.BBox = bb[:]
	}
	// Style map uses also count for the styles they point to.
	for _, id := range d.StyleOrder {
		if sm, ok := d.StyleMaps[id]; ok {
			st := StyleStat{ID: id, Kind: "StyleMap", Uses: uses[id]}
			st.Parts = "normal " + quoteOr(sm.Normal, "(inline)") + ", highlight " + quoteOr(sm.Highlight, "(inline)")
			r.Styles = append(r.Styles, st)
		} else if s, ok := d.Styles[id]; ok {
			r.Styles = append(r.Styles, StyleStat{ID: id, Kind: "Style", Uses: uses[id], Parts: styleParts(s)})
		}
	}
	r.Issues = finishIssues(d.issues.list)
	for _, is := range r.Issues {
		if is.Location == "" && is.Line == 0 && strings.HasPrefix(is.Message, "The same problem appears") {
			// overflow summaries count toward the totals below as well
		}
		switch is.Severity {
		case SevError:
			r.Errors++
		case SevWarning:
			r.Warnings++
		default:
			r.Infos++
		}
	}
	if r.Issues == nil {
		r.Issues = []Issue{}
	}
	return r
}

// Issues returns every issue found in the document, worst first.
func (d *Doc) Issues() []Issue { return finishIssues(d.issues.list) }

// HasErrors reports whether any issue has error severity.
func (d *Doc) HasErrors() bool {
	for _, is := range d.issues.list {
		if is.Severity == SevError {
			return true
		}
	}
	return false
}

func schemaType(d *Doc, f Field) string {
	id := strings.TrimPrefix(f.schema, "#")
	sc := d.Schemas[id]
	if sc == nil {
		for _, s := range d.Schemas {
			if s.Name == id {
				sc = s
			}
		}
	}
	if sc == nil {
		return ""
	}
	for _, sf := range sc.Fields {
		if sf.Name == f.Name {
			return sf.Type
		}
	}
	return ""
}

func styleParts(s *Style) string {
	var p []string
	if s.HasIcon {
		x := "icon"
		if s.IconHref != "" {
			x += " " + s.IconHref
		}
		p = append(p, x)
	}
	if s.HasLine {
		p = append(p, "line")
	}
	if s.HasPoly {
		p = append(p, "polygon")
	}
	if s.HasLabel {
		p = append(p, "label")
	}
	if s.HasBalloon {
		p = append(p, "balloon")
	}
	if s.HasList {
		p = append(p, "list")
	}
	if len(p) == 0 {
		return "empty"
	}
	return strings.Join(p, ", ")
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
