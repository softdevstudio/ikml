package ikml

import (
	"fmt"
	"sort"
)

// Severity of an issue.
type Severity string

// Severities, from worst to mildest.
const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

// Issue is one finding about the file. Count says how many times it
// happens at Location (for example, how many bad tuples in one line).
type Issue struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Location string   `json:"location,omitempty"`
	Line     int      `json:"line,omitempty"`
	Count    int      `json:"count"`
}

// CodeInfo describes one issue code. The table is the source for
// REFERENCE.md, and the codes are stable across versions.
type CodeInfo struct {
	Code     string
	Severity Severity
	Meaning  string
	Fix      string // how clean handles it ("" means clean does not fix it)
}

// Codes lists every issue code iKML can report.
var Codes = []CodeInfo{
	// Errors
	{"xml-invalid", SevError, "The KML is not well-formed XML. Most programs refuse the whole file. iKML reports the line and keeps what it could read before the problem.", ""},
	{"json-invalid", SevError, "The GeoJSON is not valid JSON. iKML reports the line of the first problem.", ""},
	{"geojson-structure", SevError, "The JSON is valid but does not follow GeoJSON rules, for example a missing \"type\" or a position that is not an array of numbers.", ""},
	{"kmz-invalid", SevError, "The file starts like a zip archive but cannot be read as one.", ""},
	{"kmz-no-kml", SevError, "The KMZ archive has no .kml file inside, so there is nothing to show.", ""},
	{"kmz-too-large", SevError, "The KMZ expands past iKML's safety limit (512 MiB in total, or an entry that compresses far better than real data does). iKML stops reading to protect your machine.", ""},
	{"coord-parse", SevError, "A coordinate tuple could not be read as two or three numbers. Viewers usually drop the point or the whole shape.", "Not fixed. The tuple is left as it is in KML output and dropped from GeoJSON output."},
	{"lon-range", SevError, "A longitude is outside -180 to 180.", ""},
	{"lat-range", SevError, "A latitude is outside -90 to 90.", ""},
	{"swapped-latlon", SevError, "Every point of a placemark has the first value inside ±90 and the second outside ±90. That can only happen when latitude and longitude were written in the wrong order.", "Fixed only with --fix-swapped."},
	{"ring-unclosed", SevError, "A polygon ring does not end on its first point. KML and GeoJSON both require closed rings.", "Fixed: the first point is appended."},
	{"ring-too-short", SevError, "A polygon ring has fewer than 4 positions (3 corners plus the closing point), so it has no area.", ""},
	{"line-too-short", SevError, "A line has fewer than 2 positions.", ""},
	{"polygon-no-outer", SevError, "A Polygon has no outer boundary.", ""},
	{"kmz-missing-asset", SevError, "The document refers to a file (icon, image, overlay, model) that should be inside the KMZ but is not there.", ""},
	{"local-path-ref", SevError, "A reference points to a file on the creator's computer (file:///, C:\\, /Users/, /home/ and similar). Nobody else can open it.", ""},
	{"style-missing", SevError, "A placemark uses a style (#id) that is not defined in the document. Viewers fall back to default styling.", "Fixed: the broken styleUrl is removed."},
	// Warnings
	{"null-island", SevWarning, "A position is exactly 0,0, in the Gulf of Guinea. This is usually a missing value written as zero.", ""},
	{"duplicate-points", SevWarning, "A line or ring repeats the same position twice in a row.", "Fixed: repeats are removed, as long as the shape keeps enough points. GPS tracks are left alone."},
	{"coord-spacing", SevWarning, "Coordinates have spaces next to the commas (\"34.8, 32.1\"). KML separates points with spaces, so some programs read each number as its own point.", "Fixed: the coordinates are rewritten without the extra spaces."},
	{"empty-placemark", SevWarning, "A placemark or feature has no geometry, so it will not show on a map.", "Kept and reported."},
	{"http-ref", SevWarning, "A reference uses plain http://. Browsers and some viewers block it, and the content is not inside the file.", ""},
	{"mixed-dimensions", SevWarning, "One shape mixes 2D (lon,lat) and 3D (lon,lat,alt) positions.", ""},
	{"geojson-crs", SevWarning, "The GeoJSON has a \"crs\" member. RFC 7946 removed it, and most tools ignore it and assume WGS 84 degrees.", "Fixed when the crs names WGS 84 (CRS84 or EPSG:4326): the member is removed. Other systems are left, because the coordinates would need reprojecting."},
	{"geojson-nested-collection", SevWarning, "A GeometryCollection contains another GeometryCollection. RFC 7946 says not to do this and many tools reject it.", ""},
	{"old-namespace", SevWarning, "The file uses the KML 2.0 or 2.1 namespace. Newer tools expect 2.2.", "Fixed: the namespace is changed to KML 2.2."},
	{"no-kml-root", SevWarning, "The XML root is a Document, Folder or Placemark without the <kml> wrapper.", ""},
	{"altitude-ignored", SevWarning, "Positions have non-zero altitudes but the altitude mode is clampToGround (the default). Most viewers will draw them on the ground.", ""},
	{"large-coordinate-count", SevWarning, "One placemark has more than 100,000 positions. Many web maps slow down or refuse shapes this large.", ""},
	{"track-times-mismatch", SevWarning, "A gx:Track has a different number of <when> times and <gx:coord> positions. KML requires one time per position.", ""},
	{"duplicate-ids", SevWarning, "Two elements share the same id. Style references and feature lookups may pick the wrong one.", ""},
	// Info
	{"no-namespace", SevInfo, "The <kml> root has no namespace. Most programs read it anyway.", "Fixed: the KML 2.2 namespace is added."},
	{"remote-ref", SevInfo, "A reference points to an https:// address. It works online but the content is not inside the file.", ""},
	{"external-file-ref", SevInfo, "A plain .kml file refers to a file next to it on disk (for example an icon). That file must travel with the .kml.", ""},
	{"network-link", SevInfo, "The file contains a NetworkLink. Its contents load from elsewhere and are not part of this file.", ""},
	{"unused-asset", SevInfo, "A file inside the KMZ is not referenced by the document.", "Removed only with --drop-unused."},
	{"unknown-element", SevInfo, "An element iKML does not interpret, such as a vendor extension. It is kept in KML output and dropped from GeoJSON output.", ""},
	{"winding", SevInfo, "A GeoJSON ring winds the opposite way from RFC 7946 (outer rings counterclockwise, holes clockwise). Most tools accept either.", "Fixed in GeoJSON output: rings are rewound."},
	{"foreign-members", SevInfo, "The GeoJSON has members outside the specification. They are kept in GeoJSON output and dropped from KML output.", ""},
	{"overlay-present", SevInfo, "The file contains image overlays (GroundOverlay, ScreenOverlay or PhotoOverlay).", ""},
	{"model-present", SevInfo, "The file contains 3D models.", ""},
	{"has-tracks", SevInfo, "The file contains gx:Track GPS tracks with a time for each point.", ""},
	{"has-timestamps", SevInfo, "Placemarks carry TimeStamp or TimeSpan values.", ""},
	{"empty-folder", SevInfo, "A Folder or Document has nothing inside.", ""},
	{"encoding-converted", SevInfo, "The file is not UTF-8. iKML converted it, and cleaned output is written as UTF-8.", ""},
}

