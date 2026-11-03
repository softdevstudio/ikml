# Changelog

## 0.2.0 (2026-11-04)

GPX and CSV, folder checks and a GitHub Action.

- GPX 1.0 and 1.1 in and out. Waypoints become points, routes lines, and
  each track one placemark with a timed track per segment. Comments,
  sources, symbols, types and links become properties or ExtendedData.
  GPX written from GPX is the source file byte for byte, so Garmin and
  Strava heart rate, cadence, temperature and power stay. Converting to
  GeoJSON, KML or KMZ reports by name what goes. KML and GeoJSON can be
  written as GPX; polygons, fields and styles are reported lost.
- CSV in and out: a header naming a latitude and a longitude column,
  comma, semicolon or tab, decimal commas with semicolons and tabs. CSV
  written from CSV is the source file byte for byte.
- New codes: `gpx-time-invalid`, `gpx-time-backwards`, `gpx-version`,
  `csv-no-coordinates`, `csv-bad-row`, `csv-ragged-row`.
- `clean --fix-swapped` works on GPX and CSV and changes only the lat and
  lon values it fixes. Other fixes don't apply to them: a GPS track's
  repeated points are real readings.
- `ikml check DIR [--json] [--strict]` checks every map file in a folder.
  JSON that isn't GeoJSON and CSV without coordinate columns are listed as
  skipped, folders that can't be read as unreadable.
- `action.yml`: a GitHub Action that annotates each problem on its file
  and line, writes a job summary and fails the check on errors.
- The browser tool opens and writes GPX and CSV.
- Go 1.26 or newer to build.

## 0.1.2 (2026-10-25)

Fixes from the second independent review.

- A second copy of the main document (`doc.kml` twice, or `doc.kml` and
  `./doc.kml`) is `kmz-duplicate-entry`, and cleaned or converted KMZs
  hold it once. Names that differ only in slashes count as the same.
- A file with an unsafe name is never chosen as the main document.

## 0.1.1 (2026-10-25)

Fixes from the independent review of 0.1.0.

- New error `kmz-unsafe-path`: a file inside a KMZ whose name would unpack
  outside the folder (`../`, an absolute path, a drive letter). 0.1.0 only
  noted such a file as unused, and `clean` copied it into the cleaned KMZ.
  Now `clean` and `convert` leave it out, and iKML refuses to write such a
  name into any KMZ.
- New warning `kmz-duplicate-entry`: two files with the same name in one
  KMZ. iKML keeps the first.
- When bundling a .kml's files into a .kmz, a reference written with
  backslashes is checked after they are turned into slashes, so `\x\y.png`
  can't become an entry at the archive's root.
- REFERENCE.md no longer loses element names such as `<kml>` when it is
  shown as Markdown.
- The usage text and README said OUT can be `-`; it can't, since OUT's
  extension picks the format. FILE and IN can be `-` for standard input.

## 0.1.0 (2026-10-25)

First release.

- `ikml inspect` shows what's inside a KML, KMZ or GeoJSON file: folders,
  placemarks, geometry, attribute fields, styles, files inside a KMZ,
  network links, overlays and vendor extensions.
- `ikml check` lists problems with stable codes and exits 1 on errors:
  coordinates that don't parse or are out of range, swapped latitude and
  longitude, open polygon rings, missing KMZ files, references to files on
  the creator's computer, broken style references, and more.
- `ikml convert` writes GeoJSON, KML or KMZ and reports what the conversion
  loses, keeps in another form or changes.
- `ikml clean` applies safe fixes and lists every change.
- A WebAssembly build runs the same engine in the browser, with nothing
  uploaded.
