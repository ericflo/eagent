package llm

import (
	"regexp"
	"strings"
)

// What a replayed conversation may carry from one model to another.

// sameModel reports whether a turn the response named recorded was made
// by the model asked, so its native items can go back to it. A response
// names the model that ran, which can be a dated variant of the one asked
// for (gpt-6 answers as gpt-6-2026-03-01), or drop or add a router's vendor
// prefix. A record that does not say, or a caller that does not, counts as
// the same model: that is how such turns replayed before.
func sameModel(recorded, asked string) bool {
	if recorded == "" || asked == "" {
		return true
	}
	a, b := strings.ToLower(recorded), strings.ToLower(asked)
	if strings.Contains(a, "/") != strings.Contains(b, "/") {
		a, b = a[strings.LastIndexByte(a, '/')+1:], b[strings.LastIndexByte(b, '/')+1:]
	}
	if a == b {
		return true
	}
	return datedVariant(a, b) || datedVariant(b, a)
}

// datedSuffix is a release date after a model name: -2026-03-01,
// -20260301, or Vertex's @20260301.
var datedSuffix = regexp.MustCompile(`^[-@]\d{4}-?\d{2}-?\d{2}$`)

func datedVariant(long, short string) bool {
	return strings.HasPrefix(long, short) && datedSuffix.MatchString(long[len(short):])
}

// claudeModel reports whether a model name is one of Anthropic's.
func claudeModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "claude")
}

// anthropicReplays reports whether a turn's own content blocks go back to
// model. Claude models take each other's thinking blocks: the API leaves
// out a block the model cannot read and the request goes on, and removing
// blocks ourselves would break the chain later blocks are signed against.
// A Claude model cannot read another vendor's signature, though, so a turn
// some other model wrote behind the same gateway goes to Claude rebuilt
// from its text and calls. Other models get what they got before.
func anthropicReplays(m Message, model string) bool {
	return m.NativeModel == "" || !claudeModel(model) || claudeModel(m.NativeModel)
}
