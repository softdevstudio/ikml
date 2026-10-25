package ikml

import (
	"fmt"
	"sort"
	"strings"
)

// Loss is one thing a conversion drops or changes.
type Loss struct {
	What  string `json:"what"`
	Count int    `json:"count"`
	// Outcome is "lost" (gone from the output), "kept" (kept in another
	// form, see Detail) or "changed" (kept but altered).
	Outcome string `json:"outcome"`
	Detail  string `json:"detail"`
	// Items lists examples, such as URLs of dropped network links.
	Items []string `json:"items,omitempty"`
}

// LossReport explains what converting d to target ("geojson", "kml" or
// "kmz") will drop or change when written by iKML. It is honest about
// the parts iKML keeps in another form, and lists what a plain converter
// would lose too.
func LossReport(d *Doc, target string) []Loss {
	t := strings.ToLower(strings.TrimPrefix(target, "."))
	if t == "json" {
		t = "geojson"
	}
	src := d.Format
	if src == FormatKMZ {
		src = FormatKML
	}
	switch {
	case src == FormatKML && t == "geojson":
		return lossKMLToGeoJSON(d)
	case src == FormatGeoJSON && (t == "kml" || t == "kmz"):
		return lossGeoJSONToKML(d)
	case src == FormatKML && t == "kml" && d.Format == FormatKMZ:
		var out []Loss
		var imgs []string
		var total int64
		for _, a := range d.Assets {
			imgs = append(imgs, a.Path)
			total += a.Size
		}
		if len(imgs) > 0 {
			out = append(out, Loss{What: "Files inside the KMZ", Count: len(imgs), Outcome: "lost",
				Detail: fmt.Sprintf("A plain .kml cannot hold files. %s (%s) stay behind, and references to them break unless the files are copied next to the .kml. Write a .kmz instead to keep them.", count(len(imgs), "file", "files"), humanBytes(total)),
				Items:  firstN(imgs, 10)})
		}
		return out
	}
	return []Loss{}
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		out := append([]string(nil), s[:n]...)
		return append(out, fmt.Sprintf("and %d more", len(s)-n))
	}
	return s
}

