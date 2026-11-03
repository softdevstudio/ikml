package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

const td = "../../testdata/"

func TestVersion(t *testing.T) {
	code, out, _ := runCLI("version")
	v, _ := os.ReadFile("../../VERSION")
	if code != 0 || out != "ikml "+strings.TrimSpace(string(v))+"\n" {
		t.Errorf("version: %d %q", code, out)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		code int
	}{
		{[]string{}, 2},
		{[]string{"nope"}, 2},
		{[]string{"check"}, 2},
		{[]string{"check", "a", "b"}, 2},
		{[]string{"check", td + "missing.kml"}, 2},
		{[]string{"check", td + "good/earth-pro-export.kml", "--bogus"}, 2},
		{[]string{"check", td + "good"}, 0},
		{[]string{"check", td + "bad"}, 1},
		{[]string{"inspect", td + "good"}, 2},
		{[]string{"check", td + "good/qgis-export.kml"}, 0},
		{[]string{"check", td + "good/qgis-export.kml", "--strict"}, 0},
		{[]string{"check", td + "good/earth-pro-export.kml"}, 0},
		{[]string{"check", td + "good/earth-pro-export.kml", "--strict"}, 1},
		{[]string{"check", td + "bad/unclosed-ring.kml"}, 1},
		{[]string{"check", td + "bad/malformed.kml"}, 1},
		{[]string{"check", "--json", td + "bad/swapped-latlon.kml"}, 1},
		{[]string{"inspect", td + "good/mymaps-export.kml"}, 0},
		{[]string{"inspect", td + "bad/broken-style.kml"}, 1},
		{[]string{"convert", td + "good/mymaps-export.kml"}, 2},
		{[]string{"convert", td + "good/mymaps-export.kml", "out.txt"}, 2},
		{[]string{"help"}, 0},
		{[]string{"codes"}, 0},
	}
	for _, c := range cases {
		code, _, _ := runCLI(c.args...)
		if code != c.code {
			t.Errorf("%v: exit %d, want %d", c.args, code, c.code)
		}
	}
}

