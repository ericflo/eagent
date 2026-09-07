// Package prompts holds the actors' system prompts and task templates as
// Markdown files. The defaults are embedded in the binary; a project can
// override any of them by placing a file with the same name in
// .agents/eagent/prompts/.
//
// Templates use Go text/template syntax. The data each one receives:
//
//	ORCHESTRATOR.md        {{.Project}} {{.Instructions}} {{.Interactive}}
//	TASK-WORKER.md         {{.Project}} {{.Instructions}}
//	NARRATOR.md            {{.Persona}}
//	PERSONA.md             (plain text, inserted into NARRATOR.md)
//	COMPACTION-DOSSIER.md  {{.SessionDir}} {{.Files}} {{.Reason}} {{.RunningTasks}}
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

//go:embed *.md
var builtin embed.FS

// Names lists the prompt files, in display order.
var Names = []string{"ORCHESTRATOR.md", "TASK-WORKER.md", "NARRATOR.md", "PERSONA.md", "COMPACTION-DOSSIER.md"}

// Dir is where a project keeps its overrides.
func Dir(project string) string { return filepath.Join(project, ".agents", "eagent", "prompts") }

// Set is a loaded collection of prompts.
type Set struct {
	text   map[string]string
	tmpl   map[string]*template.Template
	Source map[string]string // name -> "built-in" or the override path
}

// Load reads the embedded defaults and applies project overrides.
func Load(project string) (*Set, error) {
	s := &Set{text: map[string]string{}, tmpl: map[string]*template.Template{}, Source: map[string]string{}}
	for _, name := range Names {
		raw, err := builtin.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("embedded prompt %s: %w", name, err)
		}
		s.text[name] = string(raw)
		s.Source[name] = "built-in"
		if project != "" {
			p := filepath.Join(Dir(project), name)
			if over, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(over)) != "" {
				s.text[name] = string(over)
				s.Source[name] = p
			}
		}
		t, err := template.New(name).Option("missingkey=zero").Parse(s.text[name])
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", name, s.Source[name], err)
		}
		s.tmpl[name] = t
	}
	return s, nil
}

// Text returns a prompt's raw template text.
func (s *Set) Text(name string) string { return s.text[name] }

// Render executes a prompt template.
func (s *Set) Render(name string, data any) string {
	t, ok := s.tmpl[name]
	if !ok {
		return ""
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return s.text[name] + "\n\n[prompt template error: " + err.Error() + "]"
	}
	return strings.TrimSpace(b.String()) + "\n"
}

// OrchestratorData feeds ORCHESTRATOR.md.
type OrchestratorData struct {
	Project      string
	Instructions string
	Interactive  bool
}

// TaskData feeds TASK-WORKER.md.
type TaskData struct {
	Project      string
	Instructions string
}

// NarratorData feeds NARRATOR.md.
type NarratorData struct{ Persona string }

// DossierData feeds COMPACTION-DOSSIER.md.
type DossierData struct {
	SessionDir   string
	Files        string
	Reason       string
	RunningTasks []string
}

// Export writes the built-in defaults into the project's prompts directory
// so they can be edited. Existing files are left alone. It returns the paths
// written.
func Export(project string) ([]string, error) {
	dir := Dir(project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, name := range Names {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			continue
		}
		raw, _ := builtin.ReadFile(name)
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			return written, err
		}
		written = append(written, p)
	}
	sort.Strings(written)
	return written, nil
}
