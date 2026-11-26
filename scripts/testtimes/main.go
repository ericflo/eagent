// Command testtimes reads the output of `go test -json` and says where the
// time went: each package's wall time, and every test slower than a budget.
// It exists because a suite gets slow one test at a time, and nobody sees
// it happen unless something says so.
//
//	go test -race -json ./... > test.json
//	go run ./scripts/testtimes test.json
//
// It also prints the output of every test and package that failed, so a
// run whose output went to a file still shows what broke. It exits 1 when a
// test took longer than -fail (never because tests failed: `go test` has
// said that already).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type event struct {
	Action     string  `json:"Action"`
	Package    string  `json:"Package"`
	ImportPath string  `json:"ImportPath"`
	Test       string  `json:"Test"`
	Elapsed    float64 `json:"Elapsed"`
	Output     string  `json:"Output"`
}

type timed struct {
	pkg, test string
	took      time.Duration
}

type report struct {
	packages []timed // wall time, slowest first
	tests    []timed // top-level tests, slowest first
	failed   []string
	total    time.Duration
}

func main() {
	warn := flag.Duration("warn", 2*time.Second, "list tests slower than this")
	fail := flag.Duration("fail", 0, "exit 1 when a test is slower than this (0: never)")
	summary := flag.String("summary", "", "also append a Markdown summary to this file")
	github := flag.Bool("github", os.Getenv("GITHUB_ACTIONS") == "true", "print GitHub warning annotations")
	flag.Parse()
	in := io.Reader(os.Stdin)
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "testtimes:", err)
			os.Exit(2)
		}
		defer f.Close()
		in = f
	}
	r, err := read(in, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testtimes:", err)
		os.Exit(2)
	}
	text, over := r.text(*warn, *fail)
	fmt.Print(text)
	if *github {
		for _, t := range r.tests {
			if t.took > *warn {
				fmt.Printf("::warning title=Slow test::%s %s took %s (budget %s)\n", short(t.pkg), t.test, round(t.took), *warn)
			}
		}
	}
	if *summary != "" {
		if f, err := os.OpenFile(*summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprint(f, r.markdown(*warn))
			f.Close()
		}
	}
	if over {
		os.Exit(1)
	}
}

// read folds a `go test -json` stream into a report, and writes the output
// of everything that failed to out as it goes.
func read(in io.Reader, out io.Writer) (report, error) {
	var r report
	output := map[string][]string{} // "pkg" or "pkg\x00test" -> lines
	// Every test of a package, in the order it first wrote: a package that
	// times out, or a goroutine that panics, ends the binary with tests
	// still running, and those never get a pass or fail of their own.
	running := map[string][]string{} // pkg -> keys
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue // a line `go test` did not write
		}
		key := e.Package + "\x00" + e.Test
		switch e.Action {
		case "build-output":
			fmt.Fprint(out, e.Output) // a package that did not compile
		case "output":
			if _, seen := output[key]; !seen && e.Test != "" {
				running[e.Package] = append(running[e.Package], key)
			}
			output[key] = append(output[key], e.Output)
		case "pass", "fail", "skip":
			took := time.Duration(e.Elapsed * float64(time.Second))
			switch {
			case e.Test == "":
				if e.Action != "skip" {
					r.packages = append(r.packages, timed{pkg: e.Package, took: took})
					r.total += took
				}
				// What the tests that never finished wrote, the panic and
				// its stacks among it, then the package's own lines.
				for _, k := range running[e.Package] {
					if e.Action == "fail" {
						fmt.Fprint(out, strings.Join(output[k], ""))
					}
					delete(output, k)
				}
				delete(running, e.Package)
				if e.Action == "fail" {
					r.failed = append(r.failed, e.Package)
					fmt.Fprint(out, strings.Join(output[key], ""))
				}
			case !strings.Contains(e.Test, "/"):
				r.tests = append(r.tests, timed{pkg: e.Package, test: e.Test, took: took})
				fallthrough
			default:
				if e.Action == "fail" {
					fmt.Fprint(out, strings.Join(output[key], ""))
				}
			}
			delete(output, key)
		}
	}
	slowest := func(a, b timed) bool { return a.took > b.took }
	sort.SliceStable(r.packages, func(i, j int) bool { return slowest(r.packages[i], r.packages[j]) })
	sort.SliceStable(r.tests, func(i, j int) bool { return slowest(r.tests[i], r.tests[j]) })
	return r, sc.Err()
}

// text is the report for a terminal, and whether a test is over fail.
func (r report) text(warn, fail time.Duration) (string, bool) {
	var b strings.Builder
	over := false
	if len(r.failed) > 0 {
		fmt.Fprintf(&b, "\nFAILED: %s\n", strings.Join(r.failed, ", "))
	}
	b.WriteString("\nPackages by wall time:\n")
	for _, p := range r.packages {
		if p.took >= time.Second {
			fmt.Fprintf(&b, "  %7s  %s\n", round(p.took), short(p.pkg))
		}
	}
	longest := time.Duration(0)
	if len(r.packages) > 0 {
		longest = r.packages[0].took
	}
	fmt.Fprintf(&b, "  %d packages, %d tests; the slowest package (%s) is the least a run can take\n", len(r.packages), len(r.tests), round(longest))
	slow := 0
	for _, t := range r.tests {
		if t.took <= warn {
			break
		}
		if slow == 0 {
			fmt.Fprintf(&b, "\nTests slower than %s:\n", warn)
		}
		slow++
		mark := ""
		if fail > 0 && t.took > fail {
			mark, over = "  OVER THE LIMIT", true
		}
		fmt.Fprintf(&b, "  %7s  %s %s%s\n", round(t.took), short(t.pkg), t.test, mark)
	}
	if slow == 0 {
		fmt.Fprintf(&b, "\nNo test is slower than %s.\n", warn)
	}
	if over {
		fmt.Fprintf(&b, "\nA test may take %s at most. Wait on the event instead of the clock, or make the delay it waits out shrinkable in tests (CONTRIBUTING.md).\n", fail)
	}
	return b.String(), over
}

// markdown is the same, short, for a CI job summary.
func (r report) markdown(warn time.Duration) string {
	var b strings.Builder
	b.WriteString("### Test times\n\n| Package | Wall time |\n|---|---|\n")
	for i, p := range r.packages {
		if i == 5 || p.took < time.Second {
			break
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", short(p.pkg), round(p.took))
	}
	slow := 0
	for _, t := range r.tests {
		if t.took <= warn {
			break
		}
		if slow == 0 {
			fmt.Fprintf(&b, "\nTests slower than %s:\n\n", warn)
		}
		slow++
		fmt.Fprintf(&b, "- `%s` %s: %s\n", short(t.pkg), t.test, round(t.took))
	}
	if slow == 0 {
		fmt.Fprintf(&b, "\nNo test is slower than %s.\n", warn)
	}
	return b.String()
}

func short(pkg string) string {
	if i := strings.Index(pkg, "/eagent/"); i >= 0 {
		return pkg[i+len("/eagent/"):]
	}
	return pkg
}

func round(d time.Duration) time.Duration { return d.Round(10 * time.Millisecond) }