var codeIndex = func() map[string]CodeInfo {
	m := map[string]CodeInfo{}
	for _, c := range Codes {
		m[c.Code] = c
	}
	return m
}()

// LookupCode returns the description of an issue code.
func LookupCode(code string) (CodeInfo, bool) {
	c, ok := codeIndex[code]
	return c, ok
}

const maxIssuesPerCode = 25

type collector struct {
	list []Issue
	idx  map[string]int // code|location -> index
}

func newCollector() *collector { return &collector{idx: map[string]int{}} }

// add records an issue. Repeats at the same location add to its count.
// It returns the index of the issue.
func (c *collector) add(code string, loc Loc, n int, format string, args ...any) int {
	return c.addKey(code, loc.Path+"|"+fmt.Sprint(loc.Line), loc, n, format, args...)
}

func (c *collector) addKey(code, key string, loc Loc, n int, format string, args ...any) int {
	if n <= 0 {
		n = 1
	}
	key = code + "|" + key
	if i, ok := c.idx[key]; ok {
		c.list[i].Count += n
		return i
	}
	info, ok := codeIndex[code]
	if !ok {
		panic("ikml: unknown issue code " + code)
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	c.idx[key] = len(c.list)
	c.list = append(c.list, Issue{Code: code, Severity: info.Severity, Message: msg, Location: loc.Path, Line: loc.Line, Count: n})
	return len(c.list) - 1
}

func (c *collector) has(code string) bool {
	for _, is := range c.list {
		if is.Code == code {
			return true
		}
	}
	return false
}

var sevRank = map[Severity]int{SevError: 0, SevWarning: 1, SevInfo: 2}

// finish sorts issues by severity (stable within a severity) and caps the
// number of locations listed per code.
func finishIssues(list []Issue) []Issue {
	out := append([]Issue(nil), list...)
	sort.SliceStable(out, func(i, j int) bool { return sevRank[out[i].Severity] < sevRank[out[j].Severity] })
	perCode := map[string]int{}
	extra := map[string]*Issue{}
	var res []Issue
	for _, is := range out {
		perCode[is.Code]++
		if perCode[is.Code] <= maxIssuesPerCode {
			res = append(res, is)
			continue
		}
		if e, ok := extra[is.Code]; ok {
			e.Count += is.Count
			continue
		}
		cp := is
		cp.Location = ""
		extra[is.Code] = &cp
	}
	// Append the overflow summaries right after the last entry of each code.
	if len(extra) == 0 {
		return res
	}
	var final []Issue
	for i, is := range res {
		final = append(final, is)
		last := i == len(res)-1 || res[i+1].Code != is.Code
		if e, ok := extra[is.Code]; ok && last {
			more := perCode[is.Code] - maxIssuesPerCode
			e.Message = fmt.Sprintf("The same problem appears in %d more places.", more)
			final = append(final, *e)
			delete(extra, is.Code)
		}
	}
	for _, e := range extra { // codes whose entries were not contiguous
		final = append(final, *e)
	}
	return final
}
