# iKML reference

Every issue iKML reports has a stable code. Scripts can match on the codes;
they don't change between versions. This file is generated from the table
in issues.go by `go run ./internal/refgen > REFERENCE.md`.

Severity decides the exit code of `ikml check`: errors give exit status 1,
warnings give 1 only with `--strict`, and info never does.

## Errors

| Code | Meaning | What `ikml clean` does |
|---|---|---|
| `xml-invalid` | The KML is not well-formed XML. Most programs refuse the whole file. iKML reports the line and keeps what it could read before the problem. | Reported, not changed. |
| `json-invalid` | The GeoJSON is not valid JSON. iKML reports the line of the first problem. | Reported, not changed. |
| `geojson-structure` | The JSON is valid but does not follow GeoJSON rules, for example a missing "type" or a position that is not an array of numbers. | Reported, not changed. |
| `kmz-invalid` | The file starts like a zip archive but cannot be read as one. | Reported, not changed. |
| `kmz-no-kml` | The KMZ archive has no .kml file inside, so there is nothing to show. | Reported, not changed. |
| `kmz-too-large` | The KMZ expands past iKML's safety limit (512 MiB in total, or an entry that compresses far better than real data does). iKML stops reading to protect your machine. | Reported, not changed. |
| `coord-parse` | A coordinate tuple could not be read as two or three numbers. Viewers usually drop the point or the whole shape. | Not fixed. The tuple is left as it is in KML output and dropped from GeoJSON output. |
| `lon-range` | A longitude is outside -180 to 180. | Reported, not changed. |
| `lat-range` | A latitude is outside -90 to 90. | Reported, not changed. |
| `swapped-latlon` | Every point of a placemark has the first value inside ±90 and the second outside ±90. That can only happen when latitude and longitude were written in the wrong order. | Fixed only with --fix-swapped. |
| `ring-unclosed` | A polygon ring does not end on its first point. KML and GeoJSON both require closed rings. | Fixed: the first point is appended. |
| `ring-too-short` | A polygon ring has fewer than 4 positions (3 corners plus the closing point), so it has no area. | Reported, not changed. |
| `line-too-short` | A line has fewer than 2 positions. | Reported, not changed. |
| `polygon-no-outer` | A Polygon has no outer boundary. | Reported, not changed. |
| `kmz-missing-asset` | The document refers to a file (icon, image, overlay, model) that should be inside the KMZ but is not there. | Reported, not changed. |
| `local-path-ref` | A reference points to a file on the creator's computer (file:///, C:\, /Users/, /home/ and similar). Nobody else can open it. | Reported, not changed. |
| `style-missing` | A placemark uses a style (#id) that is not defined in the document. Viewers fall back to default styling. | Fixed: the broken styleUrl is removed. |

## Warnings

| Code | Meaning | What `ikml clean` does |
|---|---|---|
| `null-island` | A position is exactly 0,0, in the Gulf of Guinea. This is usually a missing value written as zero. | Reported, not changed. |
| `duplicate-points` | A line or ring repeats the same position twice in a row. | Fixed: repeats are removed, as long as the shape keeps enough points. GPS tracks are left alone. |
| `coord-spacing` | Coordinates have spaces next to the commas ("34.8, 32.1"). KML separates points with spaces, so some programs read each number as its own point. | Fixed: the coordinates are rewritten without the extra spaces. |
| `empty-placemark` | A placemark or feature has no geometry, so it will not show on a map. | Kept and reported. |
| `http-ref` | A reference uses plain http://. Browsers and some viewers block it, and the content is not inside the file. | Reported, not changed. |
| `mixed-dimensions` | One shape mixes 2D (lon,lat) and 3D (lon,lat,alt) positions. | Reported, not changed. |
| `geojson-crs` | The GeoJSON has a "crs" member. RFC 7946 removed it, and most tools ignore it and assume WGS 84 degrees. | Fixed when the crs names WGS 84 (CRS84 or EPSG:4326): the member is removed. Other systems are left, because the coordinates would need reprojecting. |
| `geojson-nested-collection` | A GeometryCollection contains another GeometryCollection. RFC 7946 says not to do this and many tools reject it. | Reported, not changed. |
| `old-namespace` | The file uses the KML 2.0 or 2.1 namespace. Newer tools expect 2.2. | Fixed: the namespace is changed to KML 2.2. |
| `no-kml-root` | The XML root is a Document, Folder or Placemark without the <kml> wrapper. | Reported, not changed. |
| `altitude-ignored` | Positions have non-zero altitudes but the altitude mode is clampToGround (the default). Most viewers will draw them on the ground. | Reported, not changed. |
| `large-coordinate-count` | One placemark has more than 100,000 positions. Many web maps slow down or refuse shapes this large. | Reported, not changed. |
| `track-times-mismatch` | A gx:Track has a different number of <when> times and <gx:coord> positions. KML requires one time per position. | Reported, not changed. |
| `duplicate-ids` | Two elements share the same id. Style references and feature lookups may pick the wrong one. | Reported, not changed. |

## Info

| Code | Meaning | What `ikml clean` does |
|---|---|---|
| `no-namespace` | The <kml> root has no namespace. Most programs read it anyway. | Fixed: the KML 2.2 namespace is added. |
| `remote-ref` | A reference points to an https:// address. It works online but the content is not inside the file. | Reported, not changed. |
| `external-file-ref` | A plain .kml file refers to a file next to it on disk (for example an icon). That file must travel with the .kml. | Reported, not changed. |
| `network-link` | The file contains a NetworkLink. Its contents load from elsewhere and are not part of this file. | Reported, not changed. |
| `unused-asset` | A file inside the KMZ is not referenced by the document. | Removed only with --drop-unused. |
| `unknown-element` | An element iKML does not interpret, such as a vendor extension. It is kept in KML output and dropped from GeoJSON output. | Reported, not changed. |
| `winding` | A GeoJSON ring winds the opposite way from RFC 7946 (outer rings counterclockwise, holes clockwise). Most tools accept either. | Fixed in GeoJSON output: rings are rewound. |
| `foreign-members` | The GeoJSON has members outside the specification. They are kept in GeoJSON output and dropped from KML output. | Reported, not changed. |
| `overlay-present` | The file contains image overlays (GroundOverlay, ScreenOverlay or PhotoOverlay). | Reported, not changed. |
| `model-present` | The file contains 3D models. | Reported, not changed. |
| `has-tracks` | The file contains gx:Track GPS tracks with a time for each point. | Reported, not changed. |
| `has-timestamps` | Placemarks carry TimeStamp or TimeSpan values. | Reported, not changed. |
| `empty-folder` | A Folder or Document has nothing inside. | Reported, not changed. |
| `encoding-converted` | The file is not UTF-8. iKML converted it, and cleaned output is written as UTF-8. | Reported, not changed. |

## What a conversion loses

`ikml convert` and `ikml convert --report` print a loss report for the
file in hand. For KML to GeoJSON, these are the parts it covers:

| KML feature | In iKML's GeoJSON output |
|---|---|
| Styles: line and fill colours, widths, marker colours | Kept as simplestyle-spec properties: `stroke`, `stroke-width`, `stroke-opacity`, `fill`, `fill-opacity`, `marker-color` |
| Icon images, label scale, other style details | Lost. GeoJSON has no styling. |
| Folder structure | Flattened. The path is kept in a `folder` property. |
| ExtendedData (Data and SchemaData) | Kept as properties. Schema types are lost, so values are strings. |
| Names and descriptions | Kept as `name` and `description`. HTML stays HTML. |
| TimeStamp and TimeSpan | Kept as `timestamp`, `begin` and `end` properties. |
| gx:Track times | Kept as a `coordTimes` array property, one time per position. |
| altitudeMode, extrude, tessellate | Lost. Altitudes stay in the coordinates. |
| NetworkLinks | Dropped. The report lists their URLs. |
| GroundOverlay, ScreenOverlay, PhotoOverlay | Dropped. The report lists them. |
| 3D models | Dropped. |
| LookAt and Camera views | Dropped. |
| Files inside a KMZ (icons, photos) | Dropped. GeoJSON can't hold files. |
| Elements iKML doesn't interpret | Dropped from GeoJSON, kept in KML and KMZ output. |

For GeoJSON to KML, foreign members are lost and nested property values
become JSON text inside ExtendedData. KMZ to plain KML loses the files
inside the archive; write .kmz to keep them.
