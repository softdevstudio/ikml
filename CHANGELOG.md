# Changelog

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
