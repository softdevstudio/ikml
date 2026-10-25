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
		{[]string{"check", td + "good"}, 2},
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
