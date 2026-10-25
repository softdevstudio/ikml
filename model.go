// Package ikml opens KML, KMZ and GeoJSON files, reports what is inside
// them, flags what is broken, explains what a conversion would lose, and
// writes cleaned copies.
//
// The usual flow is Open, then Inspect for a report, LossReport before a
// conversion, Clean for safe fixes, and one of the Write functions.
package ikml

import (
	"encoding/json"
	"fmt"
)

// Version is the library version. It must match the VERSION file and the
// Version constant in cmd/ikml (scripts/check-version.sh checks this).
const Version = "0.1.2"

// Format names a file format iKML can read or write.
type Format string

// Supported formats.
const (
	FormatKML     Format = "kml"
	FormatKMZ     Format = "kmz"
	FormatGeoJSON Format = "geojson"
)

// Doc is the unified model of an opened file. Fields are exported so
// library users can walk the structure; treat them as read-only and use
// Clean to get a modified copy.
type Doc struct {
	Format   Format `json:"format"`
	Filename string `json:"filename"`
	Size     int    `json:"size"`

	// Namespace is the KML namespace URI on the root element ("" if none).
	Namespace string `json:"namespace,omitempty"`
	// MainPath is the path of the main KML document inside a KMZ.
	MainPath string `json:"mainPath,omitempty"`

	Root *Folder `json:"root"`

	Styles    map[string]*Style    `json:"-"`
	StyleMaps map[string]*StyleMap `json:"-"`
	Schemas   map[string]*Schema   `json:"-"`
	// StyleOrder lists style and style map ids in document order.
	StyleOrder []string `json:"-"`

	Assets       []Asset          `json:"assets,omitempty"`
	Refs         []Ref            `json:"refs,omitempty"`
	NetworkLinks []NetworkLink    `json:"networkLinks,omitempty"`
	Overlays     []Overlay        `json:"overlays,omitempty"`
	Unknown      []UnknownElement `json:"unknown,omitempty"`
	Models       int              `json:"models"`
	Views        int              `json:"views"`

	// GeoJSON specifics.
	TopType        string `json:"-"`
	CRSName        string `json:"crs,omitempty"`
	HasCRS         bool   `json:"hasCRS,omitempty"`
	TopForeign     []Prop `json:"-"`
	ForeignMembers int    `json:"foreignMembers,omitempty"`
	ExtraValues    int    `json:"extraPositionValues,omitempty"`
	TopBBox        bool   `json:"-"`

	issues   *collector
	raw      []byte // main KML bytes (UTF-8), used to splice clean output
	slots    []slot
	rootTag  [2]int // byte range of the <kml> start tag
	zipData  []byte
	ids      map[string]int
	idLoc    map[string]string
	foreign  map[string]int
	fatal    bool
	encoding string

	pairs      []pairRef
	trackCount int
	stampCount int
	arrayData  int
	assetData  map[string]*zipEntry
	edits      []edit
	dropCRS    bool
}

// Folder is a Document, Folder, or the implicit root of a GeoJSON file.
type Folder struct {
	Kind        string `json:"kind"` // Document, Folder, FeatureCollection, kml
	Name        string `json:"name,omitempty"`
	ID          string `json:"id,omitempty"`
	Description string `json:"-"`
	Path        string `json:"path"`
	Items       []Item `json:"-"`
	other       int    // NetworkLinks, overlays and similar features inside
	top         bool   // the Document directly under <kml>
}

// Item is one child of a Folder: a sub-folder or a placemark.
type Item struct {
	Folder    *Folder
	Placemark *Placemark
}

