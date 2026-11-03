package ikml

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeTo(t *testing.T, d *Doc, target string) *Doc {
	t.Helper()
	var buf bytes.Buffer
	var err error
	switch target {
	case "gpx":
		err = WriteGPX(d, &buf)
	case "geojson":
		err = WriteGeoJSON(d, &buf)
	case "kml":
		err = WriteKML(d, &buf)
	case "csv":
		err = WriteCSV(d, &buf)
	}
	if err != nil {
		t.Fatal(err)
	}
	out, err := Open("out."+target, buf.Bytes())
	if err != nil {
		t.Fatalf("%s output: %v\n%s", target, err, buf.String())
	}
	if !out.Readable() {
		t.Fatalf("%s output is not readable: %+v\n%s", target, Inspect(out).Issues, buf.String())
	}
	return out
}

type shape struct {
	Name   string
	Coords []Coord
	When   []string
}

// shapes flattens a document's placemarks into names, positions and times.
func shapes(d *Doc) []shape {
	var out []shape
	for _, pm := range d.Placemarks() {
		name, _, _ := gpxText(pm)
		if pm.Geometry == nil {
			continue
		}
		for _, l := range pm.Geometry.leaves() {
			when := l.When
			if l.Type != "Track" {
				when = nil
			}
			out = append(out, shape{name, l.Coords, when})
		}
	}
	return out
}

// GPX written from a GPX source is the source file, byte for byte.
func TestGPXRoundTripUnchanged(t *testing.T) {
	for _, f := range []string{"garmin-connect.gpx", "strava.gpx", "hike-plan.gpx"} {
		t.Run(f, func(t *testing.T) {
			raw := readFile(t, "testdata/good/"+f)
			src := load(t, "testdata/good/"+f)
			if ls := LossReport(src, "gpx"); len(ls) != 0 {
				t.Errorf("GPX to GPX reports losses: %+v", ls)
			}
			var buf bytes.Buffer
			if err := WriteGPX(src, &buf); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), raw) {
				t.Error("GPX output differs from the source file")
			}
		})
	}
}

// Attributes, namespace declarations on inner elements, a prefixed root,
// comments and empty segments all survive GPX to GPX, also after clean
// swaps a point.
func TestGPXKeepsEverything(t *testing.T) {
	src := `<?xml version="1.0"?>
<!-- exported by a test -->
<g:gpx xmlns:g="http://www.topografix.com/GPX/1/1" version="1.1" creator="t">
  <g:wpt lat="45.5" lon="9.2" xmlns:x="urn:x" x:id="7"><g:name>A</g:name></g:wpt>
  <g:trk><g:name>T</g:name>
    <g:trkseg/>
    <g:trkseg>
      <g:trkpt lat='10.1' lon="20.2" x:a="1" xmlns:x="urn:x"><g:time>2026-05-01T07:00:00Z</g:time></g:trkpt>
      <!-- a note -->
      <g:trkpt lat="85" lon="10"><g:time>2026-05-01T07:01:00Z</g:time></g:trkpt>
    </g:trkseg>
  </g:trk>
</g:gpx>
`
	d, err := Open("x.gpx", []byte(src))
	if err != nil || !d.Readable() {
		t.Fatalf("open: %v %+v", err, Inspect(d).Issues)
	}
	if n := len(d.Placemarks()); n != 2 {
		t.Fatalf("%d placemarks", n)
	}
	var buf bytes.Buffer
	WriteGPX(d, &buf)
	if buf.String() != src {
		t.Errorf("GPX output changed the file:\n%s", buf.String())
	}
	// Swap the second point by hand, as clean does, and only its values change.
	pm := d.Placemarks()[1]
	l := pm.Geometry.leaves()[0]
	l.Coords[1] = Coord{Lon: 85, Lat: 10}
	buf.Reset()
	WriteGPX(d, &buf)
	want := strings.Replace(src, `lat="85" lon="10"`, `lat="10" lon="85"`, 1)
	if buf.String() != want {
		t.Errorf("swap splice wrong:\n%s", buf.String())
	}
}

