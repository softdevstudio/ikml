// Package fixtures builds the KMZ test fixtures and generated test data.
// Everything here is written by hand for iKML's tests; nothing is copied
// from real files.
package fixtures

import (
	"archive/zip"
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// Entry is one file inside a generated archive.
type Entry struct {
	Name string
	Body []byte
}

// KMZFile is one generated fixture.
type KMZFile struct {
	Dir, Name string
	Entries   []Entry
}

// Data returns the archive bytes.
func (k KMZFile) Data() []byte { return Zip(k.Entries) }

// Zip builds a deterministic zip archive.
func Zip(entries []Entry) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Deflate, Modified: mod})
		if err != nil {
			panic(err)
		}
		w.Write(e.Body)
	}
	zw.Close()
	return b.Bytes()
}

// PNG returns a small valid PNG-looking byte string (a signature plus
// filler). iKML never decodes images, so this is enough.
func PNG(seed int, n int) []byte {
	b := []byte("\x89PNG\r\n\x1a\n")
	r := rand.New(rand.NewSource(int64(seed)))
	for len(b) < n {
		b = append(b, byte(r.Intn(256)))
	}
	return b
}

const djiTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2" xmlns:wpml="http://www.dji.com/wpmz/1.0.2">
  <Document>
    <wpml:author>pilot</wpml:author>
    <wpml:createTime>1760000000000</wpml:createTime>
    <wpml:updateTime>1760000300000</wpml:updateTime>
    <wpml:missionConfig>
      <wpml:flyToWaylineMode>safely</wpml:flyToWaylineMode>
      <wpml:finishAction>goHome</wpml:finishAction>
      <wpml:exitOnRCLost>executeLostAction</wpml:exitOnRCLost>
      <wpml:executeRCLostAction>goBack</wpml:executeRCLostAction>
      <wpml:takeOffSecurityHeight>20</wpml:takeOffSecurityHeight>
      <wpml:globalTransitionalSpeed>10</wpml:globalTransitionalSpeed>
      <wpml:droneInfo>
        <wpml:droneEnumValue>77</wpml:droneEnumValue>
        <wpml:droneSubEnumValue>0</wpml:droneSubEnumValue>
      </wpml:droneInfo>
    </wpml:missionConfig>
    <Folder>
      <wpml:templateType>waypoint</wpml:templateType>
      <wpml:templateId>0</wpml:templateId>
      <wpml:waylineCoordinateSysParam>
        <wpml:coordinateMode>WGS84</wpml:coordinateMode>
        <wpml:heightMode>relativeToStartPoint</wpml:heightMode>
      </wpml:waylineCoordinateSysParam>
      <wpml:autoFlightSpeed>7</wpml:autoFlightSpeed>
      <wpml:globalHeight>60</wpml:globalHeight>
%s    </Folder>
  </Document>
</kml>
`

const djiPlacemark = `      <Placemark>
        <Point>
          <coordinates>
            %s,%s
          </coordinates>
        </Point>
        <wpml:index>%d</wpml:index>
        <wpml:ellipsoidHeight>%d</wpml:ellipsoidHeight>
        <wpml:height>%d</wpml:height>
        <wpml:useGlobalHeight>1</wpml:useGlobalHeight>
        <wpml:useGlobalSpeed>1</wpml:useGlobalSpeed>
        <wpml:useGlobalHeadingParam>1</wpml:useGlobalHeadingParam>
        <wpml:useGlobalTurnParam>1</wpml:useGlobalTurnParam>
      </Placemark>
`

const djiWaylines = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2" xmlns:wpml="http://www.dji.com/wpmz/1.0.2">
  <Document>
    <wpml:missionConfig>
      <wpml:flyToWaylineMode>safely</wpml:flyToWaylineMode>
      <wpml:finishAction>goHome</wpml:finishAction>
    </wpml:missionConfig>
    <Folder>
      <wpml:templateId>0</wpml:templateId>
      <wpml:executeHeightMode>relativeToStartPoint</wpml:executeHeightMode>
      <wpml:waylineId>0</wpml:waylineId>
      <wpml:distance>412.6</wpml:distance>
      <wpml:duration>71.3</wpml:duration>
      <wpml:autoFlightSpeed>7</wpml:autoFlightSpeed>
    </Folder>
  </Document>
</kml>
`

func djiMission() []Entry {
	pts := [][2]string{
		{"8.540210", "47.378120"},
		{"8.541580", "47.378640"},
		{"8.542930", "47.378110"},
		{"8.541600", "47.377420"},
	}
	var pm strings.Builder
	for i, p := range pts {
		fmt.Fprintf(&pm, djiPlacemark, p[0], p[1], i, 60, 60)
	}
	return []Entry{
		{"wpmz/template.kml", []byte(fmt.Sprintf(djiTemplate, pm.String()))},
		{"wpmz/waylines.wpml", []byte(djiWaylines)},
	}
}

const photoTour = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2">
<Document>
  <name>Photo tour</name>
  <Style id="cam">
    <IconStyle><Icon><href>files/camera.png</href></Icon></IconStyle>
  </Style>
  <Placemark>
    <name>Bridge at dawn</name>
    <description><![CDATA[<img src="files/bridge.jpg" width="400"><br>Shot at 06:12.]]></description>
    <styleUrl>#cam</styleUrl>
    <Point><coordinates>-9.1393,38.7223</coordinates></Point>
  </Placemark>
  <Placemark>
    <name>Tram 28</name>
    <description><![CDATA[<img src='files/tram.jpg'>]]></description>
    <styleUrl>#cam</styleUrl>
    <Point><coordinates>-9.1335,38.7139</coordinates></Point>
  </Placemark>
