package config

import "testing"

func TestPickRoute(t *testing.T) {
	project := t.TempDir()
	base := ModelRoute{Protocol: "openai-responses", BaseURL: "https://api.openai.com/v1", Model: "gpt-x", APIKeyEnv: "OPENAI_API_KEY", ReasoningEffort: "xhigh", MaxTokens: 32768,
		Fallback: &ModelRoute{BaseURL: "https://openrouter.ai/api/v1", Model: "openai/gpt-x", APIKeyEnv: "OPENROUTER_API_KEY"}}
	// Another model: the old model's fallback goes, the limits stay, and
	// the effort moves to the nearest one the model offers.
	a, err := PickRoute(project, base, "openrouter", "z-ai/glm-5.3", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Fallback != nil || a.MaxTokens != 32768 || a.BaseURL != "https://openrouter.ai/api/v1" || a.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Fatalf("picked route = %+v", a)
	}
	if a.ReasoningEffort != "high" {
		t.Fatalf("effort = %q, want the nearest offered to xhigh", a.ReasoningEffort)
	}
	// The same model with only a new effort keeps its fallback chain.
	same, err := PickRoute(project, a, "openrouter", "z-ai/glm-5.3", "low")
	if err != nil || same.ReasoningEffort != "low" {
		t.Fatalf("effort pick: %+v %v", same, err)
	}
	withFallback := a
	withFallback.Fallback = base.Fallback
	if kept, _ := PickRoute(project, withFallback, "openrouter", "z-ai/glm-5.3", "low"); kept.Fallback == nil {
		t.Fatal("an effort-only pick dropped the fallback")
	}
	for _, bad := range [][3]string{{"openrouter", "nope/none", ""}, {"evil", "x", ""}, {"openrouter", "z-ai/glm-5.3", "turbo"}, {"openrouter", "z-ai/glm-5.3-flash", "max"}} {
		if _, err := PickRoute(project, base, bad[0], bad[1], bad[2]); err == nil {
			t.Fatalf("pick %v accepted", bad)
		}
	}
}

func TestNearestEffort(t *testing.T) {
	for _, c := range []struct {
		offered []string
		want    string
		got     string
	}{
		{[]string{"low", "medium", "high"}, "xhigh", "high"},
		{[]string{"low", "high"}, "medium", "low"},
		{[]string{"low", "medium", "high"}, "", "medium"},
		{[]string{"high", "xhigh", "max"}, "none", "high"},
	} {
		if got := NearestEffort(c.offered, c.want); got != c.got {
			t.Errorf("NearestEffort(%v, %q) = %q, want %q", c.offered, c.want, got, c.got)
		}
	}
}

// An empty effort sends none; picking another model with no effort named
// keeps it empty, where an effort the model lacks moves to the nearest.
func TestPickRouteKeepsAnEmptyEffortEmpty(t *testing.T) {
	project := t.TempDir()
	base := ModelRoute{Protocol: "openai-chat", BaseURL: "https://api.together.xyz/v1", Model: "zai-org/GLM-5.3", APIKeyEnv: "TOGETHER_API_KEY"}
	for _, model := range []string{"z-ai/glm-5.3", "openai/gpt-6-astra"} {
		a, err := PickRoute(project, base, "openrouter", model, "")
		if err != nil {
			t.Fatal(err)
		}
		if a.ReasoningEffort != "" {
			t.Fatalf("%s: effort %q", model, a.ReasoningEffort)
		}
	}
}
