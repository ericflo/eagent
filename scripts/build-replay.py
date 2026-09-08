#!/usr/bin/env python3
"""Build the portable reducer and matching Go JS runtime reproducibly."""
import gzip
import os
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[1]
target = root / "internal/archive/assets"
target.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, GOOS="js", GOARCH="wasm")
with tempfile.TemporaryDirectory(prefix="eagent-wasm-build-") as temporary:
    wasm = pathlib.Path(temporary) / "replay.wasm"
    subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w -buildid=", "-o", str(wasm), "./cmd/eagent-replay-wasm"], cwd=root, env=env, check=True)
    (target / "replay.wasm.gz").write_bytes(gzip.compress(wasm.read_bytes(), compresslevel=9, mtime=0))
goroot = pathlib.Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
runtime = goroot / "lib/wasm/wasm_exec.js"
if not runtime.exists():
    runtime = goroot / "misc/wasm/wasm_exec.js"
(target / "wasm_exec.js").write_bytes(runtime.read_bytes())
print("Built portable replay assets in", target)
