package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToolListIsFixedAndWellFormed(t *testing.T) {
	first, second := All(), All()
	if len(first) != 15 {
		t.Fatalf("eagent has %d tools, want 15", len(first))
	}
	seen := map[string]bool{}
	for i, tool := range first {
		if tool.Name != second[i].Name || string(tool.Parameters) != string(second[i].Parameters) || tool.Description != second[i].Description {
			t.Fatalf("tool %d differs between two calls of All", i)
		}
		if seen[tool.Name] {
			t.Fatalf("tool %s is listed twice", tool.Name)
		}
		seen[tool.Name] = true
		var schema map[string]any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil || schema["type"] != "object" {
			t.Fatalf("tool %s: bad schema: %v", tool.Name, err)
		}
		if !Has(tool.Name) {
			t.Fatalf("Has(%q) is false", tool.Name)
		}
	}
	// A caller that changes its copy must not change the list.
	first[0].Name = "changed"
	if All()[0].Name == "changed" {
		t.Fatal("All returned its own backing array")
	}
	for _, name := range []string{"bash", "write_file", "delegate", "yield", "note"} {
		if Has(name) {
			t.Fatalf("%s is still a tool", name)
		}
	}
	// Kept from the three-actor design: timers and looking at a picture.
	for _, name := range []string{Schedule, CancelSchedule, ViewImage} {
		if !Has(name) {
			t.Fatalf("%s is not a tool", name)
		}
	}
}

func TestOnlyReadingToolsContinueATurn(t *testing.T) {
	for _, tool := range All() {
		want := tool.Name == RelayScreen || tool.Name == SessionSearch || tool.Name == SessionRead || tool.Name == ViewImage
		if Looks(tool.Name) != want {
			t.Errorf("Looks(%s) = %v, want %v", tool.Name, Looks(tool.Name), want)
		}
	}
}

func TestTruncateKeepsHeadAndTailRuneSafe(t *testing.T) {
	s := strings.Repeat("é", 1000)
	out := Truncate(s, 100)
	if !utf8.ValidString(out) {
		t.Fatal("truncation split a rune")
	}
	if !strings.HasPrefix(out, "é") || !strings.HasSuffix(out, "é") || !strings.Contains(out, "characters omitted") {
		t.Fatalf("unexpected shape: %q", out)
	}
	if got := Truncate("short", 100); got != "short" {
		t.Fatalf("short text changed: %q", got)
	}
}
