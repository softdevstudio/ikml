//go:build js && wasm

// Command wasm is iKML compiled to WebAssembly for the browser. It
// registers three functions on the page's global object. Files are passed
// in as Uint8Array and never leave the page.
//
//	ikmlInspect(bytes, name)                -> JSON string: {report, preview, losses}
//	ikmlConvert(bytes, name, target)        -> {bytes: Uint8Array, name, report: JSON string}
//	ikmlClean(bytes, name, target, swapped) -> {bytes: Uint8Array, name, report: JSON string}
//
// On failure each returns an object (or JSON) with an "error" field.
package main

import (
	"bytes"
	"encoding/json"
	"path"
	"strings"
	"syscall/js"

	"github.com/softdevstudio/ikml"
)

// Version is set to the release version by scripts/build-wasm.sh.
var Version = "dev"

func main() {
	g := js.Global()
	g.Set("ikmlVersion", Version)
	g.Set("ikmlInspect", js.FuncOf(inspect))
	g.Set("ikmlConvert", js.FuncOf(func(this js.Value, args []js.Value) any { return transform(args, false) }))
	g.Set("ikmlClean", js.FuncOf(func(this js.Value, args []js.Value) any { return transform(args, true) }))
	select {}
}

func input(args []js.Value) ([]byte, string) {
	data := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(data, args[0])
	name := "file"
	if len(args) > 1 && args[1].Type() == js.TypeString {
		name = args[1].String()
	}
	return data, name
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"internal: ` + err.Error() + `"}`
	}
	return string(b)
}

func inspect(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return jsonString(map[string]string{"error": "no file"})
	}
	data, name := input(args)
	doc, err := ikml.Open(name, data)
	if err != nil {
		return jsonString(map[string]string{"error": err.Error()})
	}
	target := "geojson"
	if doc.Format == ikml.FormatGeoJSON {
		target = "kml"
	}
	return jsonString(map[string]any{
		"version": Version,
		"report":  ikml.Inspect(doc),
		"preview": ikml.Preview(doc, 20000),
		"target":  target,
		"losses":  ikml.LossReport(doc, target),
	})
}

func errObj(msg string) any { return map[string]any{"error": msg} }

func transform(args []js.Value, clean bool) any {
	if len(args) < 3 {
		return errObj("missing arguments")
	}
	data, name := input(args)
	target := strings.ToLower(args[2].String())
	doc, err := ikml.Open(name, data)
	if err != nil {
		return errObj(err.Error())
	}
	var changes []ikml.Change
	if clean {
		swapped := len(args) > 3 && args[3].Truthy()
		doc, changes, err = ikml.Clean(doc, ikml.CleanOptions{FixSwapped: swapped})
		if err != nil {
			return errObj(err.Error())
		}
	}
	losses := ikml.LossReport(doc, target)
	var buf bytes.Buffer
	switch target {
	case "geojson":
		err = ikml.WriteGeoJSON(doc, &buf)
	case "kml":
		err = ikml.WriteKML(doc, &buf)
	case "kmz":
		err = ikml.WriteKMZ(doc, &buf, nil)
	default:
		return errObj("unknown target " + target)
	}
	if err != nil {
		return errObj(err.Error())
	}
	base := strings.TrimSuffix(path.Base(name), path.Ext(name))
	suffix := ""
	if clean {
		suffix = "-clean"
	}
	out := js.Global().Get("Uint8Array").New(buf.Len())
	js.CopyBytesToJS(out, buf.Bytes())
	return map[string]any{
		"bytes":  out,
		"name":   base + suffix + "." + target,
		"report": jsonString(map[string]any{"changes": changes, "losses": losses}),
	}
}
