package ikml

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

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

// GPX written from a GPX source keeps everything: the verbatim parts are
// the same bytes, and the model is the same.
func TestGPXRoundTripUnchanged(t *testing.T) {
	for _, f := range []string{"garmin-connect.gpx", "strava.gpx", "hike-plan.gpx"} {
		t.Run(f, func(t *testing.T) {
			src := load(t, "testdata/good/"+f)
			out := writeTo(t, src, "gpx")
			if ls := LossReport(src, "gpx"); len(ls) != 0 {
				t.Errorf("GPX to GPX reports losses: %+v", ls)
			}
			if !reflect.DeepEqual(shapes(src), shapes(out)) {
				t.Error("positions, times or names changed")
			}
			a, b := src.gpx, out.gpx
			if a.rootAttrs != b.rootAttrs || !bytes.Equal(a.meta, b.meta) || !bytes.Equal(a.ext, b.ext) {
				t.Errorf("root, metadata or extensions changed:\n%q\n%q", a.rootAttrs, b.rootAttrs)
			}
			sp, op := src.Placemarks(), out.Placemarks()
			if len(sp) != len(op) {
				t.Fatalf("%d placemarks, then %d", len(sp), len(op))
			}
			for i := range sp {
				if !reflect.DeepEqual(sp[i].Fields, op[i].Fields) || sp[i].Description != op[i].Description || sp[i].TimeStamp != op[i].TimeStamp {
					t.Errorf("placemark %d details changed", i)
				}
				if !bytes.Equal(sp[i].gpx.head, op[i].gpx.head) || !reflect.DeepEqual(sp[i].gpx.segTails, op[i].gpx.segTails) {
					t.Errorf("placemark %d verbatim parts changed", i)
				}
				if sp[i].Geometry == nil {
					continue
				}
				sl, ol := sp[i].Geometry.leaves(), op[i].Geometry.leaves()
				for j := range sl {
					if !reflect.DeepEqual(sl[j].ptRaw, ol[j].ptRaw) {
						t.Errorf("placemark %d segment %d point details changed", i, j)
					}
				}
			}
			if !reflect.DeepEqual(codesOf(Inspect(src)), codesOf(Inspect(out))) {
				t.Errorf("issues changed: %v then %v", sortedCodes(codesOf(Inspect(src))), sortedCodes(codesOf(Inspect(out))))
			}
			// A second pass gives the same bytes.
			var b1, b2 bytes.Buffer
			WriteGPX(src, &b1)
			WriteGPX(out, &b2)
			if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
				t.Error("GPX output is not stable")
			}
		})
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
		"hike-plan.gpx": {meta: []string{"name", "author"}, fields: 6, other: map[string]int{
			"Other track point details": 2, "Route point details": 4,
			"Extensions on the file, waypoints, routes, tracks and segments": 2,
			"Other waypoint details": 2, "Tracks with several segments": 1}},
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
	if !bytes.Contains(pm.Geometry.ptRaw[0], []byte("<hr>90</hr>")) {
		t.Error("point details lost in clean")
	}
}
