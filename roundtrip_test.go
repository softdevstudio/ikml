package ikml

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/softdevstudio/ikml/internal/fixtures"
)

// flatCoords lists every position of every placemark, in order.
func flatCoords(d *Doc) [][]Coord {
	var out [][]Coord
	for _, pm := range d.Placemarks() {
		var all []Coord
		if pm.Geometry != nil {
			for _, l := range pm.Geometry.coordLists() {
				all = append(all, l...)
			}
		}
		out = append(out, all)
	}
	return out
}

// sameMultiset compares positions ignoring order inside each placemark
// (GeoJSON output rewinds rings) and ignoring the closing point that
// GeoJSON output adds to unclosed rings.
func sameMultiset(a, b []Coord) bool {
	count := map[Coord]int{}
	for _, c := range a {
		count[c]++
	}
	for _, c := range b {
		count[c]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}

func roundTrip(t testing.TB, name string, d *Doc) {
	t.Helper()
	var gj bytes.Buffer
	if err := WriteGeoJSON(d, &gj); err != nil {
		t.Fatalf("%s: WriteGeoJSON: %v", name, err)
	}
	g, err := Open("rt.geojson", gj.Bytes())
	if err != nil || !g.Readable() {
		t.Fatalf("%s: GeoJSON output does not read back: %v\n%s", name, err, gj.String())
	}
	var km bytes.Buffer
	if err := WriteKML(g, &km); err != nil {
		t.Fatalf("%s: WriteKML: %v", name, err)
	}
	k, err := Open("rt.kml", km.Bytes())
	if err != nil || !k.Readable() {
		t.Fatalf("%s: KML output does not read back: %v\n%s", name, err, km.String())
	}
	a, b, c := flatCoords(d), flatCoords(g), flatCoords(k)
	if len(a) != len(b) || len(b) != len(c) {
		t.Fatalf("%s: placemark counts %d -> %d -> %d", name, len(a), len(b), len(c))
	}
	for i := range a {
		if !sameMultiset(closeRings(d, i), b[i]) {
			t.Fatalf("%s: placemark %d changed in GeoJSON\n before %v\n after  %v", name, i, a[i], b[i])
		}
		// GeoJSON -> KML must be exact, order included.
		if len(b[i]) != len(c[i]) {
			t.Fatalf("%s: placemark %d: %d positions in GeoJSON, %d in KML", name, i, len(b[i]), len(c[i]))
		}
		for j := range b[i] {
			if b[i][j] != c[i][j] {
				t.Fatalf("%s: placemark %d position %d: %v became %v", name, i, j, b[i][j], c[i][j])
			}
		}
	}
	// Names, descriptions and attribute values survive both steps.
	pa, pc := d.Placemarks(), k.Placemarks()
	for i := range pa {
		if pa[i].Name != pc[i].Name || pa[i].Description != pc[i].Description {
			t.Fatalf("%s: placemark %d name/description changed: %q/%q", name, i, pa[i].Name, pc[i].Name)
		}
		if d.Format != FormatGeoJSON {
			want := map[string]string{}
			for _, f := range pa[i].Fields {
				want[f.Name] = f.Value
			}
			got := map[string]string{}
			for _, f := range pc[i].Fields {
				got[f.Name] = f.Value
			}
			for kk, v := range want {
				if got[kk] != v {
					t.Fatalf("%s: placemark %d field %s: %q became %q", name, i, kk, v, got[kk])
				}
			}
			if strings.Join(pa[i].FolderPath, "/") != strings.Join(pc[i].FolderPath, "/") && len(pa[i].FolderPath) > 0 {
				// Top Document names are not folders in the output.
				if strings.Join(pa[i].FolderPath, "/") != strings.Join(pc[i].FolderPath, "/") {
					t.Fatalf("%s: placemark %d folder %v became %v", name, i, pa[i].FolderPath, pc[i].FolderPath)
				}
			}
		}
	}
}

// closeRings returns the positions of placemark i with unclosed rings
// closed, which is what GeoJSON output contains.
func closeRings(d *Doc, i int) []Coord {
	pm := d.Placemarks()[i]
	var all []Coord
	if pm.Geometry == nil {
		return nil
	}
	for _, l := range pm.Geometry.leaves() {
		switch l.Type {
		case "Polygon":
			for _, r := range l.Rings {
				all = append(all, r...)
				if len(r) > 0 && !ringClosed(r) {
					all = append(all, r[0])
				}
			}
		case "Model":
		case "Point":
			if len(l.Coords) > 0 {
				all = append(all, l.Coords[0])
			}
		default:
			all = append(all, l.Coords...)
		}
	}
	return all
}

func TestRoundTripFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/good/*")
	more, _ := filepath.Glob("testdata/bad/*")
	files = append(files, more...)
	n := 0
	for _, f := range files {
		d := load(t, f)
		if !d.Readable() {
			continue
		}
		n++
		roundTrip(t, filepath.Base(f), d)
	}
	if n < 20 {
		t.Errorf("only %d fixtures round-tripped", n)
	}
}

// randDoc builds a random KML document with awkward but valid numbers.
func randDoc(r *rand.Rand) string {
	num := func(lim float64) string {
		switch r.Intn(8) {
		case 0:
			return "0"
		case 1:
			return fmt.Sprintf("%g", (r.Float64()*2-1)*lim)
		case 2:
			return fmt.Sprintf("%.15f", (r.Float64()*2-1)*lim)
		case 3:
			return fmt.Sprintf("%e", (r.Float64()*2-1)*lim)
		case 4:
			return fmt.Sprintf("%d", r.Intn(int(lim)*2+1)-int(lim))
		case 5:
			return fmt.Sprintf("%.1f", (r.Float64()*2-1)*lim)
		case 6:
			return fmt.Sprint(math.Nextafter((r.Float64()*2-1)*lim, 0))
		}
		return fmt.Sprintf("%.7g", (r.Float64()*2-1)*lim)
	}
	tuple := func(three bool) string {
		s := num(180) + "," + num(90)
		if three {
			s += "," + num(9000)
		}
		return s
	}
	seps := []string{" ", "\n", "\t", "  \n  "}
	coords := func(n int, ring bool) string {
		three := r.Intn(2) == 0
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, tuple(three))
		}
		if ring && r.Intn(3) > 0 {
			parts = append(parts, parts[0])
		}
		var b strings.Builder
		for i, p := range parts {
			if i > 0 {
				b.WriteString(seps[r.Intn(len(seps))])
			}
			b.WriteString(p)
		}
		return b.String()
	}
	var geom func(depth int) string
	geom = func(depth int) string {
		switch k := r.Intn(6); {
		case k == 0:
			return "<Point><coordinates>" + coords(1, false) + "</coordinates></Point>"
		case k == 1:
			return "<LineString><coordinates>" + coords(2+r.Intn(8), false) + "</coordinates></LineString>"
		case k == 2 || k == 3:
			s := "<Polygon><outerBoundaryIs><LinearRing><coordinates>" + coords(3+r.Intn(8), true) + "</coordinates></LinearRing></outerBoundaryIs>"
			for h := r.Intn(3); h > 0; h-- {
				s += "<innerBoundaryIs><LinearRing><coordinates>" + coords(3+r.Intn(5), true) + "</coordinates></LinearRing></innerBoundaryIs>"
			}
			return s + "</Polygon>"
		case k == 4 && depth < 2:
			s := "<MultiGeometry>"
			for p := 1 + r.Intn(3); p > 0; p-- {
				s += geom(depth + 1)
			}
			return s + "</MultiGeometry>"
		default:
			s := "<gx:Track>"
			n := 1 + r.Intn(5)
			for i := 0; i < n; i++ {
				s += fmt.Sprintf("<when>2026-01-01T00:00:%02dZ</when>", i)
			}
			for i := 0; i < n; i++ {
				s += "<gx:coord>" + num(180) + " " + num(90) + " " + num(500) + "</gx:coord>"
			}
			return s + "</gx:Track>"
		}
	}
	var b strings.Builder
	b.WriteString(`<kml xmlns="http://www.opengis.net/kml/2.2" xmlns:gx="http://www.google.com/kml/ext/2.2"><Document>`)
	var body func(depth int)
	body = func(depth int) {
		for i := r.Intn(4) + 1; i > 0; i-- {
			if depth < 3 && r.Intn(4) == 0 {
				fmt.Fprintf(&b, "<Folder><name>f%d &amp; &lt;x&gt;</name>", r.Intn(100))
				body(depth + 1)
				b.WriteString("</Folder>")
				continue
			}
			fmt.Fprintf(&b, "<Placemark><name>p%d \"q\"</name>", r.Intn(1000))
			if r.Intn(2) == 0 {
				fmt.Fprintf(&b, "<ExtendedData><Data name=\"k%d\"><value>v &amp; %d</value></Data></ExtendedData>", r.Intn(5), r.Intn(9))
			}
			if r.Intn(10) > 0 {
				b.WriteString(geom(0))
			}
			b.WriteString("</Placemark>")
		}
	}
	body(0)
	b.WriteString("</Document></kml>")
	return b.String()
}

func TestRoundTripRandom(t *testing.T) {
	r := rand.New(rand.NewSource(20261025))
	iters := 3000
	if testing.Short() {
		iters = 300
	}
	for i := 0; i < iters; i++ {
		src := randDoc(r)
		d, err := Open("rand.kml", []byte(src))
		if err != nil || !d.Readable() {
			t.Fatalf("iteration %d: generated KML does not parse: %v\n%s", i, err, src)
		}
		if n := countIssue(d, "coord-parse"); n > 0 {
			t.Fatalf("iteration %d: generated coordinates rejected\n%s", i, src)
		}
		roundTrip(t, fmt.Sprintf("random #%d", i), d)
	}
	t.Logf("random round trips: %d generated KML files, KML to GeoJSON to KML, every position kept", iters)
}

func TestFormatFloatExact(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		v := math.Float64frombits(r.Uint64())
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		s := formatFloat(v)
		cs, bad, _ := ParseCoordinates(s + "," + s)
		if len(bad) > 0 || len(cs) != 1 || cs[0].Lon != v {
			t.Fatalf("%v formatted as %q does not read back", v, s)
		}
	}
	t.Logf("exact numbers: 200000 random floats written and read back bit for bit")
}

func cleanOnce(t testing.TB, d *Doc, opt CleanOptions) (*Doc, []byte) {
	t.Helper()
	c, _, err := Clean(d, opt)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	switch c.Format {
	case FormatGeoJSON:
		err = WriteGeoJSON(c, &b)
	case FormatKMZ:
		err = WriteKMZ(c, &b, nil)
	default:
		err = WriteKML(c, &b)
	}
	if err != nil {
		t.Fatal(err)
	}
	return c, b.Bytes()
}

// Codes Clean is supposed to fix.
var fixable = []string{"ring-unclosed", "duplicate-points", "coord-spacing", "style-missing", "old-namespace", "no-namespace", "geojson-crs"}

func TestCleanIdempotent(t *testing.T) {
	files, _ := filepath.Glob("testdata/*/*")
	for _, f := range files {
		d := load(t, f)
		if !d.Readable() {
			if _, _, err := Clean(d, CleanOptions{}); err == nil {
				t.Errorf("%s: Clean of unreadable file should fail", f)
			}
			continue
		}
		for _, opt := range []CleanOptions{{}, {FixSwapped: true, DropUnusedAssets: true}} {
			c1, b1 := cleanOnce(t, d, opt)
			_, b2 := cleanOnce(t, c1, opt)
			if !bytes.Equal(b1, b2) {
				t.Errorf("%s %+v: clean is not idempotent\nfirst:\n%s\nsecond:\n%s", f, opt, b1, b2)
			}
			_, ch2, _ := Clean(c1, opt)
			if len(ch2) > 0 {
				t.Errorf("%s: second clean still changed things: %+v", f, ch2)
			}
			r := Inspect(c1)
			for _, is := range r.Issues {
				for _, code := range fixable {
					if is.Code == code && !(code == "geojson-crs" && !isWGS84(c1.CRSName)) && !unfixableHere(is) {
						t.Errorf("%s: %s still reported after clean: %s", f, code, is.Message)
					}
				}
				if opt.FixSwapped && is.Code == "swapped-latlon" {
					t.Errorf("%s: swapped-latlon still reported after --fix-swapped", f)
				}
				if opt.DropUnusedAssets && is.Code == "unused-asset" && c1.MainPath != "" {
					t.Errorf("%s: unused-asset still reported after DropUnusedAssets", f)
				}
			}
		}
	}
}

