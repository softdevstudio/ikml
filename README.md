# iKML

**Understand a geographic file before you import it.** iKML opens KML, KMZ
and GeoJSON files, shows what's inside, flags what's broken, tells you
exactly what a conversion to another format will lose, and writes a cleaned
copy with a list of every change.

It ships as one small command-line program, as a Go library, and as a
WebAssembly build that runs the same engine in a web page, where files are
read in the browser and never uploaded. Open source under the Apache License
2.0. Website and in-browser inspector: [ikml.com](https://ikml.com).

```
$ ikml check survey.kmz
survey.kmz (kmz, 9.5 KiB)

  ERROR   kmz-missing-asset  Style 'marker' (line 6)
          The icon files/marker.png should be inside the KMZ but is not there.
  INFO    unused-asset  files/old-logo.png
          files/old-logo.png (900 bytes) is inside the KMZ but nothing in the
          document refers to it.
  INFO    overlay-present
          The file has 1 GroundOverlay.

1 error(s), 0 warning(s), 2 note(s).
```

## Why

KML is how Google Earth, Google My Maps, drone mission planners, surveying
apps and GIS exports hand over places, routes and areas. KMZ is the same
thing zipped with its icons and images. GeoJSON is what most web maps and
data tools want. People pass these files around constantly, and the import
is where things go quietly wrong:

- points land in the ocean because latitude and longitude were written in
  the wrong order
- polygons vanish because a ring doesn't close
- icons and photos are missing because the KMZ points to files on the
  creator's own computer (`file:///C:/Users/...`)
- styles, folders, attribute data and timestamps disappear in a conversion,
  and nothing tells you which

iKML reads the file the way an importer would, and tells you all of that
first.

## Install

| System | Download |
|---|---|
| Linux, x86-64 | [ikml_linux_amd64.tar.gz](https://github.com/softdevstudio/ikml/releases/latest/download/ikml_linux_amd64.tar.gz) |
| Linux, ARM64 | [ikml_linux_arm64.tar.gz](https://github.com/softdevstudio/ikml/releases/latest/download/ikml_linux_arm64.tar.gz) |
| macOS, Apple silicon | [ikml_darwin_arm64.tar.gz](https://github.com/softdevstudio/ikml/releases/latest/download/ikml_darwin_arm64.tar.gz) |
| macOS, Intel | [ikml_darwin_amd64.tar.gz](https://github.com/softdevstudio/ikml/releases/latest/download/ikml_darwin_amd64.tar.gz) |
| Windows, x86-64 | [ikml_windows_amd64.zip](https://github.com/softdevstudio/ikml/releases/latest/download/ikml_windows_amd64.zip) |
| Browser version | [ikml-web.zip](https://github.com/softdevstudio/ikml/releases/latest/download/ikml-web.zip) |

Checksums are in [SHA256SUMS](https://github.com/softdevstudio/ikml/releases/latest/download/SHA256SUMS).
With Go 1.24 or later: `go install github.com/softdevstudio/ikml/cmd/ikml@latest`.

## Commands

```
ikml inspect FILE [--json]            show what is inside the file
ikml check FILE [--json] [--strict]   list problems; exit 1 if errors found
ikml convert IN OUT [--report] [--json]
                                      convert by OUT extension (.geojson, .json, .kml, .kmz)
ikml clean IN OUT [--fix-swapped] [--drop-unused] [--report] [--json]
                                      apply safe fixes and write a cleaned copy
ikml codes [--json]                   list every issue code
ikml version                          print the version
```

Exit status: 0 no errors, 1 errors found (or warnings with `--strict`), 2 a
usage or read error. `OUT` can be `-` for standard output.

### inspect

```
$ ikml inspect field-trip.kml
field-trip.kml
  Format:       KML, 3.4 KiB
  Namespace:    http://www.opengis.net/kml/2.2
  Placemarks:   3
  Geometry:     1 LineString, 2 Point
  Coordinates:  6
  Extent:       lon 35.2134 to 35.2291, lat 31.7767 to 31.7801
  Folders:      3
  Styles:       4 Style, 2 StyleMap

Structure:
    Document Field trip 2026
      Folder Meeting points: 2 placemark(s); 2 Point
      Folder Routes: 1 placemark(s); 1 LineString

Attribute fields:
  contact                  2 placemark(s) (Data)
  capacity                 2 placemark(s) (Data)
  length_km                1 placemark(s) (Data)

Styles:
  Style    #icon-1899-0288D1-normal used by 0: icon https://www.gstatic.com/mapspro/images/stock/503-wht-blank_maps.png, label
  Style    #icon-1899-0288D1-highlight used by 0: icon https://www.gstatic.com/mapspro/images/stock/503-wht-blank_maps.png, label
  StyleMap #icon-1899-0288D1     used by 2: normal #icon-1899-0288D1-normal, highlight #icon-1899-0288D1-highlight
  Style    #line-E65100-3000-normal used by 0: line
  Style    #line-E65100-3000-highlight used by 0: line
  StyleMap #line-E65100-3000     used by 1: normal #line-E65100-3000-normal, highlight #line-E65100-3000-highlight

Issues:
  INFO    remote-ref  Style 'icon-1899-0288D1-normal' (line 12)
          2 references to files on www.gstatic.com, first here. They load from the
          internet and are not inside the file.

0 error(s), 0 warning(s), 1 note(s).
```

### check

Lists only the problems, with a stable code for each, and sets the exit
status. That makes it usable as a gate in a data pipeline:

```
$ ikml check office.kmz
office.kmz (kmz, 468 bytes)

  ERROR   local-path-ref  Style 'pin' (line 6)
          The icon file:///C:/Users/maria/Desktop/icons/pin.png points to a file
          on the creator's computer. Nobody else can open it.
  ERROR   local-path-ref  Placemark 'Office' (line 10)
          The image in a description /Users/maria/Pictures/office.jpg points to a
          file on the creator's computer. Nobody else can open it.

2 error(s), 0 warning(s), 0 note(s).
```

[REFERENCE.md](REFERENCE.md) lists every code, what it means and whether
`clean` fixes it.

### convert

Writes GeoJSON, KML or KMZ, chosen by the output file's extension, and with
`--report` says what the conversion loses, keeps in another form or changes:

```
$ ikml convert site.kml site.geojson --report
Converted site.kml (kml) to site.geojson (geojson, 1003 bytes).

What changes from kml to geojson:

  [CHANGED] Styles and colors (4)
    GeoJSON has no styling. 4 placemarks use 1 shared style. iKML keeps line
    color and width, fill color and opacity, and marker color as
    simplestyle-spec properties (stroke, stroke-width, stroke-opacity, fill,
    fill-opacity, marker-color) for 2 of them. Mapbox, geojson.io and GitHub
    read these; most other tools ignore them.

  [LOST] Style details with no GeoJSON equivalent (4)
    These have no simplestyle-spec property and are dropped: icon images (4
    placemarks), icon scale (4), icon hotspots (4), highlight styles from 1
    StyleMap.

  [KEPT] Folder structure (3)
    GeoJSON is a flat list of features, so 3 folders are flattened. iKML
    keeps each placemark's folder path in a "folder" property (for example
    "Survey / Gates"), and converting back to KML rebuilds the folders.

  [KEPT] Descriptions (1)
    1 description is kept in a "description" property.

  [LOST] Tessellate (2)
    Lines that follow the terrain are a KML display setting with no GeoJSON
    equivalent.

  [LOST] Saved views (LookAt, Camera) (2)
    Camera positions for flying to a place are dropped.

  [LOST] Elements iKML does not interpret (1)
    These elements, often vendor extensions, have no GeoJSON form and are
    dropped. KML or KMZ output keeps them.
      - gx:CascadingStyle (1)

  [CHANGED] Polygon rings (1)
    GeoJSON output has closed rings in RFC 7946 order (outer rings
    counterclockwise). 1 ring is rewound. The points themselves do not move.

Before: 0 error(s), 1 warning(s). After: 0 error(s), 0 warning(s).
```

iKML's GeoJSON keeps what it can: styles as simplestyle-spec properties,
the folder path in a `folder` property, ExtendedData as properties, times as
`timestamp`, `begin`, `end` and per-point `coordTimes`. Converting back to
KML rebuilds the folders.

### clean

Applies safe fixes only and lists each one: closes open rings, removes
repeated points, fixes spaces in coordinate lists, drops broken style
references, upgrades old KML namespaces and adds a missing one, and removes
a WGS 84 `crs` member from GeoJSON. `--fix-swapped` also swaps latitude and
longitude where they're certainly reversed; it's opt-in because a wrong
guess moves data far. `--drop-unused` removes files a KMZ holds but never
uses. The original file is never changed.

```
$ ikml clean fields.kml fields-clean.kml --report
Cleaned fields.kml (kml) to fields-clean.kml (kml, 775 bytes).

Changes (1):
  ring-unclosed      Closed the outer ring by repeating its first point at the end.
                     at Placemark 'Field 7' (line 6)

Nothing is lost converting kml to kml.

Before: 3 error(s), 0 warning(s). After: 2 error(s), 0 warning(s).
```

The two errors that remain are a ring with only three positions and a line
with one, which no safe fix can repair.

## In the browser

`web/` holds the browser version: `ikml.wasm` (the same engine compiled to
WebAssembly), Go's `wasm_exec.js`, and `ikml.js`, which draws the inspector
into any element with `id="ikml-app"`. The file is read with the browser's
File API and handed to WebAssembly in memory. Nothing is uploaded, and the
page makes no network requests apart from loading `ikml.wasm`; the preview
is drawn from the coordinates alone, without map tiles.

```sh
scripts/build-wasm.sh          # builds web/ikml.wasm and copies wasm_exec.js
python3 -m http.server -d web  # then open http://localhost:8000
```

The release also includes `ikml-web.zip`, ready to drop on any static host.

## As a Go library

```go
doc, err := ikml.Open("survey.kmz", data)  // KML, KMZ or GeoJSON, detected by content
report := ikml.Inspect(doc)                // structure, stats, issues
losses := ikml.LossReport(doc, "geojson")  // what a conversion drops or changes
clean, changes, err := ikml.Clean(doc, ikml.CleanOptions{})
err = ikml.WriteGeoJSON(clean, w)          // or WriteKML, WriteKMZ
```

Standard library only. Files are read in memory with hard limits: a KMZ may
expand to 512 MiB in total, and an entry that compresses far better than
real data is refused as a zip bomb.

## How it was tested

The test files in [testdata](testdata) are written by hand, laid out the
way real exporters write them: Google Earth Pro, Google My Maps, a DJI drone
mission KMZ, ArcGIS and QGIS exports, a GPS track, a photo tour KMZ. Every
good file must give no errors. Every broken file must give the exact issue
codes it was made to trigger. Every valid file goes KML to GeoJSON and back
with each coordinate unchanged, and so do thousands of randomly generated
documents. Cleaning twice gives the same file as cleaning once. Fuzzers feed
damaged KML, KMZ, GeoJSON and coordinate text to the parsers, and a zip bomb
must be refused. The browser build runs the same checks in Node.

The recorded results are in [test/results](test/results). To record them
again: `scripts/record-results.sh`.

## License

Apache License 2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE) and
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
