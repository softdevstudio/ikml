# Changelog

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