func lossKMLToGeoJSON(d *Doc) []Loss {
	var out []Loss
	pms := d.Placemarks()

	// Styles.
	styled := 0
	used := map[string]bool{}
	inline := 0
	var iconImgs, labelScale, hotspots, balloons, iconScale int
	mappedPM := 0
	for _, pm := range pms {
		has := false
		if pm.StyleURL != "" {
			if id, ok := localStyleID(pm.StyleURL); ok && (d.Styles[id] != nil || d.StyleMaps[id] != nil) {
				used[id] = true
				has = true
			}
		}
		if pm.InlineStyle != nil {
			inline++
			has = true
		}
		if !has {
			continue
		}
		styled++
		if len(simpleStyle(d, pm)) > 0 {
			mappedPM++
		}
		if st := resolveStyle(d, pm); st != nil {
			if st.IconHref != "" {
				iconImgs++
			}
			if st.HasLabel {
				labelScale++
			}
			if st.HotSpot {
				hotspots++
			}
			if st.HasBalloon {
				balloons++
			}
			if st.IconScale != "" && st.IconScale != "1" {
				iconScale++
			}
		}
	}
	highlight := 0
	for id := range used {
		if d.StyleMaps[id] != nil {
			highlight++
		}
	}
	if styled > 0 {
		detail := "GeoJSON has no styling. "
		if len(used) > 0 {
			detail += fmt.Sprintf("%s %s", count(styled, "placemark uses", "placemarks use"), count(len(used), "shared style", "shared styles"))
			if inline > 0 {
				detail += fmt.Sprintf(" and %s", count(inline, "has an inline style", "have an inline style"))
			}
		} else {
			detail += count(inline, "placemark has an inline style", "placemarks have an inline style")
		}
		detail += "."
		if mappedPM > 0 {
			detail += fmt.Sprintf(" iKML keeps line color and width, fill color and opacity, and marker color as simplestyle-spec properties (stroke, stroke-width, stroke-opacity, fill, fill-opacity, marker-color) for %d of them. Mapbox, geojson.io and GitHub read these; most other tools ignore them.", mappedPM)
		} else {
			detail += " None of these styles has a color iKML can carry over as simplestyle-spec properties."
		}
		out = append(out, Loss{What: "Styles and colors", Count: styled, Outcome: "changed", Detail: detail})
		var lost []string
		if iconImgs > 0 {
			lost = append(lost, fmt.Sprintf("icon images (%s)", count(iconImgs, "placemark", "placemarks")))
		}
		if iconScale > 0 {
			lost = append(lost, fmt.Sprintf("icon scale (%d)", iconScale))
		}
		if hotspots > 0 {
			lost = append(lost, fmt.Sprintf("icon hotspots (%d)", hotspots))
		}
		if labelScale > 0 {
			lost = append(lost, fmt.Sprintf("label color and scale (%d)", labelScale))
		}
		if balloons > 0 {
			lost = append(lost, fmt.Sprintf("balloon templates (%d)", balloons))
		}
		if highlight > 0 {
			lost = append(lost, fmt.Sprintf("highlight styles from %s", count(highlight, "StyleMap", "StyleMaps")))
		}
		if len(lost) > 0 {
			out = append(out, Loss{What: "Style details with no GeoJSON equivalent", Count: styled, Outcome: "lost",
				Detail: "These have no simplestyle-spec property and are dropped: " + strings.Join(lost, ", ") + "."})
		}
	}

	// Folders.
	folders := 0
	d.walk(func(f *Folder) {
		if f != d.Root && !f.top {
			folders++
		}
	}, func(*Placemark) {})
	if folders > 0 {
		out = append(out, Loss{What: "Folder structure", Count: folders, Outcome: "kept",
			Detail: fmt.Sprintf("GeoJSON is a flat list of features, so %s flattened. iKML keeps each placemark's folder path in a \"folder\" property (for example \"Survey / Gates\"), and converting back to KML rebuilds the folders.", count(folders, "folder is", "folders are"))})
	}
	descFolders := 0
	d.walk(func(f *Folder) {
		if f.Description != "" {
			descFolders++
		}
	}, func(*Placemark) {})
	if descFolders > 0 {
		out = append(out, Loss{What: "Folder and document descriptions", Count: descFolders, Outcome: "lost",
			Detail: "Descriptions on folders and documents have no place in GeoJSON."})
	}

	// ExtendedData.
	data, simple, typed := 0, 0, map[string]bool{}
	for _, pm := range pms {
		for _, f := range pm.Fields {
			switch f.Source {
			case "Data":
				data++
			case "SimpleData":
				simple++
				if t := schemaType(d, f); t != "" && t != "string" {
					typed[f.Name+" ("+t+")"] = true
				}
			}
		}
	}
	if data+simple > 0 {
		detail := fmt.Sprintf("%s feature properties.", count(data+simple, "ExtendedData value becomes", "ExtendedData values become"))
		outcome := "kept"
		if len(typed) > 0 {
			names := make([]string, 0, len(typed))
			for k := range typed {
				names = append(names, k)
			}
			sort.Strings(names)
			detail += " Schema types are not carried over: every value is written as a string, including " + strings.Join(firstN(names, 8), ", ") + "."
			outcome = "changed"
		}
		disp := 0
		for _, pm := range pms {
			for _, f := range pm.Fields {
				if f.Display != "" {
					disp++
				}
			}
		}
		if disp > 0 {
			detail += fmt.Sprintf(" %s dropped; the property uses the field name.", count(disp, "displayName label is", "displayName labels are"))
			outcome = "changed"
		}
		out = append(out, Loss{What: "Attribute data (ExtendedData)", Count: data + simple, Outcome: outcome, Detail: detail})
	}
	if d.arrayData > 0 {
		out = append(out, Loss{What: "Per-point track data (SimpleArrayData)", Count: d.arrayData, Outcome: "lost",
			Detail: "Values recorded for each track point, such as heart rate or speed, are dropped."})
	}

	// Descriptions.
	desc, html := 0, 0
	for _, pm := range pms {
		if pm.Description != "" {
			desc++
			if strings.Contains(pm.Description, "<") {
				html++
			}
		}
	}
	if desc > 0 {
		detail := fmt.Sprintf("%s kept in a \"description\" property.", count(desc, "description is", "descriptions are"))
		if html > 0 {
			detail += fmt.Sprintf(" %s HTML, which stays HTML; GeoJSON viewers may show it as plain text.", count(html, "contains", "contain"))
		}
		out = append(out, Loss{What: "Descriptions", Count: desc, Outcome: "kept", Detail: detail})
	}

	// Times.
	if d.stampCount > 0 {
		out = append(out, Loss{What: "TimeStamp and TimeSpan", Count: d.stampCount, Outcome: "kept",
			Detail: "Kept as \"timestamp\", \"begin\" and \"end\" properties. GeoJSON has no standard time field, so time sliders will not pick them up."})
	}
	if d.trackCount > 0 {
		out = append(out, Loss{What: "GPS tracks (gx:Track)", Count: d.trackCount, Outcome: "changed",
			Detail: "Each track becomes a LineString. The time of every point is kept in a \"coordTimes\" array property, the convention used by Mapbox's togeojson."})
	}

	// Altitude mode and extrude.
	alt, ext, tess := 0, 0, 0
	for _, pm := range pms {
		if pm.Geometry == nil {
			continue
		}
		for _, l := range pm.Geometry.leaves() {
			if l.AltitudeMode != "" && l.AltitudeMode != "clampToGround" {
				alt++
			}
			if l.Extrude {
				ext++
			}
			if l.Tessellate {
				tess++
			}
		}
	}
	if alt > 0 {
		out = append(out, Loss{What: "Altitude mode", Count: alt, Outcome: "lost",
			Detail: fmt.Sprintf("%s an altitude mode (relativeToGround, absolute and so on). GeoJSON keeps the altitude number but not what it is measured from.", count(alt, "geometry sets", "geometries set"))})
	}
	if ext > 0 {
		out = append(out, Loss{What: "Extrude", Count: ext, Outcome: "lost", Detail: "Walls drawn down to the ground from 3D shapes are a KML display setting with no GeoJSON equivalent."})
	}
	if tess > 0 {
		out = append(out, Loss{What: "Tessellate", Count: tess, Outcome: "lost", Detail: "Lines that follow the terrain are a KML display setting with no GeoJSON equivalent."})
	}

	// Things that are dropped.
	if n := len(d.NetworkLinks); n > 0 {
		var urls []string
		for _, nl := range d.NetworkLinks {
			urls = append(urls, quoteOr(nl.Href, "(no address)"))
		}
		out = append(out, Loss{What: "Network links", Count: n, Outcome: "lost",
			Detail: "NetworkLinks load other KML from elsewhere. GeoJSON cannot refer to other files, so they are dropped. Their contents were never in this file.", Items: firstN(urls, 10)})
	}
	if n := len(d.Overlays); n > 0 {
		var items []string
		for _, o := range d.Overlays {
			items = append(items, o.Kind+" "+quoteOr(o.Name, "")+" "+quoteOr(o.Href, ""))
		}
		out = append(out, Loss{What: "Image overlays", Count: n, Outcome: "lost",
			Detail: "GroundOverlay, ScreenOverlay and PhotoOverlay place images on or over the map. GeoJSON has only vector features.", Items: firstN(trimAll(items), 10)})
	}
	if d.Models > 0 {
		out = append(out, Loss{What: "3D models", Count: d.Models, Outcome: "lost", Detail: "COLLADA models have no GeoJSON equivalent."})
	}
	if d.Views > 0 {
		out = append(out, Loss{What: "Saved views (LookAt, Camera)", Count: d.Views, Outcome: "lost", Detail: "Camera positions for flying to a place are dropped."})
	}
	if d.Format == FormatKMZ && len(d.Assets) > 0 {
		var total int64
		var names []string
		for _, a := range d.Assets {
			total += a.Size
			names = append(names, a.Path)
		}
		out = append(out, Loss{What: "Files inside the KMZ", Count: len(d.Assets), Outcome: "lost",
			Detail: fmt.Sprintf("GeoJSON is a single text file. %s (%s), such as icons and photos, cannot come along, and references to them in descriptions break.", count(len(d.Assets), "embedded file", "embedded files"), humanBytes(total)), Items: firstN(names, 10)})
	}
	if n := len(d.Unknown); n > 0 {
		var names []string
		total := 0
		for _, u := range d.Unknown {
			names = append(names, fmt.Sprintf("%s (%d)", u.Name, u.Count))
			total += u.Count
		}
		out = append(out, Loss{What: "Elements iKML does not interpret", Count: total, Outcome: "lost",
			Detail: "These elements, often vendor extensions, have no GeoJSON form and are dropped. KML or KMZ output keeps them.", Items: firstN(names, 10)})
	}
	unclosed := 0
	for _, is := range d.issues.list {
		if is.Code == "ring-unclosed" {
			unclosed += is.Count
		}
	}
	wrong := 0
	for _, pm := range pms {
		if pm.Geometry == nil {
			continue
		}
		for _, l := range pm.Geometry.leaves() {
			if l.Type == "Polygon" {
				for i, r := range l.Rings {
					if len(r) >= 3 && wrongWinding(r, i == 0 && !l.NoOuter) {
						wrong++
					}
				}
			}
		}
	}
	if unclosed+wrong > 0 {
		var parts []string
		if unclosed > 0 {
			parts = append(parts, count(unclosed, "ring is closed", "rings are closed"))
		}
		if wrong > 0 {
			parts = append(parts, count(wrong, "ring is rewound", "rings are rewound"))
		}
		out = append(out, Loss{What: "Polygon rings", Count: unclosed + wrong, Outcome: "changed",
			Detail: "GeoJSON output has closed rings in RFC 7946 order (outer rings counterclockwise). " + strings.Join(parts, " and ") + ". The points themselves do not move."})
	}
	if n := countIssue(d, "coord-parse"); n > 0 {
		out = append(out, Loss{What: "Unreadable coordinates", Count: n, Outcome: "lost", Detail: "Tuples that could not be read as numbers are left out of the GeoJSON."})
	}
	if out == nil {
		out = []Loss{}
	}
	return out
}

