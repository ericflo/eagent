package projection

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

func TestCapturedPricingSurvivesViewerCatalogChangesAndUnknownRoutes(t *testing.T) {
	st := state.New()
	route := state.RouteKey{Actor: event.ActorTask, Host: "https://fixture.invalid/", Model: "future-model"}
	st.ByRoute[route] = event.Usage{Input: 1000000, Cached: 200000, Output: 100000}
	meta := Metadata{Pricing: &PricingSnapshot{Basis: "catalog_at_capture", Rates: map[string]*config.Price{pricingKey(route.Host, route.Model): {In: 2, Cached: 1, Out: 5}}}}
	detail := Detail(meta, st)
	if !detail.Priced || detail.CostUSD != 2.3 || detail.PriceBasis != "catalog_at_capture" {
		t.Fatalf("captured prices were replaced by viewer defaults: %+v", detail.SessionSummary)
	}
	meta.Pricing.Rates[pricingKey(route.Host, route.Model)] = nil
	if unknown := Detail(meta, st); unknown.Priced || unknown.CostUSD != 0 {
		t.Fatal("unknown captured price became a known estimate")
	}
	if _, ok := PriceFor(route.Host, route.Model, st.ByRoute[route]); ok {
		t.Fatal("fixture unexpectedly depends on the current catalog")
	}
}
