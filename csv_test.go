package ikml

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestCSVRead(t *testing.T) {
	d := load(t, "testdata/good/stations.csv")
	if d.Format != FormatCSV {
		t.Fatalf("format %s", d.Format)
	}
	pms := d.Placemarks()
	if len(pms) != 3 || pms[0].Name != "Rothschild, north" {
		t.Fatalf("placemarks: %d %q", len(pms), pms[0].Name)
	}
	c := pms[0].Geometry.Coords[0]
	if c.Lat != 32.0636 || c.Lon != 34.7756 || !c.Has3 || c.Alt != 18 {
		t.Errorf("coords %+v", c)
	}
	want := []Field{{Name: "operator", Value: "Tel-O-Fun", Source: "csv"}, {Name: "bikes", Value: "12", Source: "csv"}}
	if !reflect.DeepEqual(pms[0].Fields, want) {
		t.Errorf("fields %+v", pms[0].Fields)
	}
	eu := load(t, "testdata/good/europe-semicolon.csv")
	if c := eu.Placemarks()[1].Geometry.Coords[0]; c.Lat != 48.1845 || c.Lon != 16.3122 {
		t.Errorf("decimal commas: %+v", c)
	}
	if eu.Placemarks()[1].Name != "Schönbrunn" {
		t.Errorf("name %q", eu.Placemarks()[1].Name)
	}
}

func TestCSVRows(t *testing.T) {
	d := load(t, "testdata/bad/csv-rows.csv")
	if n := len(d.Placemarks()); n != 2 {
		t.Errorf("%d placemarks, want Ok and Short", n)
	}
	r := Inspect(d)
	bad := 0
	for _, is := range r.Issues {
		if is.Code == "csv-bad-row" {
			bad++
			if is.Line != 3 && is.Line != 4 {
				t.Errorf("bad row at line %d", is.Line)
			}
		}
	}
	if bad != 2 {
		t.Errorf("%d bad rows", bad)
	}
}

func TestCSVNotCSV(t *testing.T) {
	if _, err := Open("notes.txt", []byte("hello world\n")); err != ErrUnknownFormat {
		t.Errorf("plain text: %v", err)
	}
	d, err := Open("cities.csv", []byte("name,city\nDuomo,Milan\n"))
	if err != nil || d.Readable() {
		t.Fatalf("a .csv without coordinates should open unreadable: %v", err)
	}
	if _, err := Open("cities.txt", []byte("name,city\nDuomo,Milan\n")); err != ErrUnknownFormat {
		t.Errorf("a delimited .txt without coordinates: %v", err)
	}
}