func trimAll(s []string) []string {
	for i := range s {
		s[i] = strings.Join(strings.Fields(s[i]), " ")
	}
	return s
}

func countIssue(d *Doc, code string) int {
	n := 0
	for _, is := range d.issues.list {
		if is.Code == code {
			n += is.Count
		}
	}
	return n
}

func lossGeoJSONToKML(d *Doc) []Loss {
	var out []Loss
	if d.ForeignMembers > 0 {
		keys := make([]string, 0, len(d.foreign))
		for k := range d.foreign {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out = append(out, Loss{What: "Foreign members", Count: d.ForeignMembers, Outcome: "lost",
			Detail: "Members outside the GeoJSON specification have no place in KML and are dropped.", Items: firstN(keys, 10)})
	}
	bbox := 0
	if d.TopBBox {
		bbox++
	}
	nested, nonString, nonNCName := 0, 0, 0
	for _, pm := range d.Placemarks() {
		if pm.BBox {
			bbox++
		}
		for _, f := range pm.Fields {
			if f.Type == "object" || f.Type == "array" {
				nested++
			} else if f.Type == "number" || f.Type == "bool" {
				nonString++
			}
		}
		if pm.ID != "" && !isNCName(pm.ID) {
			nonNCName++
		}
	}
	if bbox > 0 {
		out = append(out, Loss{What: "Bounding boxes", Count: bbox, Outcome: "lost", Detail: "\"bbox\" members are dropped. KML viewers work out extents themselves."})
	}
	if nested > 0 {
		out = append(out, Loss{What: "Nested properties", Count: nested, Outcome: "changed",
			Detail: "Object and array property values are written to ExtendedData as JSON text. Converting back gives strings, not objects."})
	}
	if nonString > 0 {
		out = append(out, Loss{What: "Number and true/false properties", Count: nonString, Outcome: "changed",
			Detail: "KML Data values are text. Numbers and booleans are written as text and come back as strings."})
	}
	if nonNCName > 0 {
		out = append(out, Loss{What: "Feature ids", Count: nonNCName, Outcome: "changed",
			Detail: "Ids that are numbers or contain spaces are not valid XML ids, so they are kept as an \"id\" Data value instead of the Placemark id."})
	}
	if d.HasCRS {
		out = append(out, Loss{What: "crs member", Count: 1, Outcome: "lost", Detail: "KML is always WGS 84. The coordinates are written unchanged."})
	}
	styled := 0
	for _, pm := range d.Placemarks() {
		for _, k := range []string{"stroke", "fill", "marker-color"} {
			if _, ok := getProp(pm.Props, k); ok {
				styled++
				break
			}
		}
	}
	if styled > 0 {
		out = append(out, Loss{What: "simplestyle-spec properties", Count: styled, Outcome: "kept",
			Detail: "stroke, stroke-width, stroke-opacity, fill, fill-opacity and marker-color become an inline KML Style. marker-size and marker-symbol have no KML form and are dropped."})
	}
	if d.ExtraValues > 0 {
		out = append(out, Loss{What: "Extra position values", Count: d.ExtraValues, Outcome: "lost",
			Detail: "Positions with more than three numbers (for example a measure value) lose everything after the altitude."})
	}
	if out == nil {
		out = []Loss{}
	}
	return out
}
