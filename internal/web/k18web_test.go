package web

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// k18Session is a session whose record's session.start names name.
func k18Session(t *testing.T, project, name string) store.Info {
	t.Helper()
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, Config: name})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// A session ran here on the bundle "other", and then elsewhere (a terminal:
// a session.resume on its record after that run). The configuration this
// server knew it by is no longer the newest word: the record's (here, the
// server's own, as it names none) wins. It used to keep "other".
func TestK18ANewerRunElsewhereOutranksTheOneKnownHere(t *testing.T) {
	_, project := newTestServer(t)
	s := New(project, nil)
	if _, err := config.Write(project, r11Model(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveBundle(project, "other", "", r11Model(t)); err != nil {
		t.Fatal(err)
	}
	info := k18Session(t, project, "")
	s.noteRan(info.ID, runPick{Config: "other", Preset: "-"}) // its run here
	if _, pick, err := s.runConfig(info); err != nil || pick.Config != "other" {
		t.Fatalf("setup: runs on %+v (%v)", pick, err)
	}
	sess, _, err := store.Open(info.Path, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	later := event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{Interactive: true})
	later.Time = time.Now().Add(time.Second).UTC()
	if _, err := sess.Append(later); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	if _, pick, err := s.runConfig(info); err != nil || pick != (runPick{Default: true}) {
		t.Fatalf("a newer run elsewhere, and the session still runs on %+v (%v), the one its run here had", pick, err)
	}
}

// A session whose record names a preset (its session.start's config: the
// run's bundle, or with none its preset) runs on that preset, named as a
// preset; it used to be named, and listed for the next server, as a
// bundle of that name.
func TestK18ARecordedPresetIsTakenAsAPreset(t *testing.T) {
	_, project := newTestServer(t)
	var logged []string
	s := New(project, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	preset := config.PresetNames()[0]
	info := k18Session(t, project, preset)
	cfg, pick, err := s.runConfig(info)
	if err != nil {
		t.Fatal(err)
	}
	if pick != (runPick{Config: "-", Preset: preset}) || cfg.Preset != preset {
		t.Fatalf("the record names preset %q; runs on %+v (preset %q)", preset, pick, cfg.Preset)
	}
	if !slices.ContainsFunc(logged, func(l string) bool { return strings.Contains(l, "preset "+preset) }) {
		t.Fatalf("the log does not name the preset: %q", logged)
	}
	// A bundle stays a bundle.
	if _, err := config.Write(project, r11Model(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveBundle(project, "work", "", r11Model(t)); err != nil {
		t.Fatal(err)
	}
	if _, pick, err := s.runConfig(k18Session(t, project, "work")); err != nil || pick != (runPick{Config: "work", Preset: "-"}) {
		t.Fatalf("the record names bundle work; runs on %+v (%v)", pick, err)
	}
}