// Placemark is a KML Placemark or a GeoJSON Feature.
type Placemark struct {
	Name        string    `json:"name,omitempty"`
	ID          string    `json:"id,omitempty"`
	Description string    `json:"description,omitempty"`
	StyleURL    string    `json:"styleUrl,omitempty"`
	InlineStyle *Style    `json:"-"`
	Fields      []Field   `json:"fields,omitempty"`
	SchemaURL   string    `json:"schemaUrl,omitempty"`
	TimeStamp   string    `json:"timestamp,omitempty"`
	Begin       string    `json:"begin,omitempty"`
	End         string    `json:"end,omitempty"`
	Geometry    *Geometry `json:"geometry,omitempty"`
	Path        string    `json:"path"`
	Line        int       `json:"line,omitempty"`
	FolderPath  []string  `json:"-"`

	// GeoJSON source only.
	Props   []Prop          `json:"-"`
	Foreign []Prop          `json:"-"`
	RawID   json.RawMessage `json:"-"`
	BBox    bool            `json:"-"`

	styleRange [2]int
	styleLine  int
	views      int
}

// Field is one attribute value (KML ExtendedData or a GeoJSON property).
type Field struct {
	Name    string          `json:"name"`
	Value   string          `json:"value"`
	Type    string          `json:"type,omitempty"`
	Display string          `json:"displayName,omitempty"`
	Source  string          `json:"source"` // Data, SimpleData, property
	Raw     json.RawMessage `json:"-"`      // GeoJSON source: the JSON value
	schema  string
}

// Prop is a GeoJSON member kept in source order.
type Prop struct {
	Key   string
	Value json.RawMessage
}

// Geometry covers every geometry type iKML reads. Point, LineString,
// LinearRing and Track use Coords; Polygon uses Rings (outer first);
// Multi types and GeometryCollection use Parts.
type Geometry struct {
	Type         string      `json:"type"`
	Coords       []Coord     `json:"-"`
	Rings        [][]Coord   `json:"-"`
	Parts        []*Geometry `json:"parts,omitempty"`
	When         []string    `json:"-"`
	AltitudeMode string      `json:"altitudeMode,omitempty"`
	Extrude      bool        `json:"extrude,omitempty"`
	Tessellate   bool        `json:"tessellate,omitempty"`
	Foreign      []Prop      `json:"-"`
	NoOuter      bool        `json:"-"`

	Line int `json:"-"`

	slot       int
	ringSlots  []int
	ringBad    []int
	pointSlots []int
	dirty      bool
	badTuples  int
	spacing    bool
	hasOuter   bool
}

// Coord is one position. Has3 is true when an altitude was given.
type Coord struct {
	Lon, Lat, Alt float64
	Has3          bool
}

// Style is a KML Style. Only the parts that matter for reporting and for
// the GeoJSON simplestyle mapping are kept.
type Style struct {
	ID          string
	HasIcon     bool
	IconHref    string
	IconColor   string
	IconScale   string
	HotSpot     bool
	HasLine     bool
	LineColor   string
	LineWidth   string
	HasPoly     bool
	PolyColor   string
	PolyFill    string
	PolyOutline string
	HasLabel    bool
	HasBalloon  bool
	HasList     bool
	location    string
}

// StyleMap is a KML StyleMap with normal and highlight pairs.
type StyleMap struct {
	ID              string
	Normal          string
	Highlight       string
	NormalInline    *Style
	HighlightInline *Style
	location        string
}

// Schema is a KML Schema used by SchemaData.
type Schema struct {
	ID     string
	Name   string
	Fields []SchemaField
}

// SchemaField is one SimpleField of a Schema.
type SchemaField struct {
	Name, Type, DisplayName string
}

// Asset is a file stored inside a KMZ archive (other than the main KML).
type Asset struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Compressed int64  `json:"compressed"`
	Used       bool   `json:"used"`
}

// Ref is a reference from the document to another file or URL.
type Ref struct {
	Href     string `json:"href"`
	Kind     string `json:"kind"` // icon, overlay, network-link, model, description-image, style
	Location string `json:"location"`
	Line     int    `json:"line,omitempty"`
}

// NetworkLink is a KML NetworkLink. Its contents are not part of the file.
type NetworkLink struct {
	Name     string `json:"name,omitempty"`
	Href     string `json:"href"`
	Location string `json:"location"`
}