// GPX 1.0 keeps the file's name, time and bounds under the root; they are
// metadata, not unknown elements, and conversions report them.
func TestGPX10Metadata(t *testing.T) {
	src := `<gpx xmlns="http://www.topografix.com/GPX/1/0" version="1.0" creator="t">
<name>Old</name><time>2004-01-01T00:00:00Z</time><bounds minlat="1" minlon="1" maxlat="2" maxlon="2"/>
<wpt lat="1.5" lon="1.5"><name>P</name><url>http://x</url><urlname>x</urlname></wpt>
</gpx>`
	d, err := Open("o.gpx", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := codesOf(Inspect(d))["unknown-element"]; ok {
		t.Error("GPX 1.0 root elements reported as unknown")
	}
	ls := lossByWhat(LossReport(d, "geojson"))
	if l := ls["Metadata"]; l.Outcome != "lost" || !reflect.DeepEqual(l.Items, []string{"bounds", "name", "time"}) {
		t.Errorf("metadata loss: %+v", l)
	}
	if l, ok := ls["Other waypoint, route and track details"]; !ok || l.Count != 2 {
		t.Errorf("url and urlname not reported: %+v", ls)
	}
}

func lossByWhat(ls []Loss) map[string]Loss {
	m := map[string]Loss{}
	for _, l := range ls {
		m[l.What] = l
	}
	return m
}

// GPX to GeoJSON or KML and back keeps positions, elevations, times and
// names, and the loss report names exactly what went.
func TestGPXExportsThroughOtherFormats(t *testing.T) {
	type want struct {
		ext      []string
		extCount int
		meta     []string
		fields   int
		other    map[string]int // other expected losses: what -> count
	}
	cases := map[string]want{
		"garmin-connect.gpx": {ext: []string{"atemp (40)", "cad (40)", "hr (40)"}, extCount: 40, meta: []string{"link", "time"}, fields: 1},
		"strava.gpx":         {ext: []string{"cad (60)", "hr (60)", "power (60)"}, extCount: 60, meta: []string{"time"}, fields: 1},
		"hike-plan.gpx": {meta: []string{"author", "name"}, fields: 6, other: map[string]int{
			"Other track point details": 2, "Route point details": 4,
			"Extensions on the file, waypoints, routes, tracks and segments": 2,
			"Other waypoint, route and track details":                        3, "Tracks with several segments": 1}},
	}
	for f, w := range cases {
		for _, target := range []string{"geojson", "kml"} {
			t.Run(f+" via "+target, func(t *testing.T) {
				src := load(t, "testdata/good/"+f)
				ls := lossByWhat(LossReport(src, target))
				want := 2 + len(w.other) // metadata and fields
				if w.extCount > 0 {
					want++
					l := ls["Track point extensions"]
					if l.Count != w.extCount || l.Outcome != "lost" || !reflect.DeepEqual(l.Items, w.ext) {
						t.Errorf("extensions: %+v", l)
					}
				}
				if l := ls["Metadata"]; l.Outcome != "lost" || !reflect.DeepEqual(l.Items, w.meta) {
					t.Errorf("metadata: %+v", l)
				}
				if l := ls["Comments, sources, symbols, types and links"]; l.Count != w.fields || l.Outcome != "kept" {
					t.Errorf("fields: %+v", l)
				}
				for what, n := range w.other {
					if ls[what].Count != n {
						t.Errorf("%s: got %+v, want count %d", what, ls[what], n)
					}
				}
				if len(ls) != want {
					var got []string
					for k := range ls {
						got = append(got, k)
					}
					t.Errorf("%d losses, want %d: %v", len(ls), want, got)
				}
				mid := writeTo(t, src, target)
				back := writeTo(t, mid, "gpx")
				// Routes come back as tracks without times; compare positions
				// and times of every shape.
				a, b := shapes(src), shapes(back)
				if len(a) != len(b) {
					t.Fatalf("%d shapes, then %d", len(a), len(b))
				}
				for i := range a {
					if !reflect.DeepEqual(a[i].Coords, b[i].Coords) || !reflect.DeepEqual(a[i].When, b[i].When) || a[i].Name != b[i].Name {
						t.Errorf("shape %d changed:\n%+v\n%+v", i, a[i], b[i])
					}
				}
				if ls := LossReport(mid, "gpx"); target == "geojson" {
					for _, l := range ls {
						if l.What != "Attribute values" {
							t.Errorf("unexpected loss going back: %+v", l)
						}
					}
				}
			})
		}
	}
}

func TestGPXToGeoJSONProperties(t *testing.T) {
	src := load(t, "testdata/good/hike-plan.gpx")
	var buf bytes.Buffer
	if err := WriteGeoJSON(src, &buf); err != nil {
		t.Fatal(err)
	}
	var fc struct {
		Features []struct {
			Properties map[string]any
			Geometry   struct{ Type string }
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &fc); err != nil {
		t.Fatal(err)
	}
	if len(fc.Features) != 4 {
		t.Fatalf("%d features", len(fc.Features))
	}
	p := fc.Features[0].Properties
	if p["name"] != "Parking" || p["sym"] != "Parking Area" || p["link"] != "https://example.org/ein-gedi" || p["timestamp"] != "2026-04-20T05:40:00Z" {
		t.Errorf("waypoint properties: %v", p)
	}
	if g := fc.Features[3].Geometry.Type; g != "MultiLineString" {
		t.Errorf("two-segment track is %s", g)
	}
	if _, ok := fc.Features[3].Properties["coordTimes"].([]any); !ok {
		t.Errorf("no coordTimes: %v", fc.Features[3].Properties)
	}
}

func TestGPXFromKML(t *testing.T) {
	src := load(t, "testdata/good/gps-track.kml")
	out := writeTo(t, src, "gpx")
	var buf bytes.Buffer
	WriteGPX(src, &buf)
	if !strings.Contains(buf.String(), "<time>") || !strings.Contains(buf.String(), "<wpt ") {
		t.Errorf("expected a timed track and a waypoint:\n%s", buf.String())
	}
	a, b := shapes(src), shapes(out)
	sort.Slice(a, func(i, j int) bool { return a[i].Name < a[j].Name })
	sort.Slice(b, func(i, j int) bool { return b[i].Name < b[j].Name })
	if !reflect.DeepEqual(a, b) {
		t.Errorf("track changed:\n%+v\n%+v", a, b)
	}
	if lossByWhat(LossReport(src, "gpx"))["Order"].Count != 1 {
		t.Errorf("the moved waypoint isn't reported: %+v", LossReport(src, "gpx"))
	}
	poly := load(t, "testdata/good/arcgis-export.kml")
	ls := lossByWhat(LossReport(poly, "gpx"))
	if ls["Polygons and 3D models"].Count == 0 || ls["Attribute values"].Count == 0 {
		t.Errorf("polygon losses: %+v", ls)
	}
}

func TestGPXProblems(t *testing.T) {
	r := Inspect(load(t, "testdata/bad/gpx-problems.gpx"))
	for _, is := range r.Issues {
		switch is.Code {
		case "gpx-time-backwards":
			if is.Count != 1 {
				t.Errorf("backwards: %+v", is)
			}
		case "gpx-time-invalid":
			if is.Count != 1 || !strings.Contains(is.Location, "trkseg[1]") {
				t.Errorf("invalid: %+v", is)
			}
		}
	}
	if r.Stats.Placemarks != 1 {
		t.Errorf("placemarks: %d", r.Stats.Placemarks)
	}
}

func TestGPXCleanSwapped(t *testing.T) {
	// Longitudes written into lat and latitudes into lon.
	data := []byte(`<?xml version="1.0"?><gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><wpt lat="134.78" lon="32.08"><name>a</name></wpt><wpt lat="135.21" lon="31.77"><name>b</name></wpt><trk><trkseg><trkpt lat="134.78" lon="32.08"><ele>5</ele><extensions><hr>90</hr></extensions></trkpt><trkpt lat="134.79" lon="32.09"/></trkseg></trk></gpx>`)
	d, err := Open("x.gpx", data)
	if err != nil {
		t.Fatal(err)
	}
	c, changes, err := Clean(d, CleanOptions{FixSwapped: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("no changes")
	}
	pm := c.Placemarks()[2]
	if got := pm.Geometry.Coords[0]; got.Lat != 32.08 || got.Lon != 134.78 {
		t.Errorf("coords %+v", got)
	}
	var buf bytes.Buffer
	WriteGPX(c, &buf)
	if !strings.Contains(buf.String(), `<trkpt lat="32.08" lon="134.78"><ele>5</ele><extensions><hr>90</hr></extensions></trkpt>`) {
		t.Errorf("point details lost in clean:\n%s", buf.String())
	}
}
