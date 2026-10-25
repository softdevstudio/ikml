package ikml

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Limits for KMZ archives. They protect against zip bombs.
const (
	MaxUncompressed = 512 << 20 // total bytes after decompression
	MaxEntryRatio   = 400       // uncompressed/compressed, for entries over 1 MiB
	MaxEntries      = 10000
)

// ErrUnknownFormat is returned when the data is not KML, KMZ or GeoJSON.
var ErrUnknownFormat = errors.New("not a KML, KMZ or GeoJSON file")

// ErrEmpty is returned for empty input.
var ErrEmpty = errors.New("the file is empty")

// Open reads a KML, KMZ or GeoJSON file. The format is detected from the
// content, not the name; name is used only in reports.
//
// Problems inside the file (broken XML, bad coordinates, missing assets)
// do not cause an error: they are listed by Inspect. Open returns an
// error only when there is nothing to report on, for example empty input
// or a file in some other format.
func Open(name string, data []byte) (*Doc, error) {
	d := newDoc(name, len(data))
	trim := bytes.TrimLeft(data, " \t\r\n")
	switch {
	case len(trim) == 0:
		return nil, ErrEmpty
	case bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("PK\x05\x06")):
		d.Format = FormatKMZ
		d.zipData = data
		openKMZ(d, data)
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		d.Format = FormatKML
		openKML(d, data)
	default:
		b := bytes.TrimPrefix(trim, []byte("\xEF\xBB\xBF"))
		b = bytes.TrimLeft(b, " \t\r\n")
		if len(b) == 0 {
			return nil, ErrEmpty
		}
		switch b[0] {
		case '<':
			d.Format = FormatKML
			openKML(d, data)
		case '{', '[':
			d.Format = FormatGeoJSON
			parseGeoJSON(d, bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))
		default:
			return nil, ErrUnknownFormat
		}
	}
	analyze(d)
	return d, nil
}

func newDoc(name string, size int) *Doc {
	return &Doc{
		Filename:  name,
		Size:      size,
		Styles:    map[string]*Style{},
		StyleMaps: map[string]*StyleMap{},
		Schemas:   map[string]*Schema{},
		issues:    newCollector(),
		ids:       map[string]int{},
		idLoc:     map[string]string{},
		foreign:   map[string]int{},
	}
}

func openKML(d *Doc, data []byte) {
	raw, enc, err := toUTF8(data)
	if err != nil {
		d.issues.add("xml-invalid", Loc{Line: 1}, 0, "%s", err.Error())
		d.fatal = true
		d.Root = &Folder{Kind: "kml"}
		return
	}
	if enc != "" {
		d.encoding = enc
		d.issues.add("encoding-converted", Loc{Line: 1}, 0, "The file is encoded as %s. iKML converted it to UTF-8; cleaned output is UTF-8.", enc)
	}
	d.raw = raw
	if err := parseKML(d, raw, true); err != nil {
		line := 0
		msg := err.Error()
		var se *xml.SyntaxError
		if errors.As(err, &se) {
			line = se.Line
			msg = se.Msg
		}
		d.fatal = true
		if line > 0 {
			d.issues.add("xml-invalid", Loc{Line: line}, 0, "The XML is broken at line %d: %s.%s iKML shows only what it could read before that point.", line, msg, xmlHint(msg))
		} else {
			d.issues.add("xml-invalid", Loc{}, 0, "The XML could not be read: %s.", msg)
		}
	}
}

var encDeclRe = regexp.MustCompile(`^(<\?xml[^>]*?encoding\s*=\s*["'])([^"']+)(["'])`)

// toUTF8 converts UTF-16 and Latin-1/Windows-1252 input to UTF-8 and
// rewrites the XML declaration. enc is "" when no conversion happened.
func toUTF8(data []byte) ([]byte, string, error) {
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		be := data[0] == 0xFE
		body := data[2:]
		if len(body)%2 == 1 {
			body = body[:len(body)-1]
		}
		u := make([]uint16, len(body)/2)
		for i := range u {
			if be {
				u[i] = uint16(body[2*i])<<8 | uint16(body[2*i+1])
			} else {
				u[i] = uint16(body[2*i+1])<<8 | uint16(body[2*i])
			}
		}
		out := []byte(string(utf16.Decode(u)))
		return fixDecl(out), "UTF-16", nil
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	head := data
	if len(head) > 200 {
		head = head[:200]
	}
	m := encDeclRe.FindSubmatch(bytes.TrimLeft(head, " \t\r\n"))
	if m == nil {
		return data, "", nil
	}
	label := strings.ToLower(string(m[2]))
	switch label {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return data, "", nil
	case "iso-8859-1", "latin1", "latin-1", "iso8859-1", "windows-1252", "cp1252":
		var b strings.Builder
		b.Grow(len(data) + len(data)/8)
		for _, c := range data {
			if c >= 0x80 && c < 0xA0 && label != "iso-8859-1" && label != "latin1" && label != "latin-1" && label != "iso8859-1" {
				b.WriteRune(cp1252[c-0x80])
			} else {
				b.WriteRune(rune(c))
			}
		}
		return fixDecl([]byte(b.String())), string(m[2]), nil
	case "utf-16", "utf16":
		return nil, "", fmt.Errorf("the XML declaration says %s but the file has no byte order mark", m[2])
	}
	if !utf8.Valid(data) {
		return nil, "", fmt.Errorf("the file uses the %s encoding, which iKML cannot read yet", m[2])
	}
	return fixDecl(data), string(m[2]), nil
}

