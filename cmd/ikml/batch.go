package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/softdevstudio/ikml"
)

// MaxBatchFiles is the most files check DIR reads.
const MaxBatchFiles = 10000

// batchExts are the file name extensions check DIR looks at.
var batchExts = map[string]bool{".kml": true, ".kmz": true, ".geojson": true, ".json": true, ".gpx": true, ".csv": true}

var geojsonTypes = map[string]bool{"FeatureCollection": true, "Feature": true, "Point": true, "MultiPoint": true,
	"LineString": true, "MultiLineString": true, "Polygon": true, "MultiPolygon": true, "GeometryCollection": true}

type batchFile struct {
	File     string       `json:"file"`
	Format   ikml.Format  `json:"format,omitempty"`
	Readable bool         `json:"readable"`
	Errors   int          `json:"errors"`
	Warnings int          `json:"warnings"`
	Infos    int          `json:"infos"`
	Issues   []ikml.Issue `json:"issues"`
	Error    string       `json:"error,omitempty"`
}

type skipped struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type batchTotals struct {
	Files      int `json:"files"`
	Skipped    int `json:"skipped"`
	Unreadable int `json:"unreadable"`
	Errors     int `json:"errors"`
	Warnings   int `json:"warnings"`
	Infos      int `json:"infos"`
}

type batchJSON struct {
	Files   []batchFile `json:"files"`
	Skipped []skipped   `json:"skipped"`
	Totals  batchTotals `json:"totals"`
}

// skipReason says why a .json or .csv file in a folder isn't a map file:
// a JSON file whose top-level type isn't a GeoJSON type, or a CSV file
// without latitude and longitude columns. Those are common in repositories
// (package.json, data tables) and aren't checked.
func skipReason(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		var top struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &top) != nil || !geojsonTypes[top.Type] {
			return "a JSON file that isn't GeoJSON"
		}
	case ".csv":
		d, err := ikml.Open(filepath.Base(path), data)
		if err == nil && !d.Readable() {
			for _, is := range ikml.Inspect(d).Issues {
				if is.Code == "csv-no-coordinates" {
					return "a CSV file without latitude and longitude columns"
				}
			}
		}
	}
	return ""
}

// checkDir checks every map file under dir. Symbolic links are not followed.
func checkDir(dir string, o opts, stdout, stderr io.Writer) int {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if p != dir && strings.HasPrefix(e.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !e.Type().IsRegular() || !batchExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		paths = append(paths, p)
		if len(paths) > MaxBatchFiles {
			return fmt.Errorf("more than %d map files; check a smaller folder", MaxBatchFiles)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "ikml: %v\n", err)
		return exitUsage
	}
	sort.Strings(paths)
	out := batchJSON{Files: []batchFile{}, Skipped: []skipped{}}
	for _, p := range paths {
		name := filepath.ToSlash(p)
		data, err := readLimited(p)
		if err != nil {
			out.Files = append(out.Files, batchFile{File: name, Error: err.Error(), Issues: []ikml.Issue{}})
			out.Totals.Unreadable++
			continue
		}
		if why := skipReason(p, data); why != "" {
			out.Skipped = append(out.Skipped, skipped{name, why})
			continue
		}
		d, err := ikml.Open(filepath.Base(p), data)
		if err != nil {
			out.Files = append(out.Files, batchFile{File: name, Error: err.Error(), Issues: []ikml.Issue{}})
			out.Totals.Unreadable++
			continue
		}
		r := ikml.Inspect(d)
		f := batchFile{File: name, Format: r.Format, Readable: r.Readable, Errors: r.Errors, Warnings: r.Warnings, Infos: r.Infos, Issues: r.Issues}
		if !r.Readable {
			out.Totals.Unreadable++
		}
		out.Totals.Errors += r.Errors
		out.Totals.Warnings += r.Warnings
		out.Totals.Infos += r.Infos
		out.Files = append(out.Files, f)
	}
	out.Totals.Files = len(out.Files)
	out.Totals.Skipped = len(out.Skipped)
	failed := out.Totals.Errors > 0 || out.Totals.Unreadable > 0 || (o.strict && out.Totals.Warnings > 0)
	if o.json {
		writeJSON(stdout, out)
	} else {
		for _, f := range out.Files {
			switch {
			case f.Error != "":
				fmt.Fprintf(stdout, "%s: %s\n", f.File, f.Error)
			case f.Errors+f.Warnings == 0:
				fmt.Fprintf(stdout, "%s: OK (%s)\n", f.File, f.Format)
			default:
				fmt.Fprintf(stdout, "%s: %d error(s), %d warning(s) (%s)\n", f.File, f.Errors, f.Warnings, f.Format)
				var serious []ikml.Issue
				for _, is := range f.Issues {
					if is.Severity != ikml.SevInfo {
						serious = append(serious, is)
					}
				}
				printIssues(stdout, serious)
			}
		}
		for _, s := range out.Skipped {
			fmt.Fprintf(stdout, "%s: skipped, %s\n", s.File, s.Reason)
		}
		t := out.Totals
		fmt.Fprintf(stdout, "\n%d file(s) checked, %d skipped: %d error(s), %d warning(s), %d unreadable.\n", t.Files, t.Skipped, t.Errors, t.Warnings, t.Unreadable)
	}
	if failed {
		return exitIssues
	}
	return exitOK
}

func readLimited(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxInput+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxInput {
		return nil, fmt.Errorf("larger than 1 GiB")
	}
	return data, nil
}