// unfixableHere is true for issues Clean leaves on purpose: a ring that
// is too short to close, or a shape that also has unreadable tuples.
func unfixableHere(is Issue) bool {
	return strings.Contains(is.Message, "outer ring has 3") || strings.Contains(is.Message, "has 2 positions")
}

func TestCleanSpecifics(t *testing.T) {
	d := load(t, "testdata/bad/swapped-latlon.kml")
	c, ch, err := Clean(d, CleanOptions{FixSwapped: true})
	if err != nil {
		t.Fatal(err)
	}
	pm := c.Placemarks()[0]
	if pm.Geometry.Coords[0].Lon != 134.7818 || pm.Geometry.Coords[0].Lat != 32.0853 {
		t.Errorf("swap not applied: %+v", pm.Geometry.Coords)
	}
	if len(ch) != 3 {
		t.Errorf("want 3 swap changes, got %+v", ch)
	}
	// Without the option nothing moves.
	c, _, _ = Clean(d, CleanOptions{})
	if c.Placemarks()[0].Geometry.Coords[0].Lon != 32.0853 {
		t.Error("swap applied without FixSwapped")
	}

	d = load(t, "testdata/bad/broken-style.kml")
	c, _, _ = Clean(d, CleanOptions{})
	var out bytes.Buffer
	WriteKML(c, &out)
	if strings.Contains(out.String(), "#blue") || strings.Contains(out.String(), "#red-hover") {
		t.Error("broken styleUrl not removed")
	}
	if !strings.Contains(out.String(), "#pin") {
		t.Error("valid styleUrl removed")
	}

	d = load(t, "testdata/bad/kml21-namespace.kml")
	c, _, _ = Clean(d, CleanOptions{})
	if c.Namespace != NSKML22 {
		t.Errorf("namespace %q", c.Namespace)
	}

	// Unknown elements and comments survive KML cleaning.
	src := `<kml xmlns="http://www.opengis.net/kml/2.2" xmlns:x="urn:x"><!-- keep me --><Placemark><x:custom>1</x:custom><Polygon><outerBoundaryIs><LinearRing><coordinates>0,1 1,1 1,0</coordinates></LinearRing></outerBoundaryIs></Polygon></Placemark></kml>`
	d, _ = Open("u.kml", []byte(src))
	c, _, _ = Clean(d, CleanOptions{})
	out.Reset()
	WriteKML(c, &out)
	for _, want := range []string{"<!-- keep me -->", "<x:custom>1</x:custom>", "0,1 1,1 1,0 0,1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("clean output lost %q:\n%s", want, out.String())
		}
	}

	// KMZ: referenced assets kept, unused kept by default.
	d = load(t, "testdata/bad/overlay-missing-icon.kmz")
	c, _, _ = Clean(d, CleanOptions{})
	if len(c.Assets) != 2 {
		t.Errorf("assets after clean: %+v", c.Assets)
	}
	c, _, _ = Clean(d, CleanOptions{DropUnusedAssets: true})
	if len(c.Assets) != 1 || c.Assets[0].Path != "files/plan.png" {
		t.Errorf("assets after drop: %+v", c.Assets)
	}
}

