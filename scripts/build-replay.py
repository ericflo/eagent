#!/usr/bin/env python3
"""Build the portable reducer and matching Go JS runtime reproducibly."""
import gzip
import argparse
import os
import pathlib
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--check", action="store_true", help="fail if committed replay assets differ from this source/toolchain")
args = parser.parse_args()

def update(path, raw):
    if path.exists() and path.read_bytes() == raw:
        return
    if args.check:
        raise SystemExit(f"Stale replay asset: {path}. Run python3 scripts/build-replay.py")
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as temporary:
        temporary.write(raw)
        name = temporary.name
    os.chmod(name, 0o644)
    os.replace(name, path)

root = pathlib.Path(__file__).resolve().parents[1]
target = root / "internal/archive/assets"
target.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, GOOS="js", GOARCH="wasm")
with tempfile.TemporaryDirectory(prefix="eagent-wasm-build-") as temporary:
    wasm = pathlib.Path(temporary) / "replay.wasm"
    subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w -buildid=", "-o", str(wasm), "./cmd/eagent-replay-wasm"], cwd=root, env=env, check=True)
    update(target / "replay.wasm.gz", gzip.compress(wasm.read_bytes(), compresslevel=9, mtime=0))
goroot = pathlib.Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
runtime = goroot / "lib/wasm/wasm_exec.js"
if not runtime.exists():
    runtime = goroot / "misc/wasm/wasm_exec.js"
update(target / "wasm_exec.js", runtime.read_bytes())
print("Verified" if args.check else "Built", "portable replay assets in", target)
