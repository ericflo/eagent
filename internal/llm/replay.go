package llm

import (
	"encoding/json"
	"fmt"
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

// callIDs decides the tool-call ids a request carries. Some providers give
// every reply the same ids (Kimi sends functions.relay_screen:0 each time),
// or write characters another protocol refuses. Replayed to the provider
// that wrote them, a turn's own items keep their ids. A turn rebuilt from
// its text and calls keeps an id the target accepts, byte for byte; an id
// it would refuse, or one already used earlier in the request, is replaced
// by one made from the turn's place in the conversation, and the results
// that follow the turn are given the same replacement. Each decision looks
// only at what came before it, so a conversation that grows renders its
// earlier turns the same way every time.
type callIDs struct {
	accepts func(string) bool
	seen    map[string]bool
	// given maps each id of the latest assistant turn to what it was sent
	// as, in call order (one reply may use an id twice).
	given map[string][]string
}

func newCallIDs(accepts func(string) bool) *callIDs {
	return &callIDs{accepts: accepts, seen: map[string]bool{}}
}

// kept starts a turn whose own items go out as they are.
func (c *callIDs) kept(ids []string) {
	c.given = nil
	for _, id := range ids {
		c.seen[id] = true
	}
}

// rebuilt starts a turn rendered from its calls.
func (c *callIDs) rebuilt() { c.given = map[string][]string{} }

// call returns the id call j of a rebuilt turn, message msg of the
// conversation, goes out with.
func (c *callIDs) call(msg, j int, id string) string {
	out := id
	if !c.accepts(id) || c.seen[id] {
		out = fmt.Sprintf("eagent_%d_%d", msg, j)
		for c.seen[out] {
			out += "_"
		}
	}
	c.seen[out] = true
	if c.given != nil {
		c.given[id] = append(c.given[id], out)
	}
	return out
}

// result returns the id a result for id goes out with.
func (c *callIDs) result(id string) string {
	if q := c.given[id]; len(q) > 0 {
		c.given[id] = q[1:]
		return q[0]
	}
	return id
}

// anthropicAccepts is the pattern Anthropic holds tool_use ids to.
var anthropicAccepts = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString

// responsesAccepts takes any id that is there: nothing narrower is known.
func responsesAccepts(id string) bool { return id != "" }

// claudeThinking returns the blocks of a turn of Claude's that can go back
// to Claude. A thinking block without a signature (the output limit cut it
// off before the signature came) is refused, so it is left out; leaving out
// the trailing blocks of a turn does not disturb the signatures of earlier
// ones. A turn that is only thinking would not be taken either: it returns
// nil, and the turn is rebuilt from its text and calls.
func claudeThinking(native []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(native))
	said := false
	for _, b := range native {
		switch b["type"] {
		case "thinking":
			if sig, _ := b["signature"].(string); sig == "" {
				continue
			}
		case "redacted_thinking":
		default:
			said = true
		}
		out = append(out, b)
	}
	if !said {
		return nil
	}
	return out
}

// reasoningUnreplayable reports whether a Responses turn holds a reasoning
// item that cannot go back with store off: one that carries neither its
// encrypted content nor its own text would be looked up by id on a server
// that kept nothing (OpenAI writes such an item while reasoning replay is
// off). A local server that returns the reasoning in the item itself
// (content, as LM Studio does) gave a whole item, and that one goes back
// as it came. Such a turn is rebuilt from its text and calls rather than
// sent without that one item, since its other items may name the
// reasoning that came with them.
func reasoningUnreplayable(items []json.RawMessage) bool {
	for _, it := range items {
		var probe struct {
			Type      string            `json:"type"`
			Encrypted string            `json:"encrypted_content"`
			Content   []json.RawMessage `json:"content"`
		}
		if json.Unmarshal(it, &probe) == nil && probe.Type == "reasoning" && probe.Encrypted == "" && len(probe.Content) == 0 {
			return true
		}
	}
	return false
}
