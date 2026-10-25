package ikml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// KML namespace URIs.
const (
	NSKML22   = "http://www.opengis.net/kml/2.2"
	NSGx      = "http://www.google.com/kml/ext/2.2"
	nsKML20   = "http://earth.google.com/kml/2.0"
	nsKML21   = "http://earth.google.com/kml/2.1"
	nsKMLGE22 = "http://earth.google.com/kml/2.2"
	nsKML23   = "http://www.opengis.net/kml/2.3"
	nsAtom    = "http://www.w3.org/2005/Atom"
	nsXAL     = "urn:oasis:names:tc:ciq:xsdschema:xAL:2.0"
)

type nsKind int

const (
	nsK nsKind = iota
	nsG
	nsIgnore
	nsOther
)

func classifyNS(space string) nsKind {
	switch space {
	case "", NSKML22, nsKML20, nsKML21, nsKMLGE22, nsKML23, "kml":
		return nsK
	case NSGx, "gx":
		return nsG
	case nsAtom, nsXAL, "atom", "xal":
		return nsIgnore
	}
	return nsOther
}

// Elements in the KML or gx namespace that iKML recognises but does not
// need to interpret. They are skipped without an unknown-element note.
var kmlIgnored = map[string]bool{
	"visibility": true, "open": true, "Snippet": true, "snippet": true, "address": true,
	"phoneNumber": true, "Region": true, "Metadata": true, "drawOrder": true, "color": true,
	"refreshVisibility": true, "flyToView": true, "refreshMode": true, "refreshInterval": true,
	"viewRefreshMode": true, "viewRefreshTime": true, "viewBoundScale": true, "viewFormat": true,
	"httpQuery": true, "LatLonBox": true, "LatLonAltBox": true, "LatLonQuad": true, "overlayXY": true,
	"screenXY": true, "rotationXY": true, "size": true, "rotation": true, "altitude": true,
	"ViewVolume": true, "ImagePyramid": true, "shape": true, "NetworkLinkControl": true,
	"balloonVisibility": true, "outerColor": true, "outerWidth": true, "physicalWidth": true,
	"labelVisibility": true, "Lod": true, "minRefreshPeriod": true, "maxSessionLength": true,
	"cookie": true, "message": true, "linkName": true, "linkDescription": true, "linkSnippet": true,
	"expires": true, "colorMode": true, "heading": true, "headingMode": true, "bgColor": true,
	"textColor": true, "text": true, "displayMode": true, "listItemType": true, "ItemIcon": true,
	"maxSnippetLines": true, "description": true, "name": true, "interpolate": true, "angles": true,
	"SimpleArrayData": true, "Orientation": true, "Scale": true, "ResourceMap": true, "Location": true,
	"altitudeOffset": true, "x": true, "y": true, "w": true, "h": true, "state": true,
}

