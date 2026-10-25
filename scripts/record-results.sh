#!/usr/bin/env bash
# Runs every test and measurement and records the output in test/results.
# Every number on ikml.com comes from these files.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
res="$root/test/results"
mkdir -p "$res"
cd "$root"
fuzztime="${FUZZTIME:-60s}"

{
  echo "recorded: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "machine: $(uname -srm)"
  echo "cpus: $(getconf _NPROCESSORS_ONLN)"
  echo "go: $(go version)"
  echo "node: $(node --version 2>/dev/null || echo none)"
  echo "version: $(cat VERSION)"
} > "$res/environment.txt"

echo "== tests"
go vet ./...
go test -count=1 -race -v -timeout 60m ./... 2>&1 | tee "$res/tests.txt" | grep -E "^(--- |ok|FAIL)" || true
grep -qE "^(--- FAIL|FAIL)" "$res/tests.txt" && { echo "::error::tests failed"; exit 1; }

echo "== browser build"
scripts/build-wasm.sh > /dev/null
{ ls -l web/ikml.wasm | awk '{print "ikml.wasm bytes: " $5}'; node scripts/wasm-smoke.mjs; } | tee "$res/wasm.txt"

echo "== fuzz ($fuzztime each)"
: > "$res/fuzz.txt"
for f in FuzzOpenKML FuzzOpenKMZ FuzzOpenGeoJSON FuzzParseCoordinates; do
  echo "--- $f" >> "$res/fuzz.txt"
  go test -run '^$' -fuzz "^$f\$" -fuzztime "$fuzztime" . 2>&1 | grep -E "elapsed|PASS|FAIL|panic" | tail -3 >> "$res/fuzz.txt"
done
cat "$res/fuzz.txt"
grep -qE "FAIL|panic" "$res/fuzz.txt" && { echo "::error::fuzzing found a crash"; exit 1; }

echo "== every fixture"
bin="$(mktemp -d)/ikml"
go build -o "$bin" ./cmd/ikml
: > "$res/corpus-report.txt"
for f in testdata/good/* testdata/bad/*; do
  echo "\$ ikml check $f" >> "$res/corpus-report.txt"
  "$bin" check "$f" >> "$res/corpus-report.txt" 2>&1 || echo "(exit $?)" >> "$res/corpus-report.txt"
  echo >> "$res/corpus-report.txt"
done
echo "$(ls testdata/good | wc -l) good and $(ls testdata/bad | wc -l) broken fixtures checked"

echo "== timing"
python3 - "$bin" "$res/bench.json" <<'PY'
import json, os, random, resource, subprocess, sys, tempfile, time, zipfile
bin, out = sys.argv[1:]
d = tempfile.mkdtemp()
rnd = random.Random(1)
# 1,000 placemarks with 1,000-point tracks: 1,000,000 positions.
kml = os.path.join(d, "big.kml")
with open(kml, "w") as f:
    f.write('<?xml version="1.0" encoding="UTF-8"?>\n<kml xmlns="http://www.opengis.net/kml/2.2"><Document>\n')
    for i in range(1000):
        lon, lat = rnd.uniform(-170, 170), rnd.uniform(-80, 80)
        pts = []
        for _ in range(1000):
            lon += rnd.uniform(-0.001, 0.001); lat += rnd.uniform(-0.001, 0.001)
            pts.append(f"{lon:.6f},{lat:.6f},{rnd.uniform(0, 500):.1f}")
        f.write(f'<Placemark><name>Track {i}</name><LineString><altitudeMode>absolute</altitudeMode><coordinates>{" ".join(pts)}</coordinates></LineString></Placemark>\n')
    f.write("</Document></kml>\n")
kmz = os.path.join(d, "big.kmz")
with zipfile.ZipFile(kmz, "w", zipfile.ZIP_DEFLATED) as z:
    z.write(kml, "doc.kml")
def run(args):
    best = None
    for _ in range(3):
        t = time.perf_counter()
        subprocess.run([bin] + args, check=False, capture_output=True)
        e = time.perf_counter() - t
        best = e if best is None else min(best, e)
    return round(best, 2)
r = {
  "kml_bytes": os.path.getsize(kml), "kmz_bytes": os.path.getsize(kmz), "positions": 1000000, "placemarks": 1000,
  "check_kml_seconds": run(["check", kml]),
  "check_kmz_seconds": run(["check", kmz]),
  "convert_kml_to_geojson_seconds": run(["convert", kml, os.path.join(d, "out.geojson")]),
  "peak_memory_mb_all_runs": round(resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss / 1024),
  "note": "best of 3 runs each; peak memory is the largest of all runs",
}
json.dump(r, open(out, "w"), indent=2)
print(json.dumps(r, indent=2))
PY
echo "recorded in $res"
