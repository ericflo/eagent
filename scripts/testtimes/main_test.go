package main

import (
	"strings"
	"testing"
	"time"
)

const stream = `{"Action":"start","Package":"github.com/ericflo/eagent/internal/a"}
{"Action":"run","Package":"github.com/ericflo/eagent/internal/a","Test":"TestQuick"}
{"Action":"output","Package":"github.com/ericflo/eagent/internal/a","Test":"TestQuick","Output":"quick says hello\n"}
{"Action":"pass","Package":"github.com/ericflo/eagent/internal/a","Test":"TestQuick","Elapsed":0.01}
{"Action":"output","Package":"github.com/ericflo/eagent/internal/a","Test":"TestSlow/sub","Output":"    sub: got 1, want 2\n"}
{"Action":"fail","Package":"github.com/ericflo/eagent/internal/a","Test":"TestSlow/sub","Elapsed":4}
{"Action":"output","Package":"github.com/ericflo/eagent/internal/a","Test":"TestSlow","Output":"--- FAIL: TestSlow (4.50s)\n"}
{"Action":"fail","Package":"github.com/ericflo/eagent/internal/a","Test":"TestSlow","Elapsed":4.5}
{"Action":"output","Package":"github.com/ericflo/eagent/internal/a","Output":"FAIL\n"}
{"Action":"fail","Package":"github.com/ericflo/eagent/internal/a","Elapsed":4.6}
not json at all
{"Action":"pass","Package":"github.com/ericflo/eagent/internal/b","Test":"TestMiddling","Elapsed":2.5}
{"Action":"pass","Package":"github.com/ericflo/eagent/internal/b","Elapsed":12.25}
{"Action":"skip","Package":"github.com/ericflo/eagent/internal/c","Elapsed":0}
`

func TestReportListsPackagesAndSlowTestsSlowestFirst(t *testing.T) {
	var shown strings.Builder
	r, err := read(strings.NewReader(stream), &shown)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.packages) != 2 || r.packages[0].pkg != "github.com/ericflo/eagent/internal/b" || r.packages[0].took != 12250*time.Millisecond {
		t.Fatalf("packages = %+v", r.packages)
	}
	if len(r.tests) != 3 || r.tests[0].test != "TestSlow" || r.tests[1].test != "TestMiddling" {
		t.Fatalf("tests = %+v (subtests are not counted on their own)", r.tests)
	}
	text, over := r.text(2*time.Second, 0)
	for _, want := range []string{"12.25s  internal/b", "4.5s  internal/a TestSlow", "2.5s  internal/b TestMiddling", "FAILED: github.com/ericflo/eagent/internal/a"} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	if over || strings.Contains(text, "TestQuick") {
		t.Errorf("over=%v with no limit set, or a quick test was listed:\n%s", over, text)
	}
}

func TestOutputOfWhatFailedIsShownAndOfWhatPassedIsNot(t *testing.T) {
	var shown strings.Builder
	if _, err := read(strings.NewReader(stream), &shown); err != nil {
		t.Fatal(err)
	}
	got := shown.String()
	for _, want := range []string{"sub: got 1, want 2", "--- FAIL: TestSlow", "FAIL\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("failure output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "quick says hello") {
		t.Errorf("a passing test's output was shown:\n%s", got)
	}
}

func TestATestOverTheLimitFailsTheReport(t *testing.T) {
	r, _ := read(strings.NewReader(stream), &strings.Builder{})
	text, over := r.text(2*time.Second, 4*time.Second)
	if !over || !strings.Contains(text, "TestSlow  OVER THE LIMIT") || strings.Contains(text, "TestMiddling  OVER") {
		t.Fatalf("over=%v:\n%s", over, text)
	}
	if _, over := r.text(2*time.Second, 5*time.Second); over {
		t.Fatal("4.5s is within a 5s limit")
	}
	md := r.markdown(2 * time.Second)
	if !strings.Contains(md, "| `internal/b` | 12.25s |") || !strings.Contains(md, "- `internal/a` TestSlow: 4.5s") {
		t.Fatalf("summary:\n%s", md)
	}
}