var imgSrcRe = regexp.MustCompile(`(?i)<img[^>]*?\ssrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

type kp struct {
	dec     *xml.Decoder
	d       *Doc
	labels  []string
	pm      int
	folders int
	docs    int
	lastOff int64
	err     error
	prefix  map[string]string // namespace URI -> prefix seen in the file
	unk     map[string]int    // display name -> index into d.Unknown
	rootDoc bool
	pairs   []pairRef
	track   int
	stamps  int
	arrays  int
}

type pairRef struct {
	url  string
	rng  [2]int
	loc  Loc
	smID string
}

func (p *kp) tok() xml.Token {
	if p.err != nil {
		return nil
	}
	p.lastOff = p.dec.InputOffset()
	t, err := p.dec.Token()
	if err != nil {
		p.err = err
		return nil
	}
	if se, ok := t.(xml.StartElement); ok {
		for _, a := range se.Attr {
			if a.Name.Space == "xmlns" && a.Value != "" {
				if _, seen := p.prefix[a.Value]; !seen {
					p.prefix[a.Value] = a.Name.Local
				}
			}
		}
	}
	return t
}

func (p *kp) line() int {
	l, _ := p.dec.InputPos()
	return l
}

func (p *kp) path(extra string) string {
	parts := append([]string(nil), p.labels...)
	if extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, " > ")
}

func (p *kp) at(extra string) Loc { return Loc{Path: p.path(extra), Line: p.line()} }

func (p *kp) display(n xml.Name) string {
	switch classifyNS(n.Space) {
	case nsK:
		return n.Local
	case nsG:
		return "gx:" + n.Local
	}
	if pre, ok := p.prefix[n.Space]; ok && pre != "" {
		return pre + ":" + n.Local
	}
	if n.Space != "" && !strings.Contains(n.Space, "/") && !strings.Contains(n.Space, ":") {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// skip consumes the rest of the current element.
func (p *kp) skip() {
	depth := 1
	for depth > 0 {
		t := p.tok()
		if t == nil {
			return
		}
		switch t.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

// text returns the character data of the current element and consumes it.
// Nested markup (for example raw HTML in a description) is flattened back
// into text so nothing is silently lost.
func (p *kp) text() string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		t := p.tok()
		if t == nil {
			break
		}
		switch v := t.(type) {
		case xml.CharData:
			b.Write(v)
		case xml.StartElement:
			depth++
			b.WriteString("<" + v.Name.Local)
			for _, a := range v.Attr {
				fmt.Fprintf(&b, " %s=%q", a.Name.Local, a.Value)
			}
			b.WriteString(">")
		case xml.EndElement:
			depth--
			if depth > 0 {
				b.WriteString("</" + v.Name.Local + ">")
			}
		}
	}
	return b.String()
}

// textRange is text() that also returns the byte range of the content.
func (p *kp) textRange() (string, [2]int) {
	start := int(p.dec.InputOffset())
	var b []byte
	depth := 1
	end := start
	for depth > 0 {
		t := p.tok()
		if t == nil {
			break
		}
		switch v := t.(type) {
		case xml.CharData:
			if depth == 1 {
				b = append(b, v...)
			}
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				end = int(p.lastOff)
			}
		}
	}
	return string(b), [2]int{start, end}
}

func attr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name && (a.Name.Space == "" || classifyNS(a.Name.Space) == nsK) {
			return a.Value
		}
	}
	return ""
}

func (p *kp) unknown(se xml.StartElement) {
	name := p.display(se.Name)
	if i, ok := p.unk[name]; ok {
		p.d.Unknown[i].Count++
		p.skip()
		return
	}
	loc := p.at("")
	p.unk[name] = len(p.d.Unknown)
	// Elements from a foreign namespace (vendor extensions) are grouped
	// into one issue per namespace; unknown KML elements get one each.
	// Messages are written at the end, once the counts are known.
	key := name
	if classifyNS(se.Name.Space) == nsOther {
		key = "ns:" + se.Name.Space
	}
	ix := p.d.issues.addKey("unknown-element", key, loc, 1, "-")
	p.d.Unknown = append(p.d.Unknown, UnknownElement{Name: name, Count: 1, Location: loc.Path, Line: loc.Line, issue: ix, space: se.Name.Space})
	p.skip()
}

// known returns true for elements to skip quietly.
func (p *kp) known(se xml.StartElement) bool {
	k := classifyNS(se.Name.Space)
	if k == nsIgnore {
		p.skip()
		return true
	}
	if (k == nsK || k == nsG) && kmlIgnored[se.Name.Local] {
		p.skip()
		return true
	}
	return false
}

func (p *kp) noteID(id, label string, loc Loc) {
	if id == "" {
		return
	}
	if n, ok := p.d.ids[id]; ok {
		p.d.ids[id] = n + 1
		p.d.issues.add("duplicate-ids", loc, 0, "The id %q is used more than once (first at %s).", id, p.d.idLoc[id])
		return
	}
	p.d.ids[id] = 1
	p.d.idLoc[id] = label
}

func parseKML(d *Doc, raw []byte, strict bool) error {
	p := &kp{d: d, prefix: map[string]string{}, unk: map[string]int{}}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = strict
	if !strict {
		dec.AutoClose = xml.HTMLAutoClose
		dec.Entity = xml.HTMLEntity
	}
	dec.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return r, nil
		}
		return nil, fmt.Errorf("unsupported encoding %q", label)
	}
	p.dec = dec
	d.Root = &Folder{Kind: "kml"}
	for {
		t := p.tok()
		if t == nil {
			break
		}
		se, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		rootStart := int(p.lastOff)
		d.rootTag = [2]int{rootStart, int(dec.InputOffset())}
		k := classifyNS(se.Name.Space)
		if k == nsK && se.Name.Local == "kml" {
			d.Namespace = se.Name.Space
			switch se.Name.Space {
			case "", "kml":
				d.Namespace = ""
				d.issues.add("no-namespace", Loc{Line: p.line()}, 0, "The <kml> root has no namespace. Most programs read it anyway.")
			case nsKML20, nsKML21:
				d.issues.add("old-namespace", Loc{Line: p.line()}, 0, "The file uses the old namespace %s. Newer tools expect %s.", se.Name.Space, NSKML22)
			}
			p.rootDoc = true
			p.container(d.Root, se, true)
		} else if k == nsK && (se.Name.Local == "Document" || se.Name.Local == "Folder" || se.Name.Local == "Placemark") {
			d.Namespace = se.Name.Space
			d.issues.add("no-kml-root", Loc{Line: p.line()}, 0, "The root element is <%s> without the <kml> wrapper.", se.Name.Local)
			p.rootDoc = true
			p.feature(d.Root, se)
		} else {
			d.issues.add("xml-invalid", Loc{Line: p.line()}, 0, "The root element is <%s>, not <kml>. This is not a KML file.", p.display(se.Name))
			d.fatal = true
			p.skip()
		}
		// Anything after the root element is ignored by the decoder rules.
		for p.tok() != nil {
		}
		break
	}
	// Unknown-element messages, now that counts are final.
	type group struct {
		names []string
		total int
		space string
	}
	groups := map[int]*group{}
	var order []int
	for _, u := range d.Unknown {
		g := groups[u.issue]
		if g == nil {
			g = &group{space: u.space}
			groups[u.issue] = g
			order = append(order, u.issue)
		}
		g.names = append(g.names, "<"+u.Name+">")
		g.total += u.Count
	}
	for _, ix := range order {
		g := groups[ix]
		is := &d.issues.list[ix]
		is.Count = g.total
		if len(g.names) == 1 {
			times := "once"
			if g.total > 1 {
				times = fmt.Sprintf("%d times (first here)", g.total)
			}
			is.Message = fmt.Sprintf("%s appears %s. iKML does not interpret it. KML output keeps it; GeoJSON output drops it.", g.names[0], times)
			continue
		}
		pre := p.prefix[g.space]
		ex := strings.Join(firstN(g.names, 4), ", ")
		is.Message = fmt.Sprintf("%d different elements from the %s namespace (%s) appear %d times in all, first here: %s. They are vendor extensions iKML does not interpret. KML output keeps them; GeoJSON output drops them.", len(g.names), quoteOr(pre, "unnamed"), g.space, g.total, ex)
	}
	d.pairs = p.pairs
	d.trackCount = p.track
	d.stampCount = p.stamps
	d.arrayData = p.arrays
	if p.err != nil && p.err != io.EOF {
		return p.err
	}
	if d.Root != nil && len(d.Root.Items) == 0 && !d.fatal && p.err == io.EOF && !p.rootDoc {
		return errors.New("no XML elements found")
	}
	return nil
}

// container parses the children of Document, Folder or the kml root.
func (p *kp) container(f *Folder, se xml.StartElement, isRoot bool) {
	for {
		t := p.tok()
		if t == nil {
			return
		}
		switch v := t.(type) {
		case xml.EndElement:
			return
		case xml.StartElement:
			p.child(f, v, isRoot)
		}
	}
}

func (p *kp) child(f *Folder, se xml.StartElement, isRoot bool) {
	k := classifyNS(se.Name.Space)
	if k == nsK {
		switch se.Name.Local {
		case "name":
			f.Name = strings.TrimSpace(p.text())
			return
		case "description":
			f.Description = p.text()
			return
		case "Document", "Folder", "Placemark", "NetworkLink", "GroundOverlay", "ScreenOverlay", "PhotoOverlay":
			p.feature(f, se)
			return
		case "Style":
			p.style(se, nil)
			return
		case "StyleMap":
			p.styleMap(se)
			return
		case "Schema":
			p.schema(se)
			return
		case "LookAt", "Camera":
			p.d.Views++
			p.skip()
			return
		case "ExtendedData", "TimeStamp", "TimeSpan", "styleUrl":
			p.skip()
			return
		}
	}
	if k == nsG && (se.Name.Local == "TimeStamp" || se.Name.Local == "TimeSpan") {
		p.skip()
		return
	}
	if p.known(se) {
		return
	}
	p.unknown(se)
}

func (p *kp) label(kind, name string, n int) string {
	if name != "" {
		return fmt.Sprintf("%s '%s'", kind, name)
	}
	return fmt.Sprintf("%s #%d", kind, n)
}

// feature parses a Document, Folder, Placemark, NetworkLink or overlay.
func (p *kp) feature(parent *Folder, se xml.StartElement) {
	switch se.Name.Local {
	case "Document", "Folder":
		if se.Name.Local == "Folder" {
			p.folders++
		} else {
			p.docs++
		}
		n := p.folders
		if se.Name.Local == "Document" {
			n = p.docs
		}
		f := &Folder{Kind: se.Name.Local, ID: attr(se, "id")}
		line := p.line()
		f.top = p.rootDoc && parent == p.d.Root && se.Name.Local == "Document"
		// Peek the name: it normally comes first. Labels use it, so collect
		// children into f and fix the label after the name is known.
		top := p.rootDoc && parent == p.d.Root && se.Name.Local == "Document"
		idx := len(p.labels)
		if !top {
			p.labels = append(p.labels, p.label(se.Name.Local, "", n))
		}
		parent.Items = append(parent.Items, Item{Folder: f})
		f.Path = p.path("")
		p.noteID(f.ID, f.Path, Loc{Path: f.Path, Line: line})
		for {
			t := p.tok()
			if t == nil {
				break
			}
			if _, ok := t.(xml.EndElement); ok {
				break
			}
			c, ok := t.(xml.StartElement)
			if !ok {
				continue
			}
			p.child(f, c, false)
			if f.Name != "" && !top && strings.HasSuffix(p.labels[idx], fmt.Sprintf("#%d", n)) {
				p.labels[idx] = p.label(se.Name.Local, f.Name, n)
				f.Path = p.path("")
			}
		}
		if !top {
			p.labels = p.labels[:idx]
		}
		if f.Path == "" {
			f.Path = p.label(se.Name.Local, f.Name, n)
		}
		if len(f.Items) == 0 && f.other == 0 {
			p.d.issues.add("empty-folder", Loc{Path: f.Path, Line: line}, 0, "This %s has nothing inside.", strings.ToLower(se.Name.Local))
		}
	case "Placemark":
		p.placemark(parent, se)
	case "NetworkLink":
		parent.other++
		p.networkLink(se)
	default:
		parent.other++
		p.overlay(se)
	}
}

func (p *kp) networkLink(se xml.StartElement) {
	nl := NetworkLink{}
	line := p.line()
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch {
		case classifyNS(c.Name.Space) == nsK && c.Name.Local == "name":
			nl.Name = strings.TrimSpace(p.text())
		case classifyNS(c.Name.Space) == nsK && (c.Name.Local == "Link" || c.Name.Local == "Url"):
			nl.Href = p.linkHref()
		case classifyNS(c.Name.Space) == nsK && (c.Name.Local == "LookAt" || c.Name.Local == "Camera"):
			p.d.Views++
			p.skip()
		default:
			if !p.known(c) {
				if classifyNS(c.Name.Space) == nsK && (c.Name.Local == "styleUrl" || c.Name.Local == "Style" || c.Name.Local == "TimeStamp" || c.Name.Local == "TimeSpan" || c.Name.Local == "ExtendedData") {
					p.skip()
				} else {
					p.unknown(c)
				}
			}
		}
	}
	nl.Location = p.path(p.label("NetworkLink", nl.Name, len(p.d.NetworkLinks)+1))
	loc := Loc{Path: nl.Location, Line: line}
	p.d.NetworkLinks = append(p.d.NetworkLinks, nl)
	if nl.Href != "" {
		p.d.Refs = append(p.d.Refs, Ref{Href: nl.Href, Kind: "network-link", Location: nl.Location, Line: line})
	}
	p.d.issues.add("network-link", loc, 0, "NetworkLink to %s. Its contents load from there and are not part of this file.", quoteOr(nl.Href, "(no address)"))
}

func quoteOr(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func (p *kp) linkHref() string {
	href := ""
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if c.Name.Local == "href" {
			href = strings.TrimSpace(p.text())
		} else {
			p.skip()
		}
	}
	return href
}

func (p *kp) overlay(se xml.StartElement) {
	ov := Overlay{Kind: se.Name.Local}
	line := p.line()
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		k := classifyNS(c.Name.Space)
		switch {
		case k == nsK && c.Name.Local == "name":
			ov.Name = strings.TrimSpace(p.text())
		case k == nsK && c.Name.Local == "Icon":
			ov.Href = p.linkHref()
		case k == nsK && (c.Name.Local == "LookAt" || c.Name.Local == "Camera"):
			p.d.Views++
			p.skip()
		case k == nsK && (c.Name.Local == "Point" || c.Name.Local == "styleUrl" || c.Name.Local == "Style" || c.Name.Local == "ExtendedData" || c.Name.Local == "TimeStamp" || c.Name.Local == "TimeSpan"):
			p.skip()
		default:
			if !p.known(c) {
				p.unknown(c)
			}
		}
	}
	ov.Location = p.path(p.label(ov.Kind, ov.Name, len(p.d.Overlays)+1))
	p.d.Overlays = append(p.d.Overlays, ov)
	if ov.Href != "" {
		p.d.Refs = append(p.d.Refs, Ref{Href: ov.Href, Kind: "overlay", Location: ov.Location, Line: line})
	}
}

func (p *kp) placemark(parent *Folder, se xml.StartElement) {
	p.pm++
	pm := &Placemark{ID: attr(se, "id"), Line: p.line()}
	idx := len(p.labels)
	p.labels = append(p.labels, p.label("Placemark", "", p.pm))
	pm.Path = p.path("")
	pm.FolderPath = folderNames(p.labels[:idx])
	parent.Items = append(parent.Items, Item{Placemark: pm})
	var geoms []*Geometry
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		k := classifyNS(c.Name.Space)
		if k == nsK {
			switch c.Name.Local {
			case "name":
				pm.Name = strings.TrimSpace(p.text())
				p.labels[idx] = p.label("Placemark", pm.Name, p.pm)
				pm.Path = p.path("")
				continue
			case "description":
				pm.Description = p.text()
				p.descRefs(pm.Description, pm.Path)
				continue
			case "styleUrl":
				start := int(p.lastOff)
				pm.StyleURL = strings.TrimSpace(p.text())
				pm.styleRange = [2]int{start, int(p.dec.InputOffset())}
				pm.styleLine = p.line()
				continue
			case "Style":
				pm.InlineStyle = &Style{}
				p.style(c, pm.InlineStyle)
				continue
			case "StyleMap":
				p.styleMap(c)
				continue
			case "ExtendedData":
				p.extendedData(pm)
				continue
			case "TimeStamp":
				pm.TimeStamp = p.when()
				p.stamps++
				continue
			case "TimeSpan":
				pm.Begin, pm.End = p.span()
				p.stamps++
				continue
			case "LookAt", "Camera":
				p.d.Views++
				pm.views++
				p.skip()
				continue
			case "Point", "LineString", "LinearRing", "Polygon", "MultiGeometry", "Model":
				if g := p.geometry(c); g != nil {
					geoms = append(geoms, g)
				}
				continue
			}
		}
		if k == nsG {
			switch c.Name.Local {
			case "Track", "MultiTrack":
				if g := p.geometry(c); g != nil {
					geoms = append(geoms, g)
				}
				continue
			case "TimeStamp":
				pm.TimeStamp = p.when()
				p.stamps++
				continue
			case "TimeSpan":
				pm.Begin, pm.End = p.span()
				p.stamps++
				continue
			}
		}
		if !p.known(c) {
			p.unknown(c)
		}
	}
	switch len(geoms) {
	case 0:
	case 1:
		pm.Geometry = geoms[0]
	default:
		pm.Geometry = &Geometry{Type: "MultiGeometry", Parts: geoms, slot: -1}
	}
	p.noteID(pm.ID, pm.Path, Loc{Path: pm.Path, Line: pm.Line})
	p.labels = p.labels[:idx]
	if p.err != nil {
		// The XML broke inside this placemark: leave it out rather than
		// report it as empty.
		parent.Items = parent.Items[:len(parent.Items)-1]
		p.pm--
	}
}

func folderNames(labels []string) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		if j := strings.Index(l, " '"); j >= 0 && strings.HasSuffix(l, "'") {
			out[i] = l[j+2 : len(l)-1]
		} else {
			out[i] = l
		}
	}
	return out
}

func (p *kp) descRefs(desc, loc string) {
	if !strings.Contains(strings.ToLower(desc), "<img") {
		return
	}
	for _, m := range imgSrcRe.FindAllStringSubmatch(desc, -1) {
		src := m[1] + m[2] + m[3]
		if src != "" {
			p.d.Refs = append(p.d.Refs, Ref{Href: strings.TrimSpace(src), Kind: "description-image", Location: loc, Line: p.line()})
		}
	}
}

func (p *kp) when() string {
	w := ""
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		if c, ok := t.(xml.StartElement); ok {
			if c.Name.Local == "when" {
				w = strings.TrimSpace(p.text())
			} else {
				p.skip()
			}
		}
	}
	return w
}

func (p *kp) span() (string, string) {
	var b, e string
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		if c, ok := t.(xml.StartElement); ok {
			switch c.Name.Local {
			case "begin":
				b = strings.TrimSpace(p.text())
			case "end":
				e = strings.TrimSpace(p.text())
			default:
				p.skip()
			}
		}
	}
	return b, e
}

func (p *kp) extendedData(pm *Placemark) {
	for {
		t := p.tok()
		if t == nil {
			return
		}
		if _, ok := t.(xml.EndElement); ok {
			return
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		k := classifyNS(c.Name.Space)
		switch {
		case k == nsK && c.Name.Local == "Data":
			f := Field{Name: attr(c, "name"), Source: "Data"}
			for {
				t := p.tok()
				if t == nil {
					break
				}
				if _, ok := t.(xml.EndElement); ok {
					break
				}
				if cc, ok := t.(xml.StartElement); ok {
					switch cc.Name.Local {
					case "value":
						f.Value = p.text()
					case "displayName":
						f.Display = strings.TrimSpace(p.text())
					default:
						p.skip()
					}
				}
			}
			pm.Fields = append(pm.Fields, f)
		case k == nsK && c.Name.Local == "SchemaData":
			pm.SchemaURL = attr(c, "schemaUrl")
			for {
				t := p.tok()
				if t == nil {
					break
				}
				if _, ok := t.(xml.EndElement); ok {
					break
				}
				if cc, ok := t.(xml.StartElement); ok {
					if cc.Name.Local == "SimpleData" {
						pm.Fields = append(pm.Fields, Field{Name: attr(cc, "name"), Value: p.text(), Source: "SimpleData", schema: pm.SchemaURL})
					} else if cc.Name.Local == "SimpleArrayData" {
						p.arrays++
						p.skip()
					} else if !p.known(cc) {
						p.unknown(cc)
					}
				}
			}
		default:
			if !p.known(c) {
				p.unknown(c)
			}
		}
	}
}

func (p *kp) style(se xml.StartElement, into *Style) {
	st := into
	if st == nil {
		st = &Style{}
	}
	st.ID = attr(se, "id")
	line := p.line()
	st.location = p.path(p.label("Style", st.ID, 0))
	if into == nil {
		if st.ID == "" {
			// A shared style without an id cannot be referenced.
			p.styleBody(st)
			return
		}
		st.location = "Style '" + st.ID + "'"
		p.noteID(st.ID, st.location, Loc{Path: st.location, Line: line})
		if _, dup := p.d.Styles[st.ID]; !dup {
			p.d.Styles[st.ID] = st
			p.d.StyleOrder = append(p.d.StyleOrder, st.ID)
		}
	} else {
		st.location = p.path("")
	}
	p.styleBody(st)
}

func (p *kp) styleBody(st *Style) {
	for {
		t := p.tok()
		if t == nil {
			return
		}
		if _, ok := t.(xml.EndElement); ok {
			return
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch c.Name.Local {
		case "IconStyle":
			st.HasIcon = true
			p.substyle(func(n xml.StartElement) bool {
				switch n.Name.Local {
				case "color":
					st.IconColor = strings.TrimSpace(p.text())
				case "scale":
					st.IconScale = strings.TrimSpace(p.text())
				case "Icon":
					st.IconHref = p.linkHref()
					if st.IconHref != "" {
						p.d.Refs = append(p.d.Refs, Ref{Href: st.IconHref, Kind: "icon", Location: st.location, Line: p.line()})
					}
				case "hotSpot":
					st.HotSpot = true
					p.skip()
				default:
					return false
				}
				return true
			})
		case "LineStyle":
			st.HasLine = true
			p.substyle(func(n xml.StartElement) bool {
				switch n.Name.Local {
				case "color":
					st.LineColor = strings.TrimSpace(p.text())
				case "width":
					st.LineWidth = strings.TrimSpace(p.text())
				default:
					return false
				}
				return true
			})
		case "PolyStyle":
			st.HasPoly = true
			p.substyle(func(n xml.StartElement) bool {
				switch n.Name.Local {
				case "color":
					st.PolyColor = strings.TrimSpace(p.text())
				case "fill":
					st.PolyFill = strings.TrimSpace(p.text())
				case "outline":
					st.PolyOutline = strings.TrimSpace(p.text())
				default:
					return false
				}
				return true
			})
		case "LabelStyle":
			st.HasLabel = true
			p.skip()
		case "BalloonStyle":
			st.HasBalloon = true
			p.skip()
		case "ListStyle":
			st.HasList = true
			p.substyle(func(n xml.StartElement) bool {
				if n.Name.Local == "ItemIcon" {
					if h := p.linkHref(); h != "" {
						p.d.Refs = append(p.d.Refs, Ref{Href: h, Kind: "icon", Location: st.location, Line: p.line()})
					}
					return true
				}
				return false
			})
		default:
			if !p.known(c) {
				p.unknown(c)
			}
		}
	}
}

// substyle walks the children of IconStyle, LineStyle and similar. The
// handler returns false for children it does not consume.
func (p *kp) substyle(h func(xml.StartElement) bool) {
	for {
		t := p.tok()
		if t == nil {
			return
		}
		if _, ok := t.(xml.EndElement); ok {
			return
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if !h(c) {
			if !p.known(c) {
				p.unknown(c)
			}
		}
	}
}

func (p *kp) styleMap(se xml.StartElement) {
	sm := &StyleMap{ID: attr(se, "id")}
	line := p.line()
	sm.location = "StyleMap '" + sm.ID + "'"
	if sm.ID != "" {
		p.noteID(sm.ID, sm.location, Loc{Path: sm.location, Line: line})
		if _, dup := p.d.StyleMaps[sm.ID]; !dup {
			p.d.StyleMaps[sm.ID] = sm
			p.d.StyleOrder = append(p.d.StyleOrder, sm.ID)
		}
	}
	for {
		t := p.tok()
		if t == nil {
			return
		}
		if _, ok := t.(xml.EndElement); ok {
			return
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if c.Name.Local != "Pair" {
			if !p.known(c) {
				p.unknown(c)
			}
			continue
		}
		var key, url string
		var inline *Style
		var rng [2]int
		var uline int
		for {
			t := p.tok()
			if t == nil {
				break
			}
			if _, ok := t.(xml.EndElement); ok {
				break
			}
			cc, ok := t.(xml.StartElement)
			if !ok {
				continue
			}
			switch cc.Name.Local {
			case "key":
				key = strings.TrimSpace(p.text())
			case "styleUrl":
				start := int(p.lastOff)
				url = strings.TrimSpace(p.text())
				rng = [2]int{start, int(p.dec.InputOffset())}
				uline = p.line()
			case "Style":
				inline = &Style{}
				p.style(cc, inline)
				inline.location = sm.location
			default:
				if !p.known(cc) {
					p.unknown(cc)
				}
			}
		}
		if key == "highlight" {
			sm.Highlight, sm.HighlightInline = url, inline
		} else {
			sm.Normal, sm.NormalInline = url, inline
		}
		if url != "" {
			p.pairs = append(p.pairs, pairRef{url: url, rng: rng, loc: Loc{Path: sm.location, Line: uline}, smID: sm.ID})
		}
	}
}

func (p *kp) schema(se xml.StartElement) {
	sc := &Schema{ID: attr(se, "id"), Name: attr(se, "name")}
	p.noteID(sc.ID, "Schema '"+sc.ID+"'", Loc{Path: "Schema '" + sc.ID + "'", Line: p.line()})
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if c.Name.Local == "SimpleField" {
			sf := SchemaField{Name: attr(c, "name"), Type: attr(c, "type")}
			for {
				t := p.tok()
				if t == nil {
					break
				}
				if _, ok := t.(xml.EndElement); ok {
					break
				}
				if cc, ok := t.(xml.StartElement); ok {
					if cc.Name.Local == "displayName" {
						sf.DisplayName = strings.TrimSpace(p.text())
					} else {
						p.skip()
					}
				}
			}
			sc.Fields = append(sc.Fields, sf)
		} else if c.Name.Local == "SimpleArrayField" {
			p.skip()
		} else if !p.known(c) {
			p.unknown(c)
		}
	}
	key := sc.ID
	if key == "" {
		key = sc.Name
	}
	if key != "" {
		p.d.Schemas[key] = sc
	}
}

// geometry parses one geometry element. Unknown children are noted.
func (p *kp) geometry(se xml.StartElement) *Geometry {
	g := &Geometry{Type: se.Name.Local, slot: -1, Line: p.line()}
	if se.Name.Local == "Track" {
		g.Type = "Track"
		p.track++
	}
	for {
		t := p.tok()
		if t == nil {
			break
		}
		if _, ok := t.(xml.EndElement); ok {
			break
		}
		c, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		k := classifyNS(c.Name.Space)
		name := c.Name.Local
		switch {
		case (k == nsK || k == nsG) && name == "altitudeMode":
			g.AltitudeMode = strings.TrimSpace(p.text())
		case (k == nsK || k == nsG) && name == "extrude":
			g.Extrude = isTrue(p.text())
		case k == nsK && name == "tessellate":
			g.Tessellate = isTrue(p.text())
		case k == nsK && name == "coordinates" && (g.Type == "Point" || g.Type == "LineString" || g.Type == "LinearRing"):
			txt, rng := p.textRange()
			g.slot = len(p.d.slots)
			p.d.slots = append(p.d.slots, slot{start: rng[0], end: rng[1]})
			g.Coords, g.badTuples, g.spacing = p.coords(txt, g.Line)
		case k == nsK && (name == "outerBoundaryIs" || name == "innerBoundaryIs") && g.Type == "Polygon":
			outer := name == "outerBoundaryIs"
			for {
				t := p.tok()
				if t == nil {
					break
				}
				if _, ok := t.(xml.EndElement); ok {
					break
				}
				r, ok := t.(xml.StartElement)
				if !ok {
					continue
				}
				if r.Name.Local != "LinearRing" {
					if !p.known(r) {
						p.unknown(r)
					}
					continue
				}
				ring := p.geometry(r)
				if outer && len(g.Rings) > 0 && g.hasOuter {
					// A second outer boundary is invalid; keep it as a ring.
				}
				if outer {
					g.hasOuter = true
					g.Rings = append([][]Coord{ring.Coords}, g.Rings...)
					g.ringSlots = append([]int{ring.slot}, g.ringSlots...)
					g.ringBad = append([]int{ring.badTuples}, g.ringBad...)
				} else {
					g.Rings = append(g.Rings, ring.Coords)
					g.ringSlots = append(g.ringSlots, ring.slot)
					g.ringBad = append(g.ringBad, ring.badTuples)
				}
				g.badTuples += ring.badTuples
				g.spacing = g.spacing || ring.spacing
			}
		case k == nsK && g.Type == "MultiGeometry" && (name == "Point" || name == "LineString" || name == "LinearRing" || name == "Polygon" || name == "MultiGeometry" || name == "Model"):
			if part := p.geometry(c); part != nil {
				g.Parts = append(g.Parts, part)
			}
		case k == nsG && g.Type == "MultiGeometry" && (name == "Track" || name == "MultiTrack"):
			if part := p.geometry(c); part != nil {
				g.Parts = append(g.Parts, part)
			}
		case k == nsG && g.Type == "MultiTrack" && name == "Track":
			if part := p.geometry(c); part != nil {
				g.Parts = append(g.Parts, part)
			}
		case k == nsK && g.Type == "Track" && name == "when":
			g.When = append(g.When, strings.TrimSpace(p.text()))
		case k == nsG && g.Type == "Track" && name == "coord":
			txt, rng := p.textRange()
			c, ok := parseGxCoord(txt)
			if ok {
				g.pointSlots = append(g.pointSlots, len(p.d.slots))
				p.d.slots = append(p.d.slots, slot{start: rng[0], end: rng[1], gxcoord: true})
				g.Coords = append(g.Coords, c)
			} else {
				g.badTuples++
				p.d.issues.add("coord-parse", Loc{Path: p.path(""), Line: p.line()}, 0, "A gx:coord value %q is not two or three numbers.", clip(txt, 40))
			}
		case k == nsK && g.Type == "Track" && name == "Model":
			p.d.Models++
			p.model()
		case k == nsK && g.Type == "Model" && name == "Link":
			if h := p.linkHref(); h != "" {
				p.d.Refs = append(p.d.Refs, Ref{Href: h, Kind: "model", Location: p.path(""), Line: p.line()})
			}
		case k == nsK && g.Type == "Track" && name == "ExtendedData":
			p.arrays++
			p.skip()
		default:
			if !p.known(c) {
				p.unknown(c)
			}
		}
	}
	if g.Type == "Model" {
		p.d.Models++
	}
	if g.Type == "Polygon" && !g.hasOuter {
		g.NoOuter = true
	}
	return g
}

func (p *kp) model() {
	for {
		t := p.tok()
		if t == nil {
			return
		}
		if _, ok := t.(xml.EndElement); ok {
			return
		}
		if c, ok := t.(xml.StartElement); ok {
			if c.Name.Local == "Link" {
				if h := p.linkHref(); h != "" {
					p.d.Refs = append(p.d.Refs, Ref{Href: h, Kind: "model", Location: p.path(""), Line: p.line()})
				}
			} else {
				p.skip()
			}
		}
	}
}

func isTrue(s string) bool {
	s = strings.TrimSpace(s)
	return s == "1" || strings.EqualFold(s, "true")
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// coords parses a KML coordinates string and records parse problems.
func (p *kp) coords(s string, line int) ([]Coord, int, bool) {
	cs, bad, spacing := ParseCoordinates(s)
	for _, b := range bad {
		p.d.issues.add("coord-parse", Loc{Path: p.path(""), Line: line}, 0, "Could not read the coordinate %q as two or three numbers.", clip(b, 40))
	}
	if spacing {
		p.d.issues.add("coord-spacing", Loc{Path: p.path(""), Line: line}, 0, "Coordinates have spaces next to the commas, like \"lon, lat\". Some programs read each number as a separate point.")
	}
	return cs, len(bad), spacing
}

// ParseCoordinates reads a KML <coordinates> string. Tuples are separated
// by whitespace and values by commas. Spaces around commas ("lon, lat")
// are tolerated and reported through spacing. Tuples that cannot be read
// are returned in bad and left out of the result.
func ParseCoordinates(s string) (coords []Coord, bad []string, spacing bool) {
	i, n := 0, len(s)
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
	for {
		for i < n && isSpace(s[i]) {
			i++
		}
		if i >= n {
			break
		}
		start := i
		var vals [3]float64
		nv := 0
		ok := true
		for {
			j := i
			for j < n && !isSpace(s[j]) && s[j] != ',' {
				j++
			}
			if j == i {
				ok = false
			} else if nv < 3 {
				v, good := parseNumber(s[i:j])
				if !good {
					ok = false
				}
				vals[nv] = v
				nv++
			} else {
				ok = false
			}
			i = j
			k := i
			for k < n && isSpace(s[k]) {
				k++
			}
			if k < n && s[k] == ',' {
				if k > i {
					spacing = true
				}
				i = k + 1
				k = i
				for k < n && isSpace(s[k]) {
					k++
				}
				if k > i {
					spacing = true
				}
				i = k
				if i >= n {
					ok = false
					break
				}
				continue
			}
			break
		}
		if !ok || nv < 2 {
			// Resynchronise at the next whitespace.
			for i < n && !isSpace(s[i]) {
				i++
			}
			bad = append(bad, s[start:i])
			continue
		}
		coords = append(coords, Coord{Lon: vals[0], Lat: vals[1], Alt: vals[2], Has3: nv == 3})
	}
	return
}

func parseNumber(s string) (float64, bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E') {
			return 0, false
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseGxCoord(s string) (Coord, bool) {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' })
	if len(f) < 2 || len(f) > 3 {
		return Coord{}, false
	}
	var v [3]float64
	for i, x := range f {
		n, ok := parseNumber(x)
		if !ok {
			return Coord{}, false
		}
		v[i] = n
	}
	return Coord{Lon: v[0], Lat: v[1], Alt: v[2], Has3: len(f) == 3}, true
}
