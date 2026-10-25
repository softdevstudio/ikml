package ikml

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/softdevstudio/ikml/internal/fixtures"
)

// exercise runs every public entry point on a document. None may panic.
func exercise(t *testing.T, data []byte) {
	d, err := Open("fuzz", data)
	if err != nil {
		return
	}
	r := Inspect(d)
	if r.Issues == nil {
		t.Fatal("nil issues")
	}
	for _, target := range []string{"geojson", "kml", "kmz"} {
		LossReport(d, target)
	}
	Preview(d, 500)
	if !d.Readable() {
		return
	}
	var b bytes.Buffer
	if err := WriteGeoJSON(d, &b); err != nil {
		t.Fatal(err)
	}
	if g, err := Open("out.geojson", b.Bytes()); err != nil || !g.Readable() {
		t.Fatalf("GeoJSON output does not read back: %v\n%s", err, b.String())
	}
	b.Reset()
	if err := WriteKML(d, &b); err != nil {
		t.Fatal(err)
	}
	c, _, err := Clean(d, CleanOptions{FixSwapped: true, DropUnusedAssets: true})
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	Inspect(c)
}

func seed(f *testing.F, glob string) {
	files, _ := filepath.Glob(glob)
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err == nil {
			f.Add(b)
		}
	}
	// Regression inputs found by earlier fuzzing live in testdata/fuzz.
}

func FuzzOpenKML(f *testing.F) {
	seed(f, "testdata/*/*.kml")
	f.Add([]byte(`<kml><Placemark><MultiGeometry><MultiGeometry><Point><coordinates>1,2</coordinates></Point></MultiGeometry></MultiGeometry></Placemark></kml>`))
	f.Add([]byte(`<kml xmlns:gx="http://www.google.com/kml/ext/2.2"><Placemark><gx:Track><when>x</when><gx:coord>1 2 3</gx:coord></gx:Track></Placemark></kml>`))
	f.Fuzz(exercise)
}

func FuzzOpenKMZ(f *testing.F) {
	seed(f, "testdata/*/*.kmz")
	for _, k := range fixtures.KMZ() {
		f.Add(k.Data())
	}
	f.Fuzz(exercise)
}

func FuzzOpenGeoJSON(f *testing.F) {
	seed(f, "testdata/*/*.geojson")
	f.Add([]byte(`{"type":"GeometryCollection","geometries":[{"type":"GeometryCollection","geometries":[]}]}`))
	f.Add([]byte(`{"type":"Feature","properties":{"coordTimes":["a","b"]},"geometry":{"type":"LineString","coordinates":[[1,2],[3,4]]}}`))
	f.Fuzz(exercise)
}

func FuzzParseCoordinates(f *testing.F) {
	for _, s := range []string{"1,2", "1, 2 3 ,4", "1e5,2e-3,3", ",,", "1,2,3,4", "\n\t-0,+0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		cs, _, _ := ParseCoordinates(s)
		// Every parsed tuple must survive formatting and parsing again.
		out := kmlCoords(cs)
		back, bad, _ := ParseCoordinates(out)
		if len(bad) > 0 || len(back) != len(cs) {
			t.Fatalf("%q -> %q -> %d coords, %d bad", s, out, len(back), len(bad))
		}
		for i := range cs {
			if back[i] != cs[i] && !(cs[i].Lon != cs[i].Lon) {
				t.Fatalf("%v became %v", cs[i], back[i])
			}
		}
	})
}
