package relay

import "testing"

// Round 14, finding 11: pi 1.6.1 with no provider set up prints a
// sentence, not the table, and exits 0. No model comes of it, so the
// one-minute retry stands instead of an hour of a model named "No/models".
func TestR14PiModelsNeedTheHeader(t *testing.T) {
	out := "No models available. Use /login to log into a provider via OAuth or API key. See:\n" +
		"  /usr/lib/node_modules/@earendil-works/pi-coding-agent/docs/providers.md\n"
	if got := parsePiModels(out, ""); len(got) != 0 {
		t.Fatalf("models from a sentence: %+v", got)
	}
	table := "provider  model  context  max-out  thinking  images\n" +
		"local     q      200K     32K      yes       no\n" +
		"\n" +
		"See the docs for more providers.\n"
	got := parsePiModels(table, "")
	if len(got) != 1 || got[0].ID != "local/q" {
		t.Fatalf("models after the table: %+v", got)
	}
}