func TestLossReport(t *testing.T) {
	has := func(ls []Loss, what string) *Loss {
		for i := range ls {
			if ls[i].What == what {
				return &ls[i]
			}
		}
		return nil
	}
	ls := LossReport(load(t, "testdata/good/earth-pro-export.kml"), "geojson")
	for _, w := range []string{"Styles and colors", "Style details with no GeoJSON equivalent", "Folder structure", "Saved views (LookAt, Camera)", "Elements iKML does not interpret"} {
		if has(ls, w) == nil {
			t.Errorf("earth pro: missing loss %q", w)
		}
	}
	if l := has(ls, "Saved views (LookAt, Camera)"); l != nil && l.Count != 2 {
		t.Errorf("views: %d", l.Count)
	}
	ls = LossReport(load(t, "testdata/good/arcgis-export.kml"), "geojson")
	if l := has(ls, "Attribute data (ExtendedData)"); l == nil || l.Outcome != "changed" || !strings.Contains(l.Detail, "AREA_M2 (double)") {
		t.Errorf("arcgis: %+v", l)
	}
	ls = LossReport(load(t, "testdata/good/gps-track.kml"), "geojson")
	if has(ls, "GPS tracks (gx:Track)") == nil || has(ls, "Per-point track data (SimpleArrayData)") == nil {
		t.Errorf("gps: %+v", ls)
	}
	ls = LossReport(load(t, "testdata/bad/network-link.kml"), "geojson")
	if l := has(ls, "Network links"); l == nil || len(l.Items) != 2 {
		t.Errorf("network links: %+v", l)
	}
	ls = LossReport(load(t, "testdata/good/photo-tour.kmz"), "geojson")
	if l := has(ls, "Files inside the KMZ"); l == nil || l.Count != 3 {
		t.Errorf("kmz assets: %+v", l)
	}
	if ls := LossReport(load(t, "testdata/good/photo-tour.kmz"), "kmz"); len(ls) != 0 {
		t.Errorf("kmz->kmz should lose nothing: %+v", ls)
	}
	ls = LossReport(load(t, "testdata/bad/nested-collection.geojson"), "kml")
	if has(ls, "Foreign members") == nil {
		t.Errorf("geojson->kml: %+v", ls)
	}
	ls = LossReport(load(t, "testdata/good/clean.geojson"), "kml")
	if has(ls, "Nested properties") == nil || has(ls, "Number and true/false properties") == nil {
		t.Errorf("geojson->kml props: %+v", ls)
	}

	// What the report says is kept really is kept.
	var b bytes.Buffer
	WriteGeoJSON(load(t, "testdata/good/gps-track.kml"), &b)
	for _, want := range []string{`"coordTimes":["2026-10-12T06:01:00Z"`, `"stroke":"#ff7f2a"`, `"timestamp":"2026-10-12T06:40:00Z"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("GeoJSON output missing %s:\n%s", want, b.String())
		}
	}
	b.Reset()
	WriteGeoJSON(load(t, "testdata/good/mymaps-export.kml"), &b)
	for _, want := range []string{`"folder":"Meeting points"`, `"contact":"Dana"`, `"marker-color":"#0288d1"`, `"description":"Bus stops here.<br>Bring water."`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("GeoJSON output missing %s", want)
		}
	}
}

// zipWithLyingSizes builds a zip whose central directory claims small
// sizes while the stored data expands much further.
func bombKMZ(t *testing.T, realSize int) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "doc.kml", Method: zip.Deflate})
	w.Write([]byte(`<kml xmlns="http://www.opengis.net/kml/2.2"><Document>`))
	chunk := bytes.Repeat([]byte(" "), 1<<20)
	for n := 0; n < realSize; n += len(chunk) {
		w.Write(chunk)
	}
	w.Write([]byte(`</Document></kml>`))
	zw.Close()
	return b.Bytes()
}

func TestZipBomb(t *testing.T) {
	// A highly compressible entry trips the ratio check.
	d, err := Open("bomb.kmz", bombKMZ(t, 64<<20))
	if err != nil {
		t.Fatal(err)
	}
	if !codesHave(Inspect(d), "kmz-too-large") || d.Readable() {
		t.Error("ratio bomb not caught")
	}
	// A directory that claims a huge total is refused before reading.
	big := fixtures.Zip([]fixtures.Entry{{Name: "doc.kml", Body: []byte("<kml/>")}})
	big = patchSizes(t, big, 600<<20)
	d, _ = Open("claims.kmz", big)
	if !codesHave(Inspect(d), "kmz-too-large") {
		t.Error("claimed size not caught")
	}
	// An entry whose directory understates its size is stopped while
	// reading. Patch the claimed size of a 40 MiB entry down to 1 KiB.
	sneaky := bombKMZ(t, 40<<20)
	sneaky = patchSizes(t, sneaky, 1024)
	d, _ = Open("sneaky.kmz", sneaky)
	if d.Readable() && len(d.raw) > 2048 {
		t.Errorf("read %d bytes past the claimed size", len(d.raw))
	}
}

// patchSizes rewrites the uncompressed size of every entry in the
// central directory and local headers.
func patchSizes(t *testing.T, z []byte, size uint32) []byte {
	z = append([]byte(nil), z...)
	for i := 0; i+30 < len(z); i++ {
		switch binary.LittleEndian.Uint32(z[i:]) {
		case 0x04034b50: // local header: size at +22
			binary.LittleEndian.PutUint32(z[i+22:], size)
		case 0x02014b50: // central directory: size at +24
			binary.LittleEndian.PutUint32(z[i+24:], size)
		case 0x08074b50: // data descriptor: size at +12
			binary.LittleEndian.PutUint32(z[i+12:], size)
		}
	}
	return z
}

func TestPreview(t *testing.T) {
	d, _ := Open("big.kml", fixtures.BigKML(20000))
	sh := Preview(d, 2000)
	n := 0
	for _, s := range sh {
		for _, r := range s.Rings {
			n += len(r)
		}
	}
	if n > 2100 || n < 1000 || len(sh) != 20 {
		t.Errorf("preview: %d shapes, %d points", len(sh), n)
	}
}

func TestWriteKMZRefusesUnsafeNames(t *testing.T) {
	d := load(t, "testdata/good/photo-tour.kmz")
	for _, name := range []string{"../x.png", "/etc/x", `C:\x.dll`} {
		var buf bytes.Buffer
		err := WriteKMZ(d, &buf, &KMZOptions{Extra: map[string][]byte{name: []byte("x")}})
		if err == nil {
			t.Errorf("%s written into a KMZ", name)
		}
	}
}
