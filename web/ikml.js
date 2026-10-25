// iKML in the browser. Loads ikml.wasm and renders an inspector into the
// element with id "ikml-app". Files are read with FileReader and handed to
// WebAssembly in memory: nothing is uploaded, and this script makes no
// network requests apart from fetching ikml.wasm itself.
(function () {
  "use strict";
  var script = document.currentScript;
  var base = script ? script.src.replace(/[^\/]*$/, "") : "";

  var css = [
    ".ikml{font:15px/1.5 system-ui,-apple-system,'Segoe UI',sans-serif;color:inherit}",
    ".ikml *{box-sizing:border-box}",
    ".ikml-drop{border:2px dashed #8aa0b8;border-radius:12px;padding:34px 18px;text-align:center;cursor:pointer;background:rgba(127,150,180,.06)}",
    ".ikml-drop.over{border-color:#0e7c7b;background:rgba(14,124,123,.08)}",
    ".ikml-drop strong{font-size:1.1em}",
    ".ikml-drop small{display:block;margin-top:6px;opacity:.75}",
    ".ikml-out{margin-top:22px}",
    ".ikml-h{font-weight:700;font-size:1.05em;margin:22px 0 8px}",
    ".ikml-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:8px}",
    ".ikml-stat{border:1px solid rgba(127,150,180,.35);border-radius:8px;padding:8px 10px}",
    ".ikml-stat b{display:block;font-size:1.25em}",
    ".ikml-stat span{font-size:.85em;opacity:.75}",
    ".ikml-verdict{padding:10px 14px;border-radius:8px;font-weight:600;margin-top:12px}",
    ".ikml-verdict.bad{background:rgba(190,62,52,.12);color:#b03a2e}",
    ".ikml-verdict.warn{background:rgba(200,140,20,.14);color:#9a6a00}",
    ".ikml-verdict.good{background:rgba(46,140,80,.12);color:#23733f}",
    ".ikml-issue{border-left:4px solid #8aa0b8;padding:6px 10px;margin:6px 0;background:rgba(127,150,180,.06);border-radius:0 6px 6px 0}",
    ".ikml-issue.error{border-color:#be3e34}.ikml-issue.warning{border-color:#c8901a}.ikml-issue.info{border-color:#5b7ea6}",
    ".ikml-issue code{font-size:.85em;opacity:.8}",
    ".ikml-issue .loc{font-size:.85em;opacity:.7}",
    ".ikml-loss{display:grid;grid-template-columns:5.5em 1fr;gap:4px 10px;margin:6px 0}",
    ".ikml-tag{font-size:.8em;font-weight:700;text-transform:uppercase;padding:2px 6px;border-radius:4px;text-align:center;height:fit-content}",
    ".ikml-tag.lost{background:rgba(190,62,52,.14);color:#b03a2e}.ikml-tag.kept{background:rgba(46,140,80,.14);color:#23733f}.ikml-tag.changed{background:rgba(200,140,20,.16);color:#9a6a00}",
    ".ikml-map{width:100%;height:auto;border:1px solid rgba(127,150,180,.35);border-radius:8px;background:rgba(127,150,180,.05)}",
    ".ikml-tree{font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre;overflow-x:auto;padding:10px;border-radius:8px;background:rgba(127,150,180,.08)}",
    ".ikml-actions{display:flex;flex-wrap:wrap;gap:8px;margin-top:8px}",
    ".ikml-actions button{font:inherit;padding:7px 12px;border-radius:8px;border:1px solid #0e7c7b;background:transparent;color:inherit;cursor:pointer}",
    ".ikml-actions button:hover{background:rgba(14,124,123,.1)}",
    ".ikml-actions label{display:flex;align-items:center;gap:6px;font-size:.9em}",
    ".ikml-note{font-size:.85em;opacity:.75;margin-top:6px}"
  ].join("\n");

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  var ready = null;
  function loadWasm() {
    if (ready) return ready;
    ready = new Promise(function (resolve, reject) {
      function go() {
        var g = new window.Go();
        WebAssembly.instantiateStreaming(fetch(base + "ikml.wasm"), g.importObject)
          .catch(function () {
            return fetch(base + "ikml.wasm").then(function (r) { return r.arrayBuffer(); })
              .then(function (b) { return WebAssembly.instantiate(b, g.importObject); });
          })
          .then(function (res) { g.run(res.instance); resolve(); }, reject);
      }
      if (window.Go) return go();
      var s = document.createElement("script");
      s.src = base + "wasm_exec.js";
      s.onload = go;
      s.onerror = reject;
      document.head.appendChild(s);
    });
    return ready;
  }

  function download(bytes, name) {
    var url = URL.createObjectURL(new Blob([bytes]));
    var a = document.createElement("a");
    a.href = url; a.download = name;
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 2000);
  }

  function svgMap(shapes) {
    var NS = "http://www.w3.org/2000/svg";
    var minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    shapes.forEach(function (s) { s.c.forEach(function (r) { r.forEach(function (p) {
      if (p[0] < minX) minX = p[0]; if (p[0] > maxX) maxX = p[0];
      if (p[1] < minY) minY = p[1]; if (p[1] > maxY) maxY = p[1];
    }); }); });
    var W = 800, H = 420, pad = 20;
    var svg = document.createElementNS(NS, "svg");
    svg.setAttribute("viewBox", "0 0 " + W + " " + H);
    svg.setAttribute("class", "ikml-map");
    svg.setAttribute("role", "img");
    svg.setAttribute("aria-label", "Preview of the shapes in the file");
    if (!isFinite(minX)) return svg;
    // Plain equirectangular view, with longitude scaled by the latitude so
    // shapes keep their proportions near the middle of the data.
    var midLat = (minY + maxY) / 2 * Math.PI / 180;
    var kx = Math.max(Math.cos(midLat), 0.05);
    var spanX = Math.max((maxX - minX) * kx, 1e-9), spanY = Math.max(maxY - minY, 1e-9);
    var scale = Math.min((W - 2 * pad) / spanX, (H - 2 * pad) / spanY);
    var offX = (W - spanX * scale) / 2, offY = (H - spanY * scale) / 2;
    function pt(p) { return [offX + (p[0] - minX) * kx * scale, H - (offY + (p[1] - minY) * scale)]; }
    shapes.forEach(function (s) {
      var e;
      if (s.k === "point") {
        var q = pt(s.c[0][0]);
        e = document.createElementNS(NS, "circle");
        e.setAttribute("cx", q[0].toFixed(1)); e.setAttribute("cy", q[1].toFixed(1)); e.setAttribute("r", 4);
        e.setAttribute("fill", "#0e7c7b");
      } else {
        e = document.createElementNS(NS, "path");
        var d = s.c.map(function (r) {
          return r.map(function (p, i) { var q = pt(p); return (i ? "L" : "M") + q[0].toFixed(1) + " " + q[1].toFixed(1); }).join("") + (s.k === "polygon" ? "Z" : "");
        }).join("");
        e.setAttribute("d", d);
        e.setAttribute("fill", s.k === "polygon" ? "rgba(14,124,123,.18)" : "none");
        e.setAttribute("fill-rule", "evenodd");
        e.setAttribute("stroke", s.k === "polygon" ? "#0e7c7b" : "#1e345a");
        e.setAttribute("stroke-width", 2);
        e.setAttribute("vector-effect", "non-scaling-stroke");
      }
      if (s.n) { var t = document.createElementNS(NS, "title"); t.textContent = s.n; e.appendChild(t); }
      svg.appendChild(e);
    });
    return svg;
  }

  function stat(grid, value, label) {
    var s = el("div", "ikml-stat");
    s.appendChild(el("b", "", String(value)));
    s.appendChild(el("span", "", label));
    grid.appendChild(s);
  }

  function treeText(nodes) {
    return (nodes || []).map(function (n) {
      var g = n.geometries || {};
      var parts = Object.keys(g).map(function (k) { return g[k] + " " + k; });
      var sum = n.placemarks ? n.placemarks + " placemark" + (n.placemarks === 1 ? "" : "s") + (parts.length ? "; " + parts.join(", ") : "") : "";
      return new Array((n.depth || 0) + 1).join("  ") + n.kind + (n.name ? " " + n.name : "") + (sum ? ": " + sum : "");
    }).join("\n");
  }

  function render(out, res, file, bytes) {
    out.innerHTML = "";
    if (res.error) {
      out.appendChild(el("div", "ikml-verdict bad", "Can't read " + file.name + ": " + res.error));
      return;
    }
    var r = res.report;
    var v = r.errors ? ["bad", r.errors + " error" + (r.errors === 1 ? "" : "s") + " that will cause trouble on import"]
      : r.warnings ? ["warn", "No errors. " + r.warnings + " warning" + (r.warnings === 1 ? "" : "s") + " worth a look"]
      : ["good", "No errors or warnings. This file should import cleanly"];
    out.appendChild(el("div", "ikml-verdict " + v[0], file.name + ": " + v[1] + "."));

    out.appendChild(el("div", "ikml-h", "What's inside"));
    var grid = el("div", "ikml-grid");
    var st = r.stats || {};
    stat(grid, (r.format || "").toUpperCase(), Math.round((r.size || 0) / 1024) + " KB");
    stat(grid, st.placemarks || 0, "placemarks or features");
    stat(grid, st.folders || 0, "folders");
    var g = st.geometries || {};
    stat(grid, Object.keys(g).map(function (k) { return g[k] + " " + k; }).join(", ") || "none", "geometry");
    if (r.assets && r.assets.length) stat(grid, r.assets.length, "files inside the KMZ");
    if (r.fields && r.fields.length) stat(grid, r.fields.length, "attribute fields");
    out.appendChild(grid);

    if (res.preview && res.preview.length) {
      out.appendChild(el("div", "ikml-h", "Preview"));
      out.appendChild(svgMap(res.preview));
      out.appendChild(el("div", "ikml-note", "Drawn from the coordinates alone, with no map tiles, so nothing is fetched from the internet. Hover a shape for its name."));
    }

    var tree = treeText(r.tree);
    if (tree) {
      out.appendChild(el("div", "ikml-h", "Structure"));
      out.appendChild(el("div", "ikml-tree", tree));
    }

    out.appendChild(el("div", "ikml-h", "Problems and notes"));
    if (!r.issues || !r.issues.length) out.appendChild(el("div", "ikml-note", "Nothing to report."));
    (r.issues || []).forEach(function (i) {
      var d = el("div", "ikml-issue " + i.severity);
      var head = el("div");
      head.appendChild(el("strong", "", i.severity.toUpperCase() + " "));
      head.appendChild(el("code", "", i.code));
      d.appendChild(head);
      d.appendChild(el("div", "", i.message));
      if (i.location || i.line) d.appendChild(el("div", "loc", (i.location || "") + (i.line ? " (line " + i.line + ")" : "")));
      out.appendChild(d);
    });

    if (res.losses && res.losses.length) {
      out.appendChild(el("div", "ikml-h", "If you convert it to " + res.target.toUpperCase()));
      res.losses.forEach(function (l) {
        var row = el("div", "ikml-loss");
        row.appendChild(el("div", "ikml-tag " + l.outcome, l.outcome));
        var body = el("div");
        body.appendChild(el("strong", "", l.what + (l.count ? " (" + l.count + ")" : "") + ". "));
        body.appendChild(document.createTextNode(l.detail || ""));
        if (l.items && l.items.length) body.appendChild(el("div", "ikml-note", l.items.join(", ")));
        row.appendChild(body);
        out.appendChild(row);
      });
    }

    out.appendChild(el("div", "ikml-h", "Download"));
    var acts = el("div", "ikml-actions");
    var swap = el("input"); swap.type = "checkbox";
    var targets = r.format === "geojson" ? ["kml", "kmz", "geojson"] : ["geojson", "kml", "kmz"];
    targets.forEach(function (t) {
      var b = el("button", "", "Cleaned " + t.toUpperCase());
      b.onclick = function () {
        var o = window.ikmlClean(bytes, file.name, t, swap.checked);
        if (o.error) { alert(o.error); return; }
        download(o.bytes, o.name);
      };
      acts.appendChild(b);
    });
    var lab = el("label");
    lab.appendChild(swap);
    lab.appendChild(document.createTextNode("also swap latitude and longitude where they're reversed"));
    acts.appendChild(lab);
    out.appendChild(acts);
    out.appendChild(el("div", "ikml-note", "A cleaned copy closes open rings, removes repeated points, fixes spacing in coordinates, drops broken style references and updates old KML namespaces. Your original file is not changed."));
  }

  function start() {
    var root = document.getElementById("ikml-app");
    if (!root) return;
    var st = document.createElement("style"); st.textContent = css; document.head.appendChild(st);
    root.className = "ikml";
    var drop = el("div", "ikml-drop");
    drop.setAttribute("role", "button");
    drop.setAttribute("tabindex", "0");
    drop.appendChild(el("strong", "", "Drop a KML, KMZ or GeoJSON file here, or click to choose one"));
    drop.appendChild(el("small", "", "The file is read inside your browser. It is never uploaded."));
    var input = el("input"); input.type = "file"; input.accept = ".kml,.kmz,.geojson,.json"; input.hidden = true;
    var out = el("div", "ikml-out");
    root.appendChild(drop); root.appendChild(input); root.appendChild(out);

    function handle(file) {
      if (!file) return;
      out.innerHTML = "";
      out.appendChild(el("div", "ikml-note", "Reading " + file.name + "..."));
      Promise.all([loadWasm(), file.arrayBuffer()]).then(function (v) {
        var bytes = new Uint8Array(v[1]);
        var res = JSON.parse(window.ikmlInspect(bytes, file.name));
        render(out, res, file, bytes);
      }, function (e) {
        out.innerHTML = "";
        out.appendChild(el("div", "ikml-verdict bad", "The inspector couldn't start: " + e));
      });
    }
    drop.onclick = function () { input.click(); };
    drop.onkeydown = function (e) { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); input.click(); } };
    input.onchange = function () { handle(input.files[0]); input.value = ""; };
    drop.ondragover = function (e) { e.preventDefault(); drop.classList.add("over"); };
    drop.ondragleave = function () { drop.classList.remove("over"); };
    drop.ondrop = function (e) { e.preventDefault(); drop.classList.remove("over"); handle(e.dataTransfer.files[0]); };
    loadWasm();
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
