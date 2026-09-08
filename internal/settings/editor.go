package settings

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/protocol/artifact"
)

// EditorData supplies the same built-in catalog and resolved named choices the
// local config page uses. It contains typed configuration, never raw unknown
// keys, credential values, or process-control files.
func (s *Service) EditorData() (map[string]any, error) {
	presets := map[string]config.Config{}
	for _, name := range config.PresetNames() {
		cfg := config.Defaults()
		config.Presets[name](&cfg)
		cfg.Preset, cfg.Description = name, config.PresetDescription(name)
		presets[name] = cfg
	}
	bundles := map[string]any{}
	root, err := os.OpenRoot(s.Project)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	names, err := config.ListBundles(s.Project)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		entry := map[string]any{}
		raw, readErr := func() ([]byte, error) {
			rel, err := filepath.Rel(s.Project, config.BundlePath(s.Project, name))
			if err != nil {
				return nil, err
			}
			f, err := root.Open(rel)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() || info.Size() > 1<<20 {
				return nil, fmt.Errorf("bundle is not a bounded regular file")
			}
			raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
			if len(raw) > 1<<20 {
				return nil, fmt.Errorf("bundle exceeds 1 MiB")
			}
			return raw, err
		}()
		cfg, loadErr := config.BundleFromBytes(name, raw)
		if readErr != nil || loadErr != nil {
			entry["invalid"] = "This bundle is unavailable or invalid in the captured project."
		} else {
			entry["config"], entry["sha256"] = cfg, artifact.Digest(raw)
		}
		bundles[name] = entry
	}
	transports := map[string]string{}
	for _, protocol := range []string{llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic} {
		transports[protocol] = config.Transport(protocol)
	}
	out := map[string]any{"format": "eagent.config-editor/v1", "defaults": config.Defaults(), "presets": presets, "bundles": bundles, "catalog": map[string]any{"providers": config.Providers, "models": config.Models, "effort_order": config.EffortOrder, "transports": transports}}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 8<<20 {
		return nil, fmt.Errorf("configuration picker data exceeds 8 MiB")
	}
	return out, nil
}