// Overlay is a GroundOverlay, ScreenOverlay or PhotoOverlay.
type Overlay struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	Href     string `json:"href,omitempty"`
	Location string `json:"location"`
}

// UnknownElement counts elements iKML does not interpret.
type UnknownElement struct {
	Name     string `json:"name"`
	Count    int    `json:"count"`
	Location string `json:"location"`
	Line     int    `json:"line,omitempty"`
	issue    int
	space    string
}

// Loc is where something is: a structure path and, for KML, a line.
type Loc struct {
	Path string
	Line int
}

func (l Loc) String() string {
	switch {
	case l.Path != "" && l.Line > 0:
		return fmt.Sprintf("%s, line %d", l.Path, l.Line)
	case l.Line > 0:
		return fmt.Sprintf("line %d", l.Line)
	}
	return l.Path
}

type slot struct {
	start, end int
	gxcoord    bool
}

// Placemarks returns every placemark in document order.
func (d *Doc) Placemarks() []*Placemark {
	var out []*Placemark
	d.walk(func(f *Folder) {}, func(p *Placemark) { out = append(out, p) })
	return out
}

func (d *Doc) walk(ff func(*Folder), pf func(*Placemark)) {
	if d == nil || d.Root == nil {
		return
	}
	var rec func(f *Folder)
	rec = func(f *Folder) {
		ff(f)
		for _, it := range f.Items {
			if it.Folder != nil {
				rec(it.Folder)
			} else if it.Placemark != nil {
				pf(it.Placemark)
			}
		}
	}
	rec(d.Root)
}

// leaves returns the simple geometries inside g, flattening Multi types.
func (g *Geometry) leaves() []*Geometry {
	if g == nil {
		return nil
	}
	if len(g.Parts) > 0 || isMulti(g.Type) {
		var out []*Geometry
		for _, p := range g.Parts {
			out = append(out, p.leaves()...)
		}
		return out
	}
	return []*Geometry{g}
}

func isMulti(t string) bool {
	switch t {
	case "MultiGeometry", "MultiTrack", "MultiPoint", "MultiLineString", "MultiPolygon", "GeometryCollection":
		return true
	}
	return false
}

// coordLists returns every coordinate list in g (rings separately).
func (g *Geometry) coordLists() [][]Coord {
	var out [][]Coord
	for _, l := range g.leaves() {
		switch l.Type {
		case "Polygon":
			out = append(out, l.Rings...)
		case "Model":
		default:
			out = append(out, l.Coords)
		}
	}
	return out
}

func (g *Geometry) clone() *Geometry {
	if g == nil {
		return nil
	}
	c := *g
	c.Coords = append([]Coord(nil), g.Coords...)
	if g.Rings != nil {
		c.Rings = make([][]Coord, len(g.Rings))
		for i, r := range g.Rings {
			c.Rings[i] = append([]Coord(nil), r...)
		}
	}
	if g.Parts != nil {
		c.Parts = make([]*Geometry, len(g.Parts))
		for i, p := range g.Parts {
			c.Parts[i] = p.clone()
		}
	}
	c.When = append([]string(nil), g.When...)
	c.ringSlots = append([]int(nil), g.ringSlots...)
	c.pointSlots = append([]int(nil), g.pointSlots...)
	return &c
}

// clone makes a deep copy of the parts of the document that Clean changes.
func (d *Doc) clone() *Doc {
	c := *d
	var rec func(f *Folder) *Folder
	rec = func(f *Folder) *Folder {
		nf := *f
		nf.Items = make([]Item, len(f.Items))
		for i, it := range f.Items {
			if it.Folder != nil {
				nf.Items[i].Folder = rec(it.Folder)
			} else if it.Placemark != nil {
				p := *it.Placemark
				p.Geometry = it.Placemark.Geometry.clone()
				nf.Items[i].Placemark = &p
			}
		}
		return &nf
	}
	if d.Root != nil {
		c.Root = rec(d.Root)
	}
	c.Assets = append([]Asset(nil), d.Assets...)
	return &c
}