// CSV written from a CSV source keeps its header, column order and values.
func TestCSVRoundTrip(t *testing.T) {
	for _, f := range []string{"stations.csv", "europe-semicolon.csv"} {
		src := load(t, "testdata/good/"+f)
		var buf bytes.Buffer
		if err := WriteCSV(src, &buf); err != nil {
			t.Fatal(err)
		}
		back, err := Open(f, buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(shapes(src), shapes(back)) {
			t.Errorf("%s: positions changed", f)
		}
		for i, pm := range src.Placemarks() {
			if !reflect.DeepEqual(pm.csv.values, back.Placemarks()[i].csv.values) {
				t.Errorf("%s row %d: %v then %v", f, i, pm.csv.values, back.Placemarks()[i].csv.values)
			}
		}
		if len(LossReport(src, "csv")) != 0 {
			t.Errorf("CSV to CSV reports losses")
		}
	}
	// Through GeoJSON and back: positions, names and every column survive.
	src := load(t, "testdata/good/stations.csv")
	mid := writeTo(t, src, "geojson")
	back := writeTo(t, mid, "csv")
	var buf bytes.Buffer
	WriteCSV(back, &buf)
	want := "name,lat,lon,ele,operator,bikes\n\"Rothschild, north\",32.0636,34.7756,18,Tel-O-Fun,12\nHabima,32.0726,34.7794,20,Tel-O-Fun,8\nCarmel Market,32.0681,34.7685,14,Tel-O-Fun,\n"
	if buf.String() != want {
		t.Errorf("CSV via GeoJSON:\n%s\nwant\n%s", buf.String(), want)
	}
	if l := lossByWhat(LossReport(src, "geojson"))["Other columns"]; l.Count != 6 || l.Outcome != "changed" {
		t.Errorf("loss to GeoJSON: %+v", l)
	}
}

func TestCSVFromOtherFormats(t *testing.T) {
	d := load(t, "testdata/good/earth-pro-export.kml")
	var buf bytes.Buffer
	if err := WriteCSV(d, &buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "name,lat,lon") {
		t.Errorf("CSV from KML:\n%s", buf.String())
	}
	ls := lossByWhat(LossReport(d, "csv"))
	if ls["Lines, tracks and polygons"].Count != 2 || ls["Folders"].Count == 0 {
		t.Errorf("losses: %+v", ls)
	}
	g := load(t, "testdata/good/hike-plan.gpx")
	buf.Reset()
	WriteCSV(g, &buf)
	if !strings.Contains(buf.String(), "Parking") || strings.Contains(buf.String(), "Recorded hike") {
		t.Errorf("CSV from GPX should hold the waypoints only:\n%s", buf.String())
	}
	if lossByWhat(LossReport(g, "csv"))["Routes and tracks"].Count != 2 {
		t.Errorf("GPX to CSV losses: %+v", LossReport(g, "csv"))
	}
}

func TestCSVCleanSwapped(t *testing.T) {
	d, err := Open("x.csv", []byte("name,lat,lon\na,134.78,32.08\nb,135.2,31.7\n"))
	if err != nil {
		t.Fatal(err)
	}
	c, changes, err := Clean(d, CleanOptions{FixSwapped: true})
	if err != nil || len(changes) == 0 {
		t.Fatalf("clean: %v %v", changes, err)
	}
	var buf bytes.Buffer
	WriteCSV(c, &buf)
	if buf.String() != "name,lat,lon\na,32.08,134.78\nb,31.7,135.2\n" {
		t.Errorf("cleaned CSV:\n%s", buf.String())
	}
}

// CSV written from a CSV source is the source file, byte for byte: a BOM,
// CRLF line endings, rows that can't be placed and ragged rows all stay.
// Clean rewrites only the rows it fixes.
func TestCSVKeepsEverything(t *testing.T) {
	src := "\xEF\xBB\xBFname;lat;lon;note\r\n" +
		"A;45,5;9,2;x\r\n" +
		"B;n/a;9,3;bad\r\n" +
		"C;45,6;9,4;x;extra\r\n" +
		"D;134,78;32,08;swapped\r\n"
	d, err := Open("x.csv", []byte(src))
	if err != nil || !d.Readable() {
		t.Fatalf("open: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteCSV(d, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != src {
		t.Errorf("CSV output changed the file:\n%q", buf.String())
	}
	if ls := LossReport(d, "csv"); len(ls) != 0 {
		t.Errorf("CSV to CSV reports losses: %+v", ls)
	}
	c, changes, err := Clean(d, CleanOptions{FixSwapped: true})
	if err != nil || len(changes) == 0 {
		t.Fatalf("clean: %v %v", err, changes)
	}
	buf.Reset()
	WriteCSV(c, &buf)
	want := strings.Replace(src, "D;134,78;32,08;swapped", "D;32,08;134,78;swapped", 1)
	if buf.String() != want {
		t.Errorf("clean output:\n%q\nwant\n%q", buf.String(), want)
	}
}

// A header that splits into more columns on another delimiter still uses
// the one that finds the coordinates.
func TestCSVDelimiterFindsCoordinates(t *testing.T) {
	d, err := Open("x.csv", []byte("name,lat,lon\n\"a;b;c;d\",1.5,2.5\n"))
	if err != nil || !d.Readable() || len(d.Placemarks()) != 1 {
		t.Fatalf("open: %v", err)
	}
	// A tab file with points as decimals.
	d, err = Open("x.tsv", []byte("lat\tlon\tname\n1.25\t2.5\tp\n"))
	if err != nil || !d.Readable() {
		t.Fatalf("tsv: %v", err)
	}
	if c := d.Placemarks()[0].Geometry.Coords[0]; c.Lat != 1.25 || c.Lon != 2.5 {
		t.Errorf("tsv coords %+v", c)
	}
}