</Document>
</kml>
`

const overlayDoc = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2">
<Document>
  <name>Site plan</name>
  <Style id="marker">
    <IconStyle><Icon><href>files/marker.png</href></Icon></IconStyle>
  </Style>
  <GroundOverlay>
    <name>Plan scan</name>
    <Icon><href>files/plan.png</href></Icon>
    <LatLonBox>
      <north>40.4200</north><south>40.4150</south>
      <east>-3.7000</east><west>-3.7080</west>
    </LatLonBox>
  </GroundOverlay>
  <Placemark>
    <name>Entrance</name>
    <styleUrl>#marker</styleUrl>
    <Point><coordinates>-3.7040,40.4170</coordinates></Point>
  </Placemark>
</Document>
</kml>
`

const localPathDoc = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2">
<Document>
  <name>Shared by email</name>
  <Style id="pin">
    <IconStyle><Icon><href>file:///C:/Users/maria/Desktop/icons/pin.png</href></Icon></IconStyle>
  </Style>
  <Placemark>
    <name>Office</name>
    <description><![CDATA[<img src="/Users/maria/Pictures/office.jpg">]]></description>
    <styleUrl>#pin</styleUrl>
    <Point><coordinates>2.1734,41.3851</coordinates></Point>
  </Placemark>
</Document>
</kml>
`

// KMZ returns every KMZ fixture.
func KMZ() []KMZFile {
	return []KMZFile{
		{"good", "dji-mission.kmz", djiMission()},
		{"good", "photo-tour.kmz", []Entry{
			{"doc.kml", []byte(photoTour)},
			{"files/camera.png", PNG(1, 600)},
			{"files/bridge.jpg", PNG(2, 4000)},
			{"files/tram.jpg", PNG(3, 3000)},
		}},
		{"bad", "overlay-missing-icon.kmz", []Entry{
			{"doc.kml", []byte(overlayDoc)},
			{"files/plan.png", PNG(4, 8000)},
			{"files/old-logo.png", PNG(5, 900)},
		}},
		{"bad", "local-path.kmz", []Entry{
			{"doc.kml", []byte(localPathDoc)},
		}},
		{"bad", "no-kml.kmz", []Entry{
			{"images/readme.png", PNG(6, 300)},
		}},
		{"bad", "unsafe-entries.kmz", []Entry{
			{"doc.kml", []byte(unsafeDoc)},
			{"files/pin.png", PNG(7, 400)},
			{"files/pin.png", PNG(8, 400)},
			{"../../.bashrc", []byte("echo hello\n")},
			{"/etc/cron.d/update", []byte("* * * * * root true\n")},
			{`C:\Windows\System32\update.dll`, []byte("MZ")},
		}},
	}
}

// BigKML returns a KML document with about n coordinates spread over
// placemarks of 1,000 positions each.
func BigKML(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<kml xmlns=\"http://www.opengis.net/kml/2.2\"><Document><name>big</name>\n")
	r := rand.New(rand.NewSource(7))
	per := 1000
	for i := 0; i*per < n; i++ {
		fmt.Fprintf(&b, "<Placemark><name>line %d</name><LineString><coordinates>", i)
		lon, lat := -170+r.Float64()*340, -80+r.Float64()*160
		for j := 0; j < per && i*per+j < n; j++ {
			lon += (r.Float64() - 0.5) * 0.01
			lat += (r.Float64() - 0.5) * 0.01
			fmt.Fprintf(&b, "%.6f,%.6f ", lon, lat)
		}
		b.WriteString("</coordinates></LineString></Placemark>\n")
	}
	b.WriteString("</Document></kml>\n")
	return b.Bytes()
}

// BigKMZ returns a KMZ of about size bytes: a doc.kml with a few thousand
// placemarks plus incompressible photos that the document refers to.
func BigKMZ(size int) []byte {
	var doc bytes.Buffer
	doc.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<kml xmlns=\"http://www.opengis.net/kml/2.2\"><Document>\n")
	entries := []Entry{{Name: "doc.kml"}}
	photo := 1 << 20
	n := size / photo
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("photos/p%04d.jpg", i)
		fmt.Fprintf(&doc, "<Placemark><name>photo %d</name><description><![CDATA[<img src=\"%s\">]]></description><Point><coordinates>%.5f,%.5f</coordinates></Point></Placemark>\n", i, name, -9+float64(i)*0.001, 38+float64(i)*0.001)
		entries = append(entries, Entry{Name: name, Body: PNG(100+i, photo)})
	}
	doc.WriteString("</Document></kml>\n")
	entries[0].Body = doc.Bytes()
	return Zip(entries)
}

// unsafeDoc is an ordinary document in a KMZ that also carries entries
// whose names would unpack outside the folder.
const unsafeDoc = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2">
<Document>
  <name>Site visit</name>
  <Style id="pin">
    <IconStyle><Icon><href>files/pin.png</href></Icon></IconStyle>
  </Style>
  <Placemark>
    <name>Gate</name>
    <styleUrl>#pin</styleUrl>
    <Point><coordinates>-0.1276,51.5072</coordinates></Point>
  </Placemark>
</Document>
</kml>
`
