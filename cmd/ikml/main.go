// Command ikml shows what is inside a KML, KMZ or GeoJSON file, flags
// what is broken, explains what a conversion will lose, and writes
// cleaned copies.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/softdevstudio/ikml"
)

// Version is the release version. scripts/check-version.sh checks that it
// matches the VERSION file and CHANGELOG.md.
const Version = "0.1.1"

// Exit codes.
const (
	exitOK     = 0
	exitIssues = 1
	exitUsage  = 2
)

// MaxInput is the largest file the CLI reads.
const MaxInput = 1 << 30

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `ikml: understand a geographic file before importing it.

Usage:
  ikml inspect FILE [--json]            show what is inside the file
  ikml check FILE [--json] [--strict]   list problems; exit 1 if errors found
  ikml convert IN OUT [--report] [--json]
                                        convert by OUT extension (.geojson, .json, .kml, .kmz)
  ikml clean IN OUT [--fix-swapped] [--drop-unused] [--report] [--json]
                                        apply safe fixes and write a cleaned copy
  ikml codes [--json]                   list every issue code
  ikml version                          print the version

Exit codes: 0 no errors, 1 errors found (or warnings with --strict),
2 usage or read error. FILE and IN can be - for standard input.
`

type opts struct {
	json, strict, report, fixSwapped, dropUnused bool
	args                                         []string
}

func parse(args []string, allowed ...string) (opts, error) {
	var o opts
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, a := range args {
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			if !ok[name] {
				return o, fmt.Errorf("unknown flag %s", a)
			}
			switch name {
			case "json":
				o.json = true
			case "strict":
				o.strict = true
			case "report":
				o.report = true
			case "fix-swapped":
				o.fixSwapped = true
			case "drop-unused":
				o.dropUnused = true
			}
			continue
		}
		o.args = append(o.args, a)
	}
	return o, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "ikml: "+format+"\n", a...)
		return exitUsage
	}
	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "ikml "+Version)
		return exitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return exitOK
	case "codes":
		o, err := parse(rest, "json")
		if err != nil {
			return fail("%v", err)
		}
		return codes(o, stdout)
	case "inspect", "check":
		o, err := parse(rest, "json", "strict")
		if err != nil {
			return fail("%v", err)
		}
		if len(o.args) != 1 {
			return fail("%s needs exactly one FILE\n\n%s", cmd, usage)
		}
		d, err := load(o.args[0])
		if err != nil {
			return fail("%v", err)
		}
		r := ikml.Inspect(d)
		code := exitOK
		if r.Errors > 0 || (o.strict && r.Warnings > 0) {
			code = exitIssues
		}
		if cmd == "inspect" {
			if o.json {
				writeJSON(stdout, r)
			} else {
				printInspect(stdout, r)
			}
			return code
		}
		if o.json {
			writeJSON(stdout, checkJSON{File: r.File, Format: r.Format, Readable: r.Readable, Errors: r.Errors, Warnings: r.Warnings, Infos: r.Infos, Issues: r.Issues})
		} else {
			printCheck(stdout, r)
		}
		return code
	case "convert", "clean":
		allowed := []string{"report", "json"}
		if cmd == "clean" {
			allowed = append(allowed, "fix-swapped", "drop-unused")
		}
		o, err := parse(rest, allowed...)
		if err != nil {
			return fail("%v", err)
		}
		if len(o.args) != 2 {
			return fail("%s needs IN and OUT\n\n%s", cmd, usage)
		}
		return convert(cmd, o, stdout, stderr)
	}
	return fail("unknown command %q\n\n%s", cmd, usage)
}

