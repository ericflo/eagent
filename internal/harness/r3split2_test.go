package harness

import (
	"fmt"
	"math/rand"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var r3vMarker = regexp.MustCompile(`\n\n_\(part \d+ of \d+\)_$`)

// r3vRejoin checks that parts, with what splitReport adds taken away (the
// marker, a fence closed at the end of a part, the fence line opened again
// at the start of the next), are body exactly. It returns "" or what is
// wrong.
func r3vRejoin(body string, parts []string) string {
	if len(parts) == 1 {
		if parts[0] != body {
			return "single part differs"
		}
		return ""
	}
	consumed := ""
	for i, p := range parts {
		if !r3vMarker.MatchString(p) {
			return fmt.Sprintf("part %d has no marker at its end", i)
		}
		p = r3vMarker.ReplaceAllString(p, "")
		lineStart := consumed == "" || strings.HasSuffix(consumed, "\n")
		fence, open := reportFences(consumed+"\n", true, "", "")
		_ = lineStart
		if fence != "" {
			head := open + "\n"
			if len(head) > 0 && !strings.HasPrefix(p, head) && !strings.HasPrefix(p, fence+"\n") {
				return fmt.Sprintf("part %d does not reopen %q: %q", i, open, p[:min(len(p), 60)])
			}
			if strings.HasPrefix(p, head) {
				p = p[len(head):]
			} else {
				p = p[len(fence)+1:]
			}
		}
		rest := body[len(consumed):]
		if i == len(parts)-1 {
			if p != rest {
				return fmt.Sprintf("last part %q != rest %q", p, rest)
			}
			return ""
		}
		ok := false
		// a closing fence was added
		if j := strings.LastIndexByte(p, '\n'); j >= 0 {
			for _, c := range []string{p[:j+1], p[:j]} {
				if strings.HasPrefix(rest, c) {
					f, _ := reportFences(consumed+c+"\n", true, "", "")
					if f != "" && p[j+1:] == f && (strings.HasSuffix(c, "\n") || c == p[:j]) {
						consumed += c
						ok = true
						break
					}
				}
			}
		}
		if !ok && strings.HasPrefix(rest, p) {
			f, _ := reportFences(consumed+p+"\n", true, "", "")
			if f == "" {
				consumed += p
				ok = true
			}
		}
		if !ok {
			return fmt.Sprintf("part %d does not rejoin: %q", i, p[max(0, len(p)-60):])
		}
	}
	return ""
}

func TestSplitReportsRejoinExactlyWhateverTheyHold(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pieces := []string{"plain words here", "```go", "```", "````", "~~~", "~~~ python", "    ```", "`inline` code", "日本語のテキスト", strings.Repeat("x", 300), "", "``` ", "```js extra", "  ~~~~", "\t```", "> ```"}
	// The cases are drawn in order from the one seed, then checked on
	// several cores: splitReport and the check are pure.
	type splitCase struct {
		body  string
		limit int
	}
	cases := make([]splitCase, 30000)
	for n := range cases {
		var lines []string
		for k := rng.Intn(60); k > 0; k-- {
			lines = append(lines, pieces[rng.Intn(len(pieces))])
		}
		body := strings.Join(lines, "\n")
		if rng.Intn(2) == 0 {
			body += "\n"
		}
		cases[n] = splitCase{body, 120 + rng.Intn(400)}
	}
	check := func(n int) string {
		body, limit := cases[n].body, cases[n].limit
		parts := splitReport(body, limit)
		for i, p := range parts {
			if len(p) > limit {
				return fmt.Sprintf("case %d: part %d is %d bytes, limit %d", n, i, len(p), limit)
			}
		}
		if why := r3vRejoin(body, parts); why != "" {
			return fmt.Sprintf("case %d (limit %d): %s\nbody %q\nparts %q", n, limit, why, body, parts)
		}
		return ""
	}
	workers := min(runtime.GOMAXPROCS(0), 8)
	failed := make([]string, workers) // each worker's first failure
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := w; n < len(cases); n += workers {
				if why := check(n); why != "" {
					failed[w] = why
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, why := range failed {
		if why != "" {
			t.Error(why)
		}
	}
}