func fixDecl(b []byte) []byte {
	lead := len(b) - len(bytes.TrimLeft(b, " \t\r\n"))
	if loc := encDeclRe.FindSubmatchIndex(b[lead:]); loc != nil {
		s, e := loc[4]+lead, loc[5]+lead
		out := make([]byte, 0, len(b))
		out = append(out, b[:s]...)
		out = append(out, "UTF-8"...)
		return append(out, b[e:]...)
	}
	return b
}

var cp1252 = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8D, 'Ž', 0x8F,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9D, 'ž', 'Ÿ',
}

type zipEntry struct {
	f    *zip.File
	data []byte
}

func openKMZ(d *Doc, data []byte) {
	d.Root = &Folder{Kind: "kml"}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		d.issues.add("kmz-invalid", Loc{}, 0, "The file looks like a zip archive but cannot be read: %s.", err.Error())
		d.fatal = true
		return
	}
	if len(zr.File) > MaxEntries {
		d.issues.add("kmz-too-large", Loc{}, 0, "The archive lists %d files, more than the limit of %d.", len(zr.File), MaxEntries)
		d.fatal = true
		return
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if f.UncompressedSize64 > 1<<20 && f.CompressedSize64 > 0 && f.UncompressedSize64/f.CompressedSize64 > MaxEntryRatio {
			d.issues.add("kmz-too-large", Loc{Path: f.Name}, 0, "%s claims to expand from %s to %s, a ratio no real map file has. iKML did not unpack it.", f.Name, humanBytes(int64(f.CompressedSize64)), humanBytes(int64(f.UncompressedSize64)))
			d.fatal = true
			return
		}
	}
	if total > MaxUncompressed {
		d.issues.add("kmz-too-large", Loc{}, 0, "The archive expands to %s, over the %s limit. iKML did not unpack it.", humanBytes(int64(total)), humanBytes(MaxUncompressed))
		d.fatal = true
		return
	}
	// Choose the main KML: doc.kml at the root, else the shallowest .kml.
	var main *zip.File
	best := 1 << 30
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(f.Name, "./")
		if strings.EqualFold(name, "doc.kml") {
			main = f
			break
		}
		if strings.EqualFold(path.Ext(name), ".kml") {
			if depth := strings.Count(name, "/"); depth < best {
				best, main = depth, f
			}
		}
	}
	d.assetData = map[string]*zipEntry{}
	var budget int64 = MaxUncompressed
	read := func(f *zip.File) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, budget+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > budget {
			return nil, errTooLarge
		}
		budget -= int64(len(b))
		return b, nil
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f == main {
			continue
		}
		name := strings.TrimPrefix(f.Name, "./")
		d.Assets = append(d.Assets, Asset{Path: name, Size: int64(f.UncompressedSize64), Compressed: int64(f.CompressedSize64)})
		d.assetData[name] = &zipEntry{f: f}
	}
	if main == nil {
		d.issues.add("kmz-no-kml", Loc{}, 0, "The archive has %d file(s) but no .kml document.", len(d.Assets))
		d.fatal = true
		return
	}
	d.MainPath = strings.TrimPrefix(main.Name, "./")
	b, err := read(main)
	if err != nil {
		if err == errTooLarge {
			d.issues.add("kmz-too-large", Loc{Path: main.Name}, 0, "%s expands past the %s limit. iKML stopped reading it.", main.Name, humanBytes(MaxUncompressed))
		} else {
			d.issues.add("kmz-invalid", Loc{Path: main.Name}, 0, "%s could not be unpacked: %s.", main.Name, err.Error())
		}
		d.fatal = true
		return
	}
	// Check the real size of every asset too: the sizes in the zip
	// directory can lie.
	for i, a := range d.Assets {
		e := d.assetData[a.Path]
		data, err := read(e.f)
		if err == errTooLarge {
			d.issues.add("kmz-too-large", Loc{Path: a.Path}, 0, "%s expands past the %s limit. iKML stopped reading the archive.", a.Path, humanBytes(MaxUncompressed))
			d.fatal = true
			return
		}
		if err != nil {
			d.issues.add("kmz-invalid", Loc{Path: a.Path}, 0, "%s could not be unpacked: %s.", a.Path, err.Error())
			continue
		}
		e.data = data
		d.Assets[i].Size = int64(len(data))
	}
	sort.SliceStable(d.Assets, func(i, j int) bool { return d.Assets[i].Path < d.Assets[j].Path })
	openKML(d, b)
}

var errTooLarge = errors.New("too large")

func xmlHint(msg string) string {
	switch {
	case strings.Contains(msg, "entity") && strings.Contains(msg, "&"):
		return " A plain & in text must be written as &amp; (or the text wrapped in <![CDATA[ ... ]]>)."
	case strings.Contains(msg, "unexpected EOF"):
		return " The file ends in the middle of an element, so it was probably cut off."
	case strings.Contains(msg, "expected element type") || strings.Contains(msg, "unexpected end element"):
		return " An element is closed with the wrong tag, or not closed at all."
	}
	return ""
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