func TestCheckJSON(t *testing.T) {
	code, out, _ := runCLI("check", td+"bad/unclosed-ring.kml", "--json")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	var r struct {
		File     string `json:"file"`
		Format   string `json:"format"`
		Readable bool   `json:"readable"`
		Errors   int    `json:"errors"`
		Warnings int    `json:"warnings"`
		Issues   []struct {
			Code, Severity, Message, Location string
			Line, Count                       int
		} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if r.File != "unclosed-ring.kml" || r.Format != "kml" || !r.Readable || r.Errors != 3 || len(r.Issues) != 3 {
		t.Errorf("unexpected: %+v", r)
	}
	for _, is := range r.Issues {
		if is.Code == "" || is.Severity != "error" || is.Message == "" || is.Location == "" || is.Line == 0 || is.Count < 1 {
			t.Errorf("incomplete issue %+v", is)
		}
	}
}

func TestInspectJSON(t *testing.T) {
	_, out, _ := runCLI("inspect", "--json", td+"good/earth-pro-export.kml")
	var r map[string]any
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"file", "format", "size", "stats", "tree", "styles", "issues", "errors", "warnings", "infos"} {
		if _, ok := r[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
	stats := r["stats"].(map[string]any)
	if stats["placemarks"].(float64) != 4 {
		t.Errorf("placemarks %v", stats["placemarks"])
	}
}

func TestConvertAndClean(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.geojson")
	code, stdout, stderr := runCLI("convert", td+"good/mymaps-export.kml", out, "--report")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "What changes from kml to geojson") || !strings.Contains(stdout, "[KEPT] Folder structure") {
		t.Errorf("report missing:\n%s", stdout)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), `"type":"FeatureCollection"`) {
		t.Error("not GeoJSON")
	}
	// Back to KMZ, then KML.
	kmz := filepath.Join(dir, "back.kmz")
	if code, _, e := runCLI("convert", out, kmz); code != 0 {
		t.Fatalf("to kmz: %s", e)
	}
	if code, out, _ := runCLI("check", kmz); code != 0 {
		t.Errorf("converted kmz has errors:\n%s", out)
	}

	// Clean with JSON output.
	cl := filepath.Join(dir, "clean.kml")
	code, stdout, _ = runCLI("clean", td+"bad/broken-style.kml", cl, "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var res convertJSON
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 || res.Errors != 0 {
		t.Errorf("clean result %+v", res)
	}
	if code, _, _ := runCLI("check", cl); code != 0 {
		t.Error("cleaned file still has errors")
	}

	// Swapped needs the flag.
	sw := filepath.Join(dir, "sw.kml")
	runCLI("clean", td+"bad/swapped-latlon.kml", sw)
	if code, _, _ := runCLI("check", sw); code != 1 {
		t.Error("swap fixed without --fix-swapped")
	}
	runCLI("clean", td+"bad/swapped-latlon.kml", sw, "--fix-swapped")
	if code, _, _ := runCLI("check", sw); code != 0 {
		t.Error("swap not fixed with --fix-swapped")
	}

	// Unreadable input writes nothing.
	bad := filepath.Join(dir, "bad.geojson")
	if code, _, _ := runCLI("convert", td+"bad/malformed.kml", bad); code != 1 {
		t.Errorf("malformed convert exit %d", code)
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("output written for unreadable input")
	}
	// Same file in and out is refused.
	cp := filepath.Join(dir, "same.kml")
	data, _ := os.ReadFile(td + "good/qgis-export.kml")
	os.WriteFile(cp, data, 0o644)
	if code, _, _ := runCLI("clean", cp, cp); code != 2 {
		t.Error("same in/out accepted")
	}
}

func TestKMLToKMZBundlesFiles(t *testing.T) {
	dir := t.TempDir()
	kml := `<kml xmlns="http://www.opengis.net/kml/2.2"><Document><Style id="s"><IconStyle><Icon><href>icons/pin.png</href></Icon></IconStyle></Style><Placemark><styleUrl>#s</styleUrl><Point><coordinates>1,2</coordinates></Point></Placemark></Document></kml>`
	os.MkdirAll(filepath.Join(dir, "icons"), 0o755)
	os.WriteFile(filepath.Join(dir, "icons", "pin.png"), []byte("\x89PNG fake"), 0o644)
	os.WriteFile(filepath.Join(dir, "in.kml"), []byte(kml), 0o644)
	out := filepath.Join(dir, "out.kmz")
	if code, _, e := runCLI("convert", filepath.Join(dir, "in.kml"), out); code != 0 {
		t.Fatal(e)
	}
	code, stdout, _ := runCLI("check", out)
	if code != 0 || strings.Contains(stdout, "kmz-missing-asset") {
		t.Errorf("bundled kmz: %d\n%s", code, stdout)
	}
}

func TestCheckDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gpx, _ := os.ReadFile(td + "good/strava.gpx")
	write("maps/ride.gpx", string(gpx))
	write("maps/stops.csv", "name,lat,lon\nA,1,2\n")
	write("package.json", `{"name":"site","version":"1.0.0"}`)
	write("data/prices.csv", "item,price\napple,3\n")
	write("maps/broken.kml", "<kml><Placemark><Point><coordinates>200,95</coordinates></Point></Placemark></kml>")
	write(".git/ignored.kml", "<kml/>")
	write("notes.txt", "not a map")
	code, out, errs := runCLI("check", dir, "--json")
	if code != 1 {
		t.Errorf("exit %d (%s)", code, errs)
	}
	var r batchJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.Totals.Files != 3 || r.Totals.Skipped != 2 || r.Totals.Errors != 2 {
		t.Errorf("totals %+v", r.Totals)
	}
	for _, f := range r.Files {
		if !strings.HasPrefix(f.File, filepath.ToSlash(dir)+"/maps/") {
			t.Errorf("unexpected file %s", f.File)
		}
		if strings.HasSuffix(f.File, "broken.kml") && (f.Errors != 2 || len(f.Issues) == 0 || f.Issues[0].Line == 0) {
			t.Errorf("broken.kml: %+v", f)
		}
	}
	os.Remove(filepath.Join(dir, "maps/broken.kml"))
	if code, out, _ := runCLI("check", dir); code != 0 || !strings.Contains(out, "2 file(s) checked, 2 skipped") {
		t.Errorf("text mode: %d\n%s", code, out)
	}
	write("maps/late.gpx", `<gpx version="1.1"><trk><trkseg><trkpt lat="1" lon="2"><time>2026-01-01T10:00:00Z</time></trkpt><trkpt lat="1" lon="2.1"><time>2026-01-01T09:00:00Z</time></trkpt></trkseg></trk></gpx>`)
	if code, _, _ := runCLI("check", dir); code != 0 {
		t.Errorf("warnings alone fail without --strict: %d", code)
	}
	if code, _, _ := runCLI("check", dir, "--strict"); code != 1 {
		t.Errorf("--strict with warnings: %d", code)
	}
}

