// Command refgen writes REFERENCE.md from the issue code table in
// issues.go, so the document can't drift from the code. Run it from the
// repo root:
//
//	go run ./internal/refgen > REFERENCE.md
package main

import (
	"fmt"
	"strings"

	"github.com/softdevstudio/ikml"
)

func main() {
	fmt.Print(`# iKML reference

Every issue iKML reports has a stable code. Scripts can match on the codes;
they don't change between versions. This file is generated from the table
in issues.go by ` + "`go run ./internal/refgen > REFERENCE.md`" + `.

Severity decides the exit code of ` + "`ikml check`" + `: errors give exit status 1,
warnings give 1 only with ` + "`--strict`" + `, and info never does.

`)
	for _, sev := range []ikml.Severity{ikml.SevError, ikml.SevWarning, ikml.SevInfo} {
		title := map[ikml.Severity]string{ikml.SevError: "Errors", ikml.SevWarning: "Warnings", ikml.SevInfo: "Info"}[sev]
		fmt.Printf("## %s\n\n| Code | Meaning | What `ikml clean` does |\n|---|---|---|\n", title)
		for _, c := range ikml.Codes {
			if c.Severity != sev {
				continue
			}
			fix := c.Fix
			if fix == "" {
				fix = "Reported, not changed."
			}
			fmt.Printf("| `%s` | %s | %s |\n", c.Code, esc(c.Meaning), esc(fix))
		}
		fmt.Println()
	}
	fmt.Print(`## What a conversion loses

` + "`ikml convert`" + ` and ` + "`ikml convert --report`" + ` print a loss report for the
file in hand. For KML to GeoJSON, these are the parts it covers:

| KML feature | In iKML's GeoJSON output |
|---|---|
| Styles: line and fill colours, widths, marker colours | Kept as simplestyle-spec properties: ` + "`stroke`, `stroke-width`, `stroke-opacity`, `fill`, `fill-opacity`, `marker-color`" + ` |
| Icon images, label scale, other style details | Lost. GeoJSON has no styling. |
| Folder structure | Flattened. The path is kept in a ` + "`folder`" + ` property. |
| ExtendedData (Data and SchemaData) | Kept as properties. Schema types are lost, so values are strings. |
| Names and descriptions | Kept as ` + "`name`" + ` and ` + "`description`" + `. HTML stays HTML. |
| TimeStamp and TimeSpan | Kept as ` + "`timestamp`" + `, ` + "`begin`" + ` and ` + "`end`" + ` properties. |
| gx:Track times | Kept as a ` + "`coordTimes`" + ` array property, one time per position. |
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
`)
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
