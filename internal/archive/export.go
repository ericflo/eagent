// Package archive produces portable, immutable session websites. Source JSONL
// is copied byte-for-byte through the last complete record, never reserialized.
package archive

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/projection"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
	webstatic "github.com/ericflo/eagent/internal/web/static"
)

//go:embed assets/*
var assets embed.FS

type Export struct {
	Dir      string
	Manifest artifact.Manifest
	Detail   *projection.SessionDetail
}

func (e *Export) Close() { _ = os.RemoveAll(e.Dir) }

// Snapshot creates a private staging directory. Call Close after publication
// or move it to a new destination to retain an offline export.
func Snapshot(ctx context.Context, project, ref, version string) (*Export, error) {
	return SnapshotIn(ctx, project, ref, version, "")
}

// SnapshotIn keeps a pending publication in a persistent private directory.
func SnapshotIn(ctx context.Context, project, ref, version, parent string) (*Export, error) {
	info, err := store.Resolve(store.Root(project), ref)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "eagent-artifact-")
	if err != nil {
		return nil, err
	}
	out := &Export{Dir: dir}
	ok := false
	defer func() {
		if !ok {
			out.Close()
		}
	}()
	out.Manifest = artifact.Manifest{Format: artifact.Format, Producer: artifact.Producer{Name: "eagent", Version: version}, Entrypoint: "index.html", SettingsEntrypoint: "settings/index.html", CapturedAt: time.Now().UTC(), Dataset: map[string]any{"format": "eagent.session-jsonl/v1", "session_id": info.ID, "source_policy": "original committed JSONL; no credential or process-control files"}, Files: []artifact.File{}}
	out.Manifest.Viewer = map[string]any{"id": "eagent-session-explorer", "version": version, "fingerprint": ViewerFingerprint()}
	root, err := os.OpenRoot(info.Path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entries, err := os.ReadDir(info.Path)
	if err != nil {
		return nil, err
	}
	st := state.New()
	var logs []string
	var sourceBytes int64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") || entry.IsDir() {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("session log is not a regular file: %s", entry.Name())
		}
		name := "sessions/" + entry.Name()
		f, err := root.Open(entry.Name())
		if err != nil {
			return nil, err
		}
		stat, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if stat.Size() > artifact.MaxFileBytes {
			f.Close()
			return nil, fmt.Errorf("session file exceeds archive limit")
		}
		var committed int64
		line := 0
		err = out.add(name, "source", "application/x-ndjson", func(dst io.Writer) error {
			scan := bufio.NewScanner(io.LimitReader(f, stat.Size()))
			scan.Buffer(make([]byte, 65536), 16<<20)
			scan.Split(committedLines)
			for scan.Scan() {
				if err := ctx.Err(); err != nil {
					return err
				}
				raw := scan.Bytes()
				line++
				if len(bytes.TrimSpace(raw)) > 0 {
					var ev event.Event
					if err := json.Unmarshal(raw, &ev); err != nil {
						return fmt.Errorf("%s:%d: corrupt event: %w", entry.Name(), line, err)
					}
					if ev.Seq <= st.LastSeq() {
						return fmt.Errorf("%s:%d: non-increasing event sequence", entry.Name(), line)
					}
					ev.Source = event.Source{File: entry.Name(), Line: line}
					st.Apply(ev)
				}
				if _, err := dst.Write(raw); err != nil {
					return err
				}
				committed += int64(len(raw))
			}
			return scan.Err()
		})
		f.Close()
		if err != nil {
			return nil, err
		}
		logs = append(logs, name)
		sourceBytes += committed
	}
	if len(logs) == 0 || len(st.Events) == 0 {
		return nil, fmt.Errorf("session has no committed events")
	}
	out.Manifest.Dataset["files"] = logs
	out.Manifest.Dataset["last_seq"] = st.LastSeq()
	out.Manifest.Dataset["committed_bytes"] = sourceBytes
	meta := projection.Metadata{ID: info.ID, Started: info.Started, Modified: st.Events[len(st.Events)-1].Time, Subsessions: len(logs), Size: sourceBytes, Pricing: projection.CapturePricing(st)}
	out.Detail = projection.Detail(meta, st)
	if err := out.addJSON("derived/summary.json", "derived", out.Detail); err != nil {
		return nil, err
	}
	if err := out.addJSON("context/replay.json", "context", meta); err != nil {
		return nil, err
	}
	if err := out.addJSON("context/pricing.json", "context", meta.Pricing); err != nil {
		return nil, err
	}
	// These directories contain native tool outputs and session attachments.
	// WalkDir never follows symlinks; Root.Open also confines every read.
	for _, subdir := range []string{"outputs", "attachments"} {
		err = filepath.WalkDir(filepath.Join(info.Path, subdir), func(path string, d fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("archive asset is not a regular file: %s", d.Name())
			}
			rel, err := filepath.Rel(info.Path, path)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			if !artifact.ValidPath(name) {
				return fmt.Errorf("asset has an unportable name: %s", name)
			}
			f, err := root.Open(rel)
			if err != nil {
				return err
			}
			defer f.Close()
			stat, err := f.Stat()
			if err != nil {
				return err
			}
			if stat.Size() > artifact.MaxFileBytes {
				return fmt.Errorf("asset exceeds archive limit: %s", name)
			}
			ct := mime.TypeByExtension(filepath.Ext(name))
			if ct == "" {
				ct = "application/octet-stream"
			}
			return out.add(name, "asset", ct, func(w io.Writer) error {
				n, err := io.Copy(w, io.LimitReader(f, stat.Size()))
				if err == nil && n != stat.Size() {
					err = fmt.Errorf("asset changed during snapshot: %s", name)
				}
				return err
			})
		})
		if err != nil {
			return nil, err
		}
	}
	service := &settings.Service{Project: project}
	editor, err := config.LockEditor(ctx, project)
	if err != nil {
		return nil, err
	}
	defer editor.Close()
	view, err := service.RemoteSnapshot(service.Grant())
	if err != nil {
		return nil, err
	}
	out.Manifest.Dataset["settings_version"] = view.Snapshot.Version
	if err := out.addJSON("settings/state.json", "context", view); err != nil {
		return nil, err
	}
	editorData, err := service.EditorData()
	if err != nil {
		return nil, err
	}
	if err := out.addJSON("settings/editor.json", "context", editorData); err != nil {
		return nil, err
	}
	if err := out.captureAudit(ctx, project); err != nil {
		return nil, err
	}
	if err := out.captureReferences(info.Path, st.Events); err != nil {
		return nil, err
	}
	if err := out.addJSON("context/provenance.json", "context", map[string]any{"captured_at": out.Manifest.CapturedAt, "project": project, "runtime_settings": "Unknown unless explicitly recorded in session events. This settings snapshot describes defaults at capture time.", "restore": "Inspect this archive before restoring. Restoring does not run tools or reconnect old process IDs.", "source_root": info.Path}); err != nil {
		return nil, err
	}
	js, err := assets.ReadFile("assets/finale-artifact.js")
	if err != nil {
		return nil, err
	}
	for _, surface := range []struct{ asset, path string }{{"viewer.html", "index.html"}, {"settings.html", "settings/index.html"}} {
		page, err := assets.ReadFile("assets/" + surface.asset)
		if err != nil {
			return nil, err
		}
		page = bytes.Replace(page, []byte("/* FINALE_ARTIFACT_SDK */"), js, 1)
		for marker, name := range map[string]string{"/* EAGENT_EDITOR_STYLE */": "style.css", "/* EAGENT_CONFIG_EDITOR */": "config.js", "/* EAGENT_SETTINGS_TRANSPORT */": "artifact-settings.js"} {
			if bytes.Contains(page, []byte(marker)) {
				data, err := webstatic.Files.ReadFile(name)
				if err != nil {
					return nil, err
				}
				page = bytes.Replace(page, []byte(marker), data, 1)
			}
		}
		if bytes.Contains(page, []byte("/* GO_WASM_RUNTIME */")) {
			runtime, err := assets.ReadFile("assets/wasm_exec.js")
			if err != nil {
				return nil, err
			}
			page = bytes.Replace(page, []byte("/* GO_WASM_RUNTIME */"), runtime, 1)
		}
		if err := out.addBytes(surface.path, "viewer", "text/html; charset=utf-8", page); err != nil {
			return nil, err
		}
	}
	// Replay assets are built from the exact same Go reducer and projection.
	for _, name := range []string{"replay.wasm.gz", "wasm_exec.js"} {
		b, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return nil, fmt.Errorf("build portable replay assets: %w", err)
		}
		ct := "application/javascript"
		if strings.HasSuffix(name, ".gz") {
			ct = "application/gzip"
		}
		if err := out.addBytes("viewer/"+name, "viewer", ct, b); err != nil {
			return nil, err
		}
	}
	sort.Slice(out.Manifest.Files, func(i, j int) bool { return out.Manifest.Files[i].Path < out.Manifest.Files[j].Path })
	if err := out.Manifest.Validate(); err != nil {
		return nil, err
	}
	manifest, err := json.MarshalIndent(out.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(manifest, '\n'), 0o600); err != nil {
		return nil, err
	}
	ok = true
	return out, nil
}

func committedLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF {
		return len(data), nil, nil
	}
	return 0, nil, nil
}
func (e *Export) addJSON(name, role string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return e.addBytes(name, role, "application/json", append(raw, '\n'))
}
func (e *Export) addBytes(name, role, ct string, data []byte) error {
	return e.add(name, role, ct, func(w io.Writer) error { _, err := w.Write(data); return err })
}
func (e *Export) add(name, role, ct string, copy func(io.Writer) error) error {
	if !artifact.ValidPath(name) {
		return fmt.Errorf("invalid archive path")
	}
	path := filepath.Join(e.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	whole := sha256.New()
	chunked := &chunkWriter{dst: io.MultiWriter(f, whole), pending: make([]byte, 0, artifact.MaxBlobBytes)}
	err = copy(chunked)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	chunked.flush()
	e.Manifest.Files = append(e.Manifest.Files, artifact.File{Path: name, Role: role, ContentType: ct, Size: chunked.size, SHA256: hex.EncodeToString(whole.Sum(nil)), Chunks: chunked.chunks})
	return nil
}

type chunkWriter struct {
	dst     io.Writer
	pending []byte
	chunks  []artifact.Chunk
	size    int64
}

func (w *chunkWriter) Write(raw []byte) (int, error) {
	if w.size+int64(len(raw)) > artifact.MaxFileBytes {
		return 0, fmt.Errorf("archive file exceeds limit")
	}
	n, err := w.dst.Write(raw)
	w.size += int64(n)
	for rest := raw[:n]; len(rest) > 0; {
		take := min(artifact.MaxBlobBytes-len(w.pending), len(rest))
		w.pending = append(w.pending, rest[:take]...)
		rest = rest[take:]
		if len(w.pending) == artifact.MaxBlobBytes {
			w.flush()
		}
	}
	return n, err
}
func (w *chunkWriter) flush() {
	if len(w.pending) > 0 {
		w.chunks = append(w.chunks, artifact.Chunk{SHA256: artifact.Digest(w.pending), Size: int64(len(w.pending))})
		w.pending = w.pending[:0]
	}
}