// GeoJSON with a byte order mark and broken JSON are checked, not skipped;
// an unreadable folder is reported and the walk goes on; a symlinked folder
// argument is followed; an empty folder says so.
func TestCheckDirEdges(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/bom.json", "\xEF\xBB\xBF"+`{"type":"FeatureCollection","features":[]}`)
	write("a/broken.geojson.json", `{"type":"FeatureCollection","features":[`)
	write("a/list.json", `[1,2,3]`)
	code, out, errs := runCLI("check", dir, "--json")
	var r batchJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.Totals.Files != 2 || r.Totals.Skipped != 1 || code != 1 {
		t.Errorf("exit %d, totals %+v (%s)", code, r.Totals, errs)
	}
	if os.Getuid() != 0 {
		write("locked/x.kml", "<kml/>")
		os.Chmod(filepath.Join(dir, "locked"), 0)
		defer os.Chmod(filepath.Join(dir, "locked"), 0o755)
		code, out, _ = runCLI("check", dir, "--json")
		json.Unmarshal([]byte(out), &r)
		if code != 1 || r.Totals.Unreadable < 2 || r.Totals.Files != 3 {
			t.Errorf("unreadable folder: exit %d, totals %+v", code, r.Totals)
		}
		os.Chmod(filepath.Join(dir, "locked"), 0o755)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(dir, "a"), link); err == nil {
		_, out, _ = runCLI("check", link, "--json")
		json.Unmarshal([]byte(out), &r)
		if r.Totals.Files != 2 || !strings.HasPrefix(r.Files[0].File, filepath.ToSlash(link)+"/") {
			t.Errorf("symlinked folder: %+v", r)
		}
	}
	empty := t.TempDir()
	if code, _, errs := runCLI("check", empty); code != 0 || !strings.Contains(errs, "no map files") {
		t.Errorf("empty folder: %d %q", code, errs)
	}
}

func TestConvertGPXAndCSV(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "ride.geojson")
	code, stdout, errs := runCLI("convert", td+"good/garmin-connect.gpx", out, "--report")
	if code != 0 || !strings.Contains(stdout, "Track point extensions") || !strings.Contains(stdout, "hr (40)") {
		t.Fatalf("convert: %d %s %s", code, stdout, errs)
	}
	back := filepath.Join(dir, "ride.gpx")
	if code, _, errs := runCLI("convert", out, back); code != 0 {
		t.Fatalf("back to GPX: %d %s", code, errs)
	}
	if code, _, _ := runCLI("check", back); code != 0 {
		t.Errorf("the GPX written back has errors")
	}
	csv := filepath.Join(dir, "stops.csv")
	if code, stdout, errs := runCLI("convert", td+"good/hike-plan.gpx", csv, "--report"); code != 0 || !strings.Contains(stdout, "Routes and tracks") {
		t.Fatalf("to CSV: %d %s %s", code, stdout, errs)
	}
	b, _ := os.ReadFile(csv)
	if !strings.HasPrefix(string(b), "name,lat,lon,ele,time,description,cmt,link,sym\n") {
		t.Errorf("CSV:\n%s", b)
	}
}
