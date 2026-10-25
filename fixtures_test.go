package ikml

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/softdevstudio/ikml/internal/fixtures"
)

func load(t testing.TB, path string) *Doc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Base(path), data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return d
}

func codesOf(r Report) map[string]Severity {
	m := map[string]Severity{}
	for _, is := range r.Issues {
		m[is.Code] = is.Severity
	}
	return m
}

func sortedCodes(m map[string]Severity) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Every defect fixture must produce the listed issue codes, and nothing
// at error severity that is not listed (no false positives).
var defects = map[string][]string{
	"altitude-clamped.kml":      {"altitude-ignored", "duplicate-points", "mixed-dimensions"},
	"bad-positions.geojson":     {"coord-parse", "lon-range", "null-island"},
	"broken-style.kml":          {"style-missing"},
	"crs-clockwise.geojson":     {"geojson-crs", "winding", "ring-unclosed", "foreign-members"},
	"duplicate-ids.kml":         {"duplicate-ids", "empty-folder"},
	"invalid.geojson":           {"json-invalid"},
	"kml21-namespace.kml":       {"old-namespace"},
	"local-path.kmz":            {"local-path-ref"},
	"malformed.kml":             {"xml-invalid"},
	"nested-collection.geojson": {"geojson-nested-collection", "empty-placemark", "foreign-members"},
	"network-link.kml":          {"network-link", "http-ref", "overlay-present", "external-file-ref"},
	"no-kml.kmz":                {"kmz-no-kml"},
	"no-namespace.kml":          {"no-namespace"},
	"null-island.kml":           {"null-island", "empty-placemark"},
	"out-of-range.kml":          {"lon-range", "lat-range"},
	"overlay-missing-icon.kmz":  {"kmz-missing-asset", "unused-asset", "overlay-present"},
	"spaced-coords.kml":         {"coord-spacing"},
	"swapped-latlon.kml":        {"swapped-latlon"},
	"swapped-one-placemark.kml": {"swapped-latlon"},
	"unclosed-ring.kml":         {"ring-unclosed", "ring-too-short", "line-too-short"},
	"unparseable-coords.kml":    {"coord-parse"},
}

func TestDefectFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/bad/*")
	if len(files) != len(defects) {
		t.Errorf("testdata/bad has %d files but the table lists %d", len(files), len(defects))
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			want, ok := defects[name]
			if !ok {
				t.Fatalf("no expectations for %s", name)
			}
			r := Inspect(load(t, f))
			got := codesOf(r)
			for _, c := range want {
				if _, ok := got[c]; !ok {
					t.Errorf("missing %s; got %v", c, sortedCodes(got))
				}
			}
			wanted := map[string]bool{}
			for _, c := range want {
				wanted[c] = true
			}
			for c, sev := range got {
				if sev == SevError && !wanted[c] {
					t.Errorf("unexpected error %s", c)
				}
			}
		})
	}
}

func TestGoodFixturesHaveNoErrors(t *testing.T) {
	files, _ := filepath.Glob("testdata/good/*")
	if len(files) < 8 {
		t.Fatalf("expected at least 8 good fixtures, found %d", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			r := Inspect(load(t, f))
			if !r.Readable {
				t.Error("not readable")
			}
			for _, is := range r.Issues {
				if is.Severity == SevError {
					t.Errorf("false positive: %s %s: %s", is.Code, is.Location, is.Message)
				}
			}
			if r.Stats.Placemarks == 0 {
				t.Error("no placemarks found")
			}
		})
	}
}

// Specific facts about the good fixtures, so the report is checked for
// content and not only for the absence of errors.
func TestFixtureContents(t *testing.T) {
	type want struct {
		placemarks, folders, styles, styleMaps, coords, assets int
		geoms                                                  map[string]int
		fields                                                 []string
		notes                                                  []string
	}
	cases := map[string]want{
		"earth-pro-export.kml": {placemarks: 4, folders: 3, styles: 2, styleMaps: 1, coords: 11,
			geoms: map[string]int{"Point": 2, "LineString": 1, "Polygon": 1}, notes: []string{"unknown-element", "http-ref"}},
		"mymaps-export.kml": {placemarks: 3, folders: 2, styles: 4, styleMaps: 2, coords: 6,
			fields: []string{"contact", "capacity", "length_km"}, notes: []string{"remote-ref"}},
		"arcgis-export.kml": {placemarks: 2, folders: 1, styles: 1, coords: 15,
			geoms: map[string]int{"Polygon": 2, "MultiGeometry": 2}, fields: []string{"PARCEL_ID", "AREA_M2", "ZONE"}},
		"qgis-export.kml": {placemarks: 3, folders: 1, coords: 5, fields: []string{"species", "height_m", "planted"}},
		"gps-track.kml": {placemarks: 2, styles: 1, coords: 6, geoms: map[string]int{"Track": 1, "Point": 1},
			notes: []string{"has-tracks", "has-timestamps"}},
		"dji-mission.kmz": {placemarks: 4, folders: 1, coords: 4, assets: 1, geoms: map[string]int{"Point": 4},
			notes: []string{"unknown-element", "unused-asset"}},
		"photo-tour.kmz": {placemarks: 2, styles: 1, coords: 2, assets: 3},
		"clean.geojson": {placemarks: 4, coords: 16,
			geoms: map[string]int{"Point": 3, "LineString": 1, "Polygon": 1, "MultiPoint": 1}},
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			r := Inspect(load(t, "testdata/good/"+name))
			s := r.Stats
			check := func(what string, got, want int) {
				if got != want {
					t.Errorf("%s: got %d, want %d", what, got, want)
				}
			}
			check("placemarks", s.Placemarks, w.placemarks)
			check("folders", s.Folders, w.folders+boolInt(r.Format != FormatGeoJSON))
			check("styles", s.Styles, w.styles)
			check("styleMaps", s.StyleMaps, w.styleMaps)
			check("coordinates", s.Coordinates, w.coords)
			check("assets", s.Assets, w.assets)
			for g, n := range w.geoms {
				check("geometry "+g, s.Geometries[g], n)
			}
			have := map[string]bool{}
			for _, f := range r.Fields {
				have[f.Name] = true
			}
			for _, f := range w.fields {
				if !have[f] {
					t.Errorf("missing field %s", f)
				}
			}
			codes := codesOf(r)
			for _, n := range w.notes {
				if _, ok := codes[n]; !ok {
					t.Errorf("missing note %s", n)
				}
			}
		})
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestSchemaTypes(t *testing.T) {
	r := Inspect(load(t, "testdata/good/arcgis-export.kml"))
	types := map[string]string{}
	for _, f := range r.Fields {
		types[f.Name] = f.Type
	}
	if types["AREA_M2"] != "double" || types["ZONE"] != "int" {
		t.Errorf("schema types not resolved: %v", types)
	}
}

func TestLocationsAreSpecific(t *testing.T) {
	r := Inspect(load(t, "testdata/bad/swapped-one-placemark.kml"))
	for _, is := range r.Issues {
		if is.Code == "swapped-latlon" {
			if is.Location != "Placemark 'Typed in by hand'" || is.Line != 8 {
				t.Errorf("location %q line %d", is.Location, is.Line)
			}
			return
		}
	}
	t.Error("swapped-latlon not found")
}

func TestKMZFixturesMatchGenerator(t *testing.T) {
	for _, f := range fixtures.KMZ() {
		got, err := os.ReadFile(filepath.Join("testdata", f.Dir, f.Name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, f.Data()) {
			t.Errorf("%s differs from internal/fixtures; run go run ./internal/fixturegen", f.Name)
		}
	}
}

func TestEveryCodeDocumented(t *testing.T) {
	ref, err := os.ReadFile("REFERENCE.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range Codes {
		if !strings.Contains(string(ref), "`"+c.Code+"`") {
			t.Errorf("REFERENCE.md does not document %s", c.Code)
		}
	}
}

func TestOpenErrors(t *testing.T) {
	for _, in := range []string{"", "   \n", "hello world", "\x00\x01\x02"} {
		if _, err := Open("x", []byte(in)); err == nil {
			t.Errorf("Open(%q): want error", in)
		}
	}
}

func TestParseCoordinates(t *testing.T) {
	cases := []struct {
		in      string
		n       int
		bad     int
		spacing bool
	}{
		{"1,2", 1, 0, false},
		{"1,2,3 4,5,6", 2, 0, false},
		{"\n\t1,2\n\n  3,4\t", 2, 0, false},
		{"1, 2 3 ,4", 2, 0, true},
		{"1.5e2,-3E-1,4e+0", 1, 0, false},
		{"1,2,3,4", 0, 1, false},
		{"1", 0, 1, false},
		{"1,", 0, 1, false},
		{"a,b 1,2", 1, 1, false},
		{"1;2 3,4", 1, 1, false},
		{",,", 0, 1, false},
		{"NaN,1", 0, 1, false},
		{"Inf,1", 0, 1, false},
		{"0x10,1", 0, 1, false},
	}
	for _, c := range cases {
		cs, bad, sp := ParseCoordinates(c.in)
		if len(cs) != c.n || len(bad) != c.bad || sp != c.spacing {
			t.Errorf("%q: got %d coords, %d bad, spacing %v", c.in, len(cs), len(bad), sp)
		}
	}
	cs, _, _ := ParseCoordinates("34.7818, 32.0853, 12.5")
	if len(cs) != 1 || cs[0].Lon != 34.7818 || cs[0].Lat != 32.0853 || cs[0].Alt != 12.5 || !cs[0].Has3 {
		t.Errorf("spaced 3D tuple: %+v", cs)
	}
}

func TestEncodings(t *testing.T) {
	latin1 := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<kml xmlns=\"http://www.opengis.net/kml/2.2\"><Placemark><name>Caf\xe9</name><Point><coordinates>2.35,48.85</coordinates></Point></Placemark></kml>")
	d, err := Open("latin1.kml", latin1)
	if err != nil {
		t.Fatal(err)
	}
	if pm := d.Placemarks(); len(pm) != 1 || pm[0].Name != "Café" {
		t.Errorf("latin-1 name: %+v", pm)
	}
	if !codesHave(Inspect(d), "encoding-converted") {
		t.Error("encoding-converted not reported")
	}
	src := "<kml xmlns=\"http://www.opengis.net/kml/2.2\"><Placemark><name>Ünïcode</name><Point><coordinates>1,2</coordinates></Point></Placemark></kml>"
	u := []byte{0xFF, 0xFE}
	for _, r := range src {
		u = append(u, byte(r), byte(r>>8))
	}
	d, err = Open("utf16.kml", u)
	if err != nil {
		t.Fatal(err)
	}
	if pm := d.Placemarks(); len(pm) != 1 || pm[0].Name != "Ünïcode" {
		t.Errorf("utf-16 name: %+v", pm)
	}
}

func codesHave(r Report, code string) bool {
	_, ok := codesOf(r)[code]
	return ok
}

func TestNamespaces(t *testing.T) {
	for _, ns := range []string{"", NSKML22, "http://earth.google.com/kml/2.0", "http://earth.google.com/kml/2.2"} {
		x := `<kml`
		if ns != "" {
			x += ` xmlns="` + ns + `"`
		}
		x += `><Document><Placemark><name>A</name><Point><coordinates>1,2</coordinates></Point></Placemark></Document></kml>`
		d, err := Open("ns.kml", []byte(x))
		if err != nil {
			t.Fatal(err)
		}
		if n := len(d.Placemarks()); n != 1 {
			t.Errorf("namespace %q: %d placemarks", ns, n)
		}
	}
	// Prefixed KML elements.
	x := `<k:kml xmlns:k="http://www.opengis.net/kml/2.2"><k:Placemark><k:Point><k:coordinates>1,2</k:coordinates></k:Point></k:Placemark></k:kml>`
	d, _ := Open("p.kml", []byte(x))
	if n := len(d.Placemarks()); n != 1 || d.Placemarks()[0].Geometry == nil {
		t.Errorf("prefixed kml: %d placemarks", n)
	}
}

func TestReportCaps(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<kml xmlns="http://www.opengis.net/kml/2.2"><Document>`)
	for i := 0; i < 200; i++ {
		b.WriteString(`<Placemark><Point><coordinates>0,0</coordinates></Point></Placemark>`)
	}
	b.WriteString(`</Document></kml>`)
	d, _ := Open("many.kml", []byte(b.String()))
	r := Inspect(d)
	n, total := 0, 0
	for _, is := range r.Issues {
		if is.Code == "null-island" {
			n++
			total += is.Count
		}
	}
	if n != maxIssuesPerCode+1 || total != 200 {
		t.Errorf("got %d entries totalling %d", n, total)
	}
}
