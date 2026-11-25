package main

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The help's list of built-in presets is the list of presets: every one is
// there, and no name that now stands for another is offered as one.
func TestTheHelpListsTheBuiltInPresets(t *testing.T) {
	start := strings.Index(usage, "built-ins:")
	end := strings.Index(usage, "--config <n>")
	if start < 0 || end < start {
		t.Fatal("no preset list in the help")
	}
	var listed []string
	for _, word := range regexp.MustCompile(`[a-z0-9.|-]+`).FindAllString(usage[start+len("built-ins:"):end], -1) {
		if word == "default" || word == "or" || word == "a" || word == "provider" || word == "tier" {
			continue
		}
		base, tiers, isTier := strings.Cut(word, "|")
		if !isTier {
			listed = append(listed, word)
			continue
		}
		family, _, _ := strings.Cut(base, "-")
		listed = append(listed, base)
		for _, tier := range strings.Split(tiers, "|") {
			listed = append(listed, family+"-"+tier)
		}
	}
	want := config.PresetNames()
	sort.Strings(listed)
	sort.Strings(want)
	if !slices.Equal(listed, want) {
		t.Fatalf("the help lists %v\nthe presets are %v", listed, want)
	}
}
