package ikml

import (
	"fmt"
	"math"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// LargeCoordinateCount is the per-placemark position count above which
// large-coordinate-count is reported.
const LargeCoordinateCount = 100000

var winDriveRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func analyze(d *Doc) {
	c := d.issues
	var tracks, stamped, geomPlacemarks, swappedPMs int
	var swappedList []*Placemark
	for _, pm := range d.Placemarks() {
		loc := Loc{Path: pm.Path, Line: pm.Line}
		if pm.TimeStamp != "" || pm.Begin != "" || pm.End != "" {
			stamped++
		}
		if pm.Geometry == nil {
			c.add("empty-placemark", loc, 0, "This placemark has no geometry, so it will not appear on a map.")
			continue
		}
		geomPlacemarks++
		if isSwapped(pm.Geometry) {
			swappedPMs++
			swappedList = append(swappedList, pm)
		}
		checkGeometry(d, pm, loc)
		for _, l := range pm.Geometry.leaves() {
			if l.Type == "Track" {
				tracks++
			}
		}
	}
	// Swapped coordinates: one issue for the whole file when every
	// placemark is affected, otherwise one per placemark.
	if swappedPMs > 1 && swappedPMs == geomPlacemarks {
		c.add("swapped-latlon", Loc{}, swappedPMs, "Every placemark in the file (%d) has latitude and longitude in the wrong order: the first value of each point is within ±90 and the second is outside it. A swapped point lands on the far side of the world.", swappedPMs)
	} else {
		for _, pm := range swappedList {
			c.add("swapped-latlon", Loc{Path: pm.Path, Line: pm.Line}, 0, "Latitude and longitude are in the wrong order: every point has its first value within ±90 and its second outside it.")
		}
	}

	// Style references.
	for _, pm := range d.Placemarks() {
		if pm.StyleURL == "" {
			continue
		}
		if id, local := localStyleID(pm.StyleURL); local {
			if d.Styles[id] == nil && d.StyleMaps[id] == nil {
				c.add("style-missing", Loc{Path: pm.Path, Line: pm.styleLine}, 0, "The style %s is not defined in this file. Viewers will draw this placemark with default styling.", pm.StyleURL)
			}
		}
	}
	for _, pr := range d.pairs {
		if id, local := localStyleID(pr.url); local {
			if d.Styles[id] == nil && d.StyleMaps[id] == nil {
				c.add("style-missing", pr.loc, 0, "The style map points to %s, which is not defined in this file.", pr.url)
			}
		}
	}

	checkRefs(d)

	if tracks > 0 {
		c.add("has-tracks", Loc{}, tracks, "The file has %s with a time for each point.", count(tracks, "gx:Track GPS track", "gx:Track GPS tracks"))
	}
	if stamped > 0 {
		c.add("has-timestamps", Loc{}, stamped, "%s a TimeStamp or TimeSpan.", count(stamped, "placemark carries", "placemarks carry"))
	}
	if n := len(d.Overlays); n > 0 {
		kinds := map[string]int{}
		for _, o := range d.Overlays {
			kinds[o.Kind]++
		}
		c.add("overlay-present", Loc{}, n, "The file has %s.", countList(kinds))
	}
	if d.Models > 0 {
		c.add("model-present", Loc{}, d.Models, "The file has %s.", count(d.Models, "3D model", "3D models"))
	}
	if d.Format == FormatGeoJSON {
		// Fill in counts on the per-key foreign member notes.
		for i := range c.list {
			is := &c.list[i]
			if is.Code == "foreign-members" {
				for k, n := range d.foreign {
					if strings.Contains(is.Message, fmt.Sprintf("%q", k)) {
						if n > 1 {
							is.Message += fmt.Sprintf(" It appears %d times.", n)
						}
						is.Count = n
					}
				}
			}
		}
	}
}

func countList(m map[string]int) string {
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

func localStyleID(u string) (string, bool) {
	if strings.HasPrefix(u, "#") {
		return u[1:], true
	}
	if !strings.Contains(u, "#") && !strings.Contains(u, "/") && !strings.Contains(u, ".") {
		// Some exporters write the id without the '#'. Google Earth accepts it.
		return u, true
	}
	return "", false
}

// isSwapped is true when every position has a first value within ±90 and
// a second value outside ±90 (but within ±180).
func isSwapped(g *Geometry) bool {
	n := 0
	for _, l := range g.coordLists() {
		for _, c := range l {
			if math.Abs(c.Lon) > 90 || math.Abs(c.Lat) <= 90 || math.Abs(c.Lat) > 180 {
				return false
			}
			n++
		}
	}
	return n > 0
}

func ringClosed(r []Coord) bool {
	if len(r) == 0 {
		return true
	}
	a, b := r[0], r[len(r)-1]
	return a.Lon == b.Lon && a.Lat == b.Lat && a.Alt == b.Alt
}

// signedArea is positive for counterclockwise rings (lon as x, lat as y).
func signedArea(r []Coord) float64 {
	var s float64
	for i := 0; i+1 < len(r); i++ {
		s += r[i].Lon*r[i+1].Lat - r[i+1].Lon*r[i].Lat
	}
	if len(r) > 1 && !ringClosed(r) {
		a, b := r[len(r)-1], r[0]
		s += a.Lon*b.Lat - b.Lon*a.Lat
	}
	return s / 2
}

// wrongWinding reports whether ring i of a polygon breaks RFC 7946.
func wrongWinding(r []Coord, outer bool) bool {
	a := signedArea(r)
	if a == 0 {
		return false
	}
	return (outer && a < 0) || (!outer && a > 0)
}

func checkGeometry(d *Doc, pm *Placemark, loc Loc) {
	c := d.issues
	g := pm.Geometry
	if ls := g.leaves(); len(ls) > 0 && ls[0].Line > 0 {
		loc.Line = ls[0].Line
	}
	swapped := isSwapped(g)
	var total, null, lonBad, latBad, has2, has3, nonzeroAlt int
	clamp := false
	var firstLon, firstLat float64
	for _, l := range g.leaves() {
		gl := loc
		if l.Line > 0 {
			gl.Line = l.Line
		}
		mode := l.AltitudeMode
		if d.Format != FormatGeoJSON && (mode == "" || mode == "clampToGround" || mode == "clampToSeaFloor") {
			clamp = true
		}
		lists := l.coordLists()
		for _, list := range lists {
			for _, p := range list {
				total++
				if p.Lon == 0 && p.Lat == 0 {
					null++
				}
				if math.Abs(p.Lon) > 180 || math.IsNaN(p.Lon) {
					if lonBad == 0 {
						firstLon = p.Lon
					}
					lonBad++
				}
				if math.Abs(p.Lat) > 90 || math.IsNaN(p.Lat) {
					if latBad == 0 {
						firstLat = p.Lat
					}
					latBad++
				}
				if p.Has3 {
					has3++
					if p.Alt != 0 && (mode == "" || mode == "clampToGround" || mode == "clampToSeaFloor") {
						nonzeroAlt++
					}
				} else {
					has2++
				}
			}
		}
		switch l.Type {
		case "LineString":
			if len(l.Coords) < 2 && l.badTuples == 0 {
				c.add("line-too-short", gl, 0, "The line has %s. A line needs at least 2.", count(len(l.Coords), "position", "positions"))
			}
			if n := countDups(l.Coords); n > 0 {
				c.add("duplicate-points", gl, n, "The line has %s.", count(n, "position repeated right after itself", "positions repeated right after themselves"))
			}
		case "LinearRing":
			checkRing(d, l.Coords, gl, "ring", l.badTuples)
		case "Polygon":
			if l.NoOuter && len(l.Rings) == 0 {
				c.add("polygon-no-outer", gl, 0, "The polygon has no outer boundary.")
			} else if l.NoOuter {
				c.add("polygon-no-outer", gl, 0, "The polygon has inner boundaries but no outer boundary.")
			}
			for i, r := range l.Rings {
				name := "outer ring"
				if i > 0 || l.NoOuter {
					name = fmt.Sprintf("inner ring %d", i)
					if l.NoOuter {
						name = fmt.Sprintf("inner ring %d", i+1)
					}
				}
				bad := 0
				if i < len(l.ringBad) {
					bad = l.ringBad[i]
				}
				checkRing(d, r, gl, name, bad)
				if d.Format == FormatGeoJSON && len(r) >= 3 && wrongWinding(r, i == 0 && !l.NoOuter) {
					want := "counterclockwise"
					if i > 0 {
						want = "clockwise"
					}
					c.add("winding", gl, 0, "The %s winds the other way from RFC 7946, which asks for %s. Most tools accept either.", name, want)
				}
			}
		case "Track":
			when := len(l.When)
			pts := len(l.Coords) + l.badTuples
			if when != pts {
				c.add("track-times-mismatch", gl, 0, "The track has %d <when> times and %d <gx:coord> positions. KML needs one time per position.", when, pts)
			}
		}
	}
	projected := ""
	if math.Abs(firstLon) > 1000 || math.Abs(firstLat) > 1000 {
		projected = " Values this large are usually projected coordinates in metres (such as UTM), not degrees. KML and GeoJSON need longitude and latitude in WGS 84."
	}
	if latBad > 0 && !swapped {
		c.add("lat-range", loc, latBad, "%s outside -90 to 90 (for example %v).%s", count(latBad, "latitude value is", "latitude values are"), fmtNum(firstLat), projected)
		projected = ""
	}
	if lonBad > 0 {
		c.add("lon-range", loc, lonBad, "%s outside -180 to 180 (for example %v).%s", count(lonBad, "longitude value is", "longitude values are"), fmtNum(firstLon), projected)
	}
	if null > 0 {
		c.add("null-island", loc, null, "%s exactly 0,0. That is in the ocean off West Africa, and usually means a missing location was written as zero.", count(null, "position is", "positions are"))
	}
	if has2 > 0 && has3 > 0 {
		c.add("mixed-dimensions", loc, 0, "The shape mixes %s without altitude and %s with altitude.", count(has2, "position", "positions"), count(has3, "position", "positions"))
	}
	if nonzeroAlt > 0 && clamp && d.Format != FormatGeoJSON {
		c.add("altitude-ignored", loc, nonzeroAlt, "%s an altitude, but the altitude mode is clampToGround (the default). Most viewers will draw the shape on the ground.", count(nonzeroAlt, "position has", "positions have"))
	}
	if total > LargeCoordinateCount {
		c.add("large-coordinate-count", loc, 0, "This placemark has %d positions. Many web maps slow down or refuse shapes this large.", total)
	}
}

func checkRing(d *Doc, r []Coord, loc Loc, name string, bad int) {
	c := d.issues
	if len(r) == 0 && bad > 0 {
		return
	}
	closed := ringClosed(r)
	n := len(r)
	if !closed {
		c.add("ring-unclosed", loc, 0, "The %s does not end on its first point.", name)
		n++
	}
	if n < 4 && bad == 0 {
		c.add("ring-too-short", loc, 0, "The %s has %s. A ring needs at least 4 (three corners plus the closing point).", name, count(len(r), "position", "positions"))
	}
	if k := countDups(r); k > 0 {
		c.add("duplicate-points", loc, k, "The %s has %s.", name, count(k, "position repeated right after itself", "positions repeated right after themselves"))
	}
}

func countDups(cs []Coord) int {
	n := 0
	for i := 1; i < len(cs); i++ {
		if cs[i] == cs[i-1] {
			n++
		}
	}
	return n
}

func fmtNum(v float64) string { return formatFloat(v) }

// count writes "1 thing" or "3 things".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

type refClass int

const (
	refRelative refClass = iota
	refLocal
	refHTTP
	refHTTPS
	refOther
)

func classifyRef(h string) refClass {
	l := strings.ToLower(h)
	switch {
	case strings.HasPrefix(l, "file:"), winDriveRe.MatchString(h), strings.HasPrefix(h, `\\`),
		strings.HasPrefix(h, "/Users/"), strings.HasPrefix(h, "/home/"), strings.HasPrefix(h, "/var/"),
		strings.HasPrefix(h, "/tmp/"), strings.HasPrefix(h, "/mnt/"), strings.HasPrefix(h, "/private/"),
		strings.HasPrefix(h, "/Volumes/"), strings.HasPrefix(h, "~/"), strings.HasPrefix(h, "/storage/"),
		strings.HasPrefix(h, "/sdcard/"):
		return refLocal
	case strings.HasPrefix(l, "http://"):
		return refHTTP
	case strings.HasPrefix(l, "https://"):
		return refHTTPS
	case strings.HasPrefix(h, "/"):
		return refLocal
	}
	if i := strings.Index(h, ":"); i > 1 {
		if j := strings.IndexAny(h, "/."); j < 0 || j > i {
			return refOther // data:, root://, mailto: and so on
		}
	}
	return refRelative
}

// resolveAsset finds a relative href inside the KMZ.
func (d *Doc) resolveAsset(href string) (string, bool) {
	h := href
	if i := strings.IndexAny(h, "?#"); i >= 0 {
		h = h[:i]
	}
	if u, err := url.PathUnescape(h); err == nil {
		h = u
	}
	h = strings.ReplaceAll(h, `\`, "/")
	dir := path.Dir(d.MainPath)
	cands := []string{path.Clean(path.Join(dir, h)), path.Clean(h)}
	for _, cnd := range cands {
		cnd = strings.TrimPrefix(cnd, "./")
		if _, ok := d.assetData[cnd]; ok {
			return cnd, true
		}
	}
	for _, cnd := range cands {
		for p := range d.assetData {
			if strings.EqualFold(p, cnd) {
				return p, true
			}
		}
	}
	return "", false
}

func checkRefs(d *Doc) {
	c := d.issues
	used := map[string]bool{}
	kindWord := map[string]string{
		"icon": "icon", "overlay": "overlay image", "network-link": "network link",
		"model": "3D model", "description-image": "image in a description",
	}
	for _, r := range d.Refs {
		loc := Loc{Path: r.Location, Line: r.Line}
		what := kindWord[r.Kind]
		switch classifyRef(r.Href) {
		case refLocal:
			c.addKey("local-path-ref", r.Href+"|"+r.Location, loc, 1, "The %s %s points to a file on the creator's computer. Nobody else can open it.", what, r.Href)
		case refHTTP:
			i := c.addKey("http-ref", r.Href, loc, 1, "")
			times := ""
			if n := c.list[i].Count; n > 1 {
				times = fmt.Sprintf(" (%d references, first here)", n)
			}
			c.list[i].Message = fmt.Sprintf("The %s %s uses plain http://%s. Browsers and some viewers block it, and the file is not included.", what, r.Href, times)
		case refHTTPS:
			if r.Kind == "network-link" {
				continue // reported as network-link already
			}
			host := r.Href
			if u, err := url.Parse(r.Href); err == nil && u.Host != "" {
				host = u.Host
			}
			i := c.addKey("remote-ref", host, loc, 1, "")
			c.list[i].Message = fmt.Sprintf("%s to files on %s, first here. They load from the internet and are not inside the file.", count(c.list[i].Count, "reference", "references"), host)
		case refRelative:
			if d.Format == FormatKMZ {
				if p, ok := d.resolveAsset(r.Href); ok {
					used[p] = true
				} else {
					c.addKey("kmz-missing-asset", r.Href+"|"+r.Location, loc, 1, "The %s %s should be inside the KMZ but is not there.", what, r.Href)
				}
			} else if r.Kind != "network-link" || !strings.HasPrefix(r.Href, "#") {
				i := c.addKey("external-file-ref", r.Href, loc, 1, "")
				times := ""
				if n := c.list[i].Count; n > 1 {
					times = fmt.Sprintf(" (%d references, first here)", n)
				}
				c.list[i].Message = fmt.Sprintf("The %s %s is a separate file that must sit next to this .kml%s. If it is missing, the %s will not show. Converting to .kmz bundles it.", what, r.Href, times, what)
			}
		}
	}
	if d.Format == FormatKMZ && d.MainPath != "" {
		for i, a := range d.Assets {
			if used[a.Path] {
				d.Assets[i].Used = true
				continue
			}
			c.add("unused-asset", Loc{Path: a.Path}, 0, "%s (%s) is inside the KMZ but nothing in the document refers to it.", a.Path, humanBytes(a.Size))
		}
	}
}
