// Package prompts holds eagent's system prompt and the templates around it
// as Markdown files. The defaults are embedded in the binary; a project can
// override any of them by placing a file with the same name in
// .agents/eagent/prompts/.
//
// Templates use Go text/template syntax. The data each one receives:
//
//	EAGENT.md         {{.Persona}} {{.Phone}} {{.Project}} {{.Interactive}} {{.Instructions}} (the system prompt)
//	PERSONA.md        (plain text, inserted into EAGENT.md)
//	USER_MESSAGE.md   {{.Text}} {{.Time}} {{.Timestamp}} {{.Source}} {{.Nick}} {{.Attachments}} {{.Client}} {{.ClientLine}}
//	USER_ANSWER.md    {{.Text}} {{.Time}} {{.Timestamp}} {{.Source}} {{.Nick}} {{.Attachments}} {{.QuestionID}} {{.Question}} {{.Client}} {{.ClientLine}}
//	DOSSIER.md        {{.Reason}} (the request for a briefing before the conversation restarts)
//	THREAD-SUMMARY.md (plain text: how a dedicated summarizer writes the title and summary)
package prompts

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/filelock"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

//go:embed *.md
var builtin embed.FS

// Names lists the prompt files, in display order.
var Names = []string{"EAGENT.md", "PERSONA.md", "USER_MESSAGE.md", "USER_ANSWER.md", "DOSSIER.md", "THREAD-SUMMARY.md"}

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
			if over, err := boundedfile.Read(p, 1<<20); err == nil && strings.TrimSpace(string(over)) != "" {
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

// EagentData feeds EAGENT.md. Phone is the one line that says the user
// also reads on their phone ("" without a mirror). Nothing here may change
// while a session runs: the system prompt opens every request, and a byte
// that moved would cost the provider's cache of everything after it.
type EagentData struct {
	Persona      string
	Phone        string
	Project      string
	Interactive  bool
	Instructions string
}

// DossierData feeds DOSSIER.md.
type DossierData struct {
	// Files is the table of the session's record files, oldest first.
	Files string
	// Index lists what happened since the last dossier, one line per event
	// with its file:line.
	Index string
	// Fresh says the model no longer has the conversation in front of it
	// (the provider refused it as too long) and writes from the files.
	Fresh bool
	// Reason is why the restart is happening ("" when it is only size).
	Reason string
}

// UserMessageData feeds USER_MESSAGE.md. Timestamp is preformatted from
// Time (ev.Time.Local().Format("2006-01-02 15:04:05")) so templates never
// call time.Now and rendering stays deterministic across replays.
type UserMessageData struct {
	Text        string
	Time        time.Time
	Timestamp   string
	Source      string
	Nick        string
	Attachments string
	// Client is the raw capsule (may be nil); ClientLine is its Compact()
	// rendering ("" when nil or empty), the only form templates should use.
	Client     string
	ClientLine string
}

// UserAnswerData feeds USER_ANSWER.md. Question is the restated question
// text ("" when asked in this subsession); QuestionID names the question.
type UserAnswerData struct {
	Text        string
	Time        time.Time
	Timestamp   string
	Source      string
	Nick        string
	Attachments string
	QuestionID  string
	Question    string
	// Client is the capsule's source ("" when none); ClientLine is its
	// Compact() rendering ("" when nil or empty).
	Client     string
	ClientLine string
}

// Export writes the built-in defaults into the project's prompts directory
// so they can be edited. Existing files are left alone. It returns the paths
// written.
func Export(project string) ([]string, error) {
	editor, err := config.LockEditor(context.Background(), project)
	if err != nil {
		return nil, err
	}
	defer editor.Close()
	dir := Dir(project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, name := range Names {
		p := filepath.Join(dir, name)
		raw, _ := builtin.ReadFile(name)
		if err := filelock.CreateFile(p, raw, 0o644); os.IsExist(err) {
			continue
		} else if err != nil {
			return written, err
		}
		written = append(written, p)
	}
	sort.Strings(written)
	return written, nil
}