func load(name string) (*ikml.Doc, error) {
	var data []byte
	var err error
	if name == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, MaxInput+1))
		name = "stdin"
	} else {
		var f *os.File
		f, err = os.Open(name)
		if err == nil {
			defer f.Close()
			if st, e := f.Stat(); e == nil && st.IsDir() {
				return nil, fmt.Errorf("%s is a directory", name)
			}
			data, err = io.ReadAll(io.LimitReader(f, MaxInput+1))
		}
	}
	if err != nil {
		return nil, err
	}
	if len(data) > MaxInput {
		return nil, fmt.Errorf("%s is larger than 1 GiB", name)
	}
	d, err := ikml.Open(filepath.Base(name), data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return d, nil
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

type checkJSON struct {
	File     string       `json:"file"`
	Format   ikml.Format  `json:"format"`
	Readable bool         `json:"readable"`
	Errors   int          `json:"errors"`
	Warnings int          `json:"warnings"`
	Infos    int          `json:"infos"`
	Issues   []ikml.Issue `json:"issues"`
}

type convertJSON struct {
	Input    string        `json:"input"`
	Output   string        `json:"output"`
	Format   string        `json:"format"`
	Bytes    int           `json:"bytes"`
	Changes  []ikml.Change `json:"changes,omitempty"`
	Losses   []ikml.Loss   `json:"losses"`
	Errors   int           `json:"errorsAfter"`
	Warnings int           `json:"warningsAfter"`
}

func targetOf(out string) (string, error) {
	switch strings.ToLower(filepath.Ext(out)) {
	case ".geojson", ".json":
		return "geojson", nil
	case ".kml":
		return "kml", nil
	case ".kmz":
		return "kmz", nil
	}
	return "", fmt.Errorf("cannot tell the output format from %q; use .geojson, .json, .kml or .kmz", out)
}

func convert(cmd string, o opts, stdout, stderr io.Writer) int {
	in, out := o.args[0], o.args[1]
	target, err := targetOf(out)
	if out == "-" {
		return func() int {
			fmt.Fprintln(stderr, "ikml: OUT - needs a format; write to a file name such as out.geojson")
			return exitUsage
		}()
	}
	if err != nil {
		fmt.Fprintf(stderr, "ikml: %v\n", err)
		return exitUsage
	}
	if same(in, out) {
		fmt.Fprintln(stderr, "ikml: IN and OUT are the same file; write to a new file")
		return exitUsage
	}
	d, err := load(in)
	if err != nil {
		fmt.Fprintf(stderr, "ikml: %v\n", err)
		return exitUsage
	}
	if !d.Readable() {
		r := ikml.Inspect(d)
		fmt.Fprintf(stderr, "ikml: %s could not be read completely, so nothing was written.\n", in)
		for _, is := range r.Issues {
			if is.Severity == ikml.SevError {
				fmt.Fprintf(stderr, "  %s: %s\n", is.Code, is.Message)
			}
		}
		return exitIssues
	}
	var changes []ikml.Change
	src := d
	if cmd == "clean" {
		d, changes, err = ikml.Clean(d, ikml.CleanOptions{FixSwapped: o.fixSwapped, DropUnusedAssets: o.dropUnused})
		if err != nil {
			fmt.Fprintf(stderr, "ikml: %v\n", err)
			return exitIssues
		}
	}
	losses := ikml.LossReport(d, target)
	var buf bytes.Buffer
	switch target {
	case "geojson":
		err = ikml.WriteGeoJSON(d, &buf)
	case "kml":
		err = ikml.WriteKML(d, &buf)
	case "kmz":
		err = ikml.WriteKMZ(d, &buf, &ikml.KMZOptions{Extra: bundle(d, in)})
	}
	if err != nil {
		fmt.Fprintf(stderr, "ikml: %v\n", err)
		return exitIssues
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(stderr, "ikml: %v\n", err)
		return exitUsage
	}
	// Re-check the written file so the summary tells the truth about it.
	after, err := ikml.Open(filepath.Base(out), buf.Bytes())
	var ar ikml.Report
	if err == nil {
		ar = ikml.Inspect(after)
	}
	if o.json {
		writeJSON(stdout, convertJSON{Input: in, Output: out, Format: target, Bytes: buf.Len(), Changes: changes, Losses: losses, Errors: ar.Errors, Warnings: ar.Warnings})
		return exitOK
	}
	br := ikml.Inspect(src)
	verb := "Converted"
	if cmd == "clean" {
		verb = "Cleaned"
	}
	fmt.Fprintf(stdout, "%s %s (%s) to %s (%s, %s).\n", verb, in, src.Format, out, target, human(int64(buf.Len())))
	if cmd == "clean" {
		if len(changes) == 0 {
			fmt.Fprintln(stdout, "No safe fixes were needed.")
		} else {
			fmt.Fprintf(stdout, "\nChanges (%d):\n", len(changes))
			for _, c := range changes {
				loc := locString(c.Location, c.Line)
				cnt := ""
				if c.Count > 1 {
					cnt = fmt.Sprintf(" (x%d)", c.Count)
				}
				if loc != "" {
					fmt.Fprintf(stdout, "  %-18s %s%s\n  %-18s at %s\n", c.Code, c.Message, cnt, "", loc)
				} else {
					fmt.Fprintf(stdout, "  %-18s %s%s\n", c.Code, c.Message, cnt)
				}
			}
		}
	}
	if o.report || cmd == "convert" {
		printLosses(stdout, losses, src.Format, target, o.report)
	}
	fmt.Fprintf(stdout, "\nBefore: %d error(s), %d warning(s). After: %d error(s), %d warning(s).\n", br.Errors, br.Warnings, ar.Errors, ar.Warnings)
	return exitOK
}

func same(a, b string) bool {
	if a == "-" || b == "-" {
		return false
	}
	sa, ea := os.Stat(a)
	sb, eb := os.Stat(b)
	if ea != nil || eb != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// bundle reads files a plain .kml refers to by relative path, so that
// writing a .kmz from a .kml keeps its icons and images.
func bundle(d *ikml.Doc, in string) map[string][]byte {
	if d.Format != ikml.FormatKML || in == "-" {
		return nil
	}
	dir := filepath.Dir(in)
	out := map[string][]byte{}
	var total int64
	for _, r := range d.Refs {
		h := strings.ReplaceAll(r.Href, `\`, "/")
		if h == "" || strings.Contains(h, "://") || strings.HasPrefix(h, "/") || strings.HasPrefix(h, "#") || strings.Contains(h, "..") || strings.Contains(h, ":") {
			continue
		}
		if _, done := out[h]; done {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(h)))
		if err != nil || total+int64(len(b)) > ikml.MaxUncompressed {
			continue
		}
		total += int64(len(b))
		out[h] = b
	}
	return out
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

func locString(path string, line int) string {
	switch {
	case path != "" && line > 0:
		return fmt.Sprintf("%s (line %d)", path, line)
	case line > 0:
		return fmt.Sprintf("line %d", line)
	}
	return path
}

func printLosses(w io.Writer, ls []ikml.Loss, from ikml.Format, to string, detail bool) {
	if len(ls) == 0 {
		fmt.Fprintf(w, "\nNothing is lost converting %s to %s.\n", from, to)
		return
	}
	lost, changed, kept := 0, 0, 0
	for _, l := range ls {
		switch l.Outcome {
		case "lost":
			lost++
		case "changed":
			changed++
		default:
			kept++
		}
	}
	if !detail {
		fmt.Fprintf(w, "\nConversion notes: %d thing(s) lost, %d changed, %d kept in another form. Add --report for details.\n", lost, changed, kept)
		return
	}
	fmt.Fprintf(w, "\nWhat changes from %s to %s:\n", from, to)
	for _, l := range ls {
		fmt.Fprintf(w, "\n  [%s] %s (%d)\n", strings.ToUpper(l.Outcome), l.What, l.Count)
		for _, line := range wrap(l.Detail, 72) {
			fmt.Fprintf(w, "    %s\n", line)
		}
		for _, it := range l.Items {
			fmt.Fprintf(w, "      - %s\n", it)
		}
	}
}

func wrap(s string, n int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && len(line)+1+len(word) > n {
			out = append(out, line)
			line = word
			continue
		}
		if line == "" {
			line = word
		} else {
			line += " " + word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func printIssues(w io.Writer, issues []ikml.Issue) {
	for _, is := range issues {
		loc := locString(is.Location, is.Line)
		label := strings.ToUpper(string(is.Severity))
		fmt.Fprintf(w, "  %-7s %s", label, is.Code)
		if loc != "" {
			fmt.Fprintf(w, "  %s", loc)
		}
		fmt.Fprintln(w)
		msg := is.Message
		if is.Count > 1 && !strings.Contains(msg, strconv.Itoa(is.Count)) {
			msg += fmt.Sprintf(" (%d times)", is.Count)
		}
		for _, line := range wrap(msg, 72) {
			fmt.Fprintf(w, "          %s\n", line)
		}
	}
}

func summary(r ikml.Report) string {
	return fmt.Sprintf("%d error(s), %d warning(s), %d note(s).", r.Errors, r.Warnings, r.Infos)
}

func printCheck(w io.Writer, r ikml.Report) {
	fmt.Fprintf(w, "%s (%s, %s)\n", r.File, r.Format, human(int64(r.Size)))
	if !r.Readable {
		fmt.Fprintln(w, "The file could not be read completely.")
	}
	if len(r.Issues) == 0 {
		fmt.Fprintln(w, "No problems found.")
		return
	}
	fmt.Fprintln(w)
	printIssues(w, r.Issues)
	fmt.Fprintf(w, "\n%s\n", summary(r))
}

func geomList(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", m[k], k))
	}
	return strings.Join(parts, ", ")
}

func printInspect(w io.Writer, r ikml.Report) {
	s := r.Stats
	fmt.Fprintf(w, "%s\n", r.File)
	fmt.Fprintf(w, "  Format:       %s, %s\n", strings.ToUpper(string(r.Format)), human(int64(r.Size)))
	if r.Namespace != "" {
		fmt.Fprintf(w, "  Namespace:    %s\n", r.Namespace)
	}
	if r.MainPath != "" {
		fmt.Fprintf(w, "  Main file:    %s\n", r.MainPath)
	}
	if r.Encoding != "" {
		fmt.Fprintf(w, "  Encoding:     %s (converted to UTF-8)\n", r.Encoding)
	}
	if !r.Readable {
		fmt.Fprintln(w, "  Readable:     no, see the errors below")
	}
	fmt.Fprintf(w, "  Placemarks:   %d", s.Placemarks)
	if s.Empty > 0 {
		fmt.Fprintf(w, " (%d without geometry)", s.Empty)
	}
	fmt.Fprintln(w)
	if len(s.Geometries) > 0 {
		fmt.Fprintf(w, "  Geometry:     %s\n", geomList(s.Geometries))
	}
	fmt.Fprintf(w, "  Coordinates:  %d\n", s.Coordinates)
	if s.BBox != nil {
		fmt.Fprintf(w, "  Extent:       lon %s to %s, lat %s to %s\n", num(s.BBox[0]), num(s.BBox[2]), num(s.BBox[1]), num(s.BBox[3]))
	}
	if s.Folders > 0 {
		fmt.Fprintf(w, "  Folders:      %d\n", s.Folders)
	}
	if s.Styles+s.StyleMaps > 0 {
		fmt.Fprintf(w, "  Styles:       %d Style, %d StyleMap\n", s.Styles, s.StyleMaps)
	}
	if s.Schemas > 0 {
		fmt.Fprintf(w, "  Schemas:      %d\n", s.Schemas)
	}
	var extras []string
	if s.Tracks > 0 {
		extras = append(extras, fmt.Sprintf("%d GPS track(s)", s.Tracks))
	}
	if s.Timestamped > 0 {
		extras = append(extras, fmt.Sprintf("%d timestamped placemark(s)", s.Timestamped))
	}
	if s.NetworkLinks > 0 {
		extras = append(extras, fmt.Sprintf("%d network link(s)", s.NetworkLinks))
	}
	if s.Overlays > 0 {
		extras = append(extras, fmt.Sprintf("%d overlay(s)", s.Overlays))
	}
	if s.Models > 0 {
		extras = append(extras, fmt.Sprintf("%d 3D model(s)", s.Models))
	}
	if s.Views > 0 {
		extras = append(extras, fmt.Sprintf("%d saved view(s)", s.Views))
	}
	if len(extras) > 0 {
		fmt.Fprintf(w, "  Also:         %s\n", strings.Join(extras, ", "))
	}

	if len(r.Tree) > 1 || (len(r.Tree) == 1 && r.Tree[0].Placemarks > 0 && r.Format != ikml.FormatGeoJSON) {
		fmt.Fprintln(w, "\nStructure:")
		const maxRows = 60
		for i, n := range r.Tree {
			if i == 0 && n.Placemarks == 0 && n.Other == 0 {
				continue
			}
			if i >= maxRows {
				fmt.Fprintf(w, "  ... and %d more folders\n", len(r.Tree)-maxRows)
				break
			}
			name := n.Name
			if name == "" {
				name = "(no name)"
			}
			if i == 0 {
				name = "(top level)"
			}
			line := fmt.Sprintf("%s%s %s", strings.Repeat("  ", n.Depth), n.Kind, name)
			if i == 0 {
				line = "(top level)"
			}
			var bits []string
			if n.Placemarks > 0 {
				bits = append(bits, fmt.Sprintf("%d placemark(s)", n.Placemarks))
			}
			if len(n.Geometries) > 0 {
				bits = append(bits, geomList(n.Geometries))
			}
			if n.Other > 0 {
				bits = append(bits, fmt.Sprintf("%d link/overlay(s)", n.Other))
			}
			if len(bits) > 0 {
				line += ": " + strings.Join(bits, "; ")
			}
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
	if len(r.Fields) > 0 {
		fmt.Fprintln(w, "\nAttribute fields:")
		for i, f := range r.Fields {
			if i >= 40 {
				fmt.Fprintf(w, "  ... and %d more\n", len(r.Fields)-40)
				break
			}
			t := ""
			if f.Type != "" {
				t = ", " + f.Type
			}
			fmt.Fprintf(w, "  %-24s %d placemark(s) (%s%s)\n", f.Name, f.Count, f.Source, t)
		}
	}
	if len(r.Styles) > 0 {
		fmt.Fprintln(w, "\nStyles:")
		for i, st := range r.Styles {
			if i >= 30 {
				fmt.Fprintf(w, "  ... and %d more\n", len(r.Styles)-30)
				break
			}
			fmt.Fprintf(w, "  %-8s #%-20s used by %d: %s\n", st.Kind, st.ID, st.Uses, st.Parts)
		}
	}
	if len(r.Assets) > 0 {
		fmt.Fprintln(w, "\nFiles inside the KMZ:")
		for _, a := range r.Assets {
			used := "used"
			if !a.Used {
				used = "not referenced"
			}
			fmt.Fprintf(w, "  %-36s %10s  %s\n", a.Path, human(a.Size), used)
		}
	}
	if len(r.NetworkLinks) > 0 {
		fmt.Fprintln(w, "\nNetwork links:")
		for _, nl := range r.NetworkLinks {
			fmt.Fprintf(w, "  %s\n", nl.Href)
		}
	}
	fmt.Fprintln(w)
	if len(r.Issues) == 0 {
		fmt.Fprintln(w, "No problems found.")
		return
	}
	fmt.Fprintln(w, "Issues:")
	printIssues(w, r.Issues)
	fmt.Fprintf(w, "\n%s\n", summary(r))
}

func num(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func codes(o opts, w io.Writer) int {
	if o.json {
		type c struct {
			Code     string        `json:"code"`
			Severity ikml.Severity `json:"severity"`
			Meaning  string        `json:"meaning"`
			Clean    string        `json:"clean,omitempty"`
		}
		var out []c
		for _, x := range ikml.Codes {
			out = append(out, c{x.Code, x.Severity, x.Meaning, x.Fix})
		}
		writeJSON(w, out)
		return exitOK
	}
	for _, x := range ikml.Codes {
		fmt.Fprintf(w, "%-8s %s\n", x.Severity, x.Code)
	}
	return exitOK
}
