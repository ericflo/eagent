package main

import (
	"strings"
	"testing"
)

// What `go test -json` writes when a package times out with a test still
// running (and the same shape when a goroutine panics): the test never
// gets a pass or fail of its own, only the package does.
const timedOut = `{"Action":"start","Package":"example.com/hang"}
{"Action":"run","Package":"example.com/hang","Test":"TestFine"}
{"Action":"output","Package":"example.com/hang","Test":"TestFine","Output":"    hang_test.go:3: fine says hi\n"}
{"Action":"pass","Package":"example.com/hang","Test":"TestFine","Elapsed":0}
{"Action":"run","Package":"example.com/hang","Test":"TestStuck"}
{"Action":"output","Package":"example.com/hang","Test":"TestStuck","Output":"=== RUN   TestStuck\n"}
{"Action":"output","Package":"example.com/hang","Test":"TestStuck","Output":"    hang_test.go:4: waiting for the server\n"}
{"Action":"run","Package":"example.com/hang","Test":"TestStuck/inner"}
{"Action":"output","Package":"example.com/hang","Test":"TestStuck/inner","Output":"    hang_test.go:9: inner started\n"}
{"Action":"output","Package":"example.com/hang","Test":"TestStuck","Output":"panic: test timed out after 1s\n"}
{"Action":"output","Package":"example.com/hang","Test":"TestStuck","Output":"\trunning tests:\n"}
{"Action":"output","Package":"example.com/hang","Test":"TestStuck","Output":"\t\tTestStuck (1s)\n"}
{"Action":"output","Package":"example.com/hang","Test":"TestStuck","Output":"example.com/hang.TestStuck(0x22e4cf7e6488?)\n"}
{"Action":"output","Package":"example.com/hang","Output":"FAIL\texample.com/hang\t1.005s\n"}
{"Action":"fail","Package":"example.com/hang","Elapsed":1.005}
{"Action":"run","Package":"example.com/other","Test":"TestElsewhere"}
{"Action":"output","Package":"example.com/other","Test":"TestElsewhere","Output":"    other_test.go:1: another package's words\n"}
`

func TestAPackageThatFailsWithATestStillRunningShowsWhatThatTestWrote(t *testing.T) {
	var shown strings.Builder
	r, err := read(strings.NewReader(timedOut), &shown)
	if err != nil {
		t.Fatal(err)
	}
	got := shown.String()
	want := []string{"waiting for the server", "inner started", "panic: test timed out after 1s", "TestStuck(0x22e4cf7e6488?)", "FAIL\texample.com/hang"}
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w)
		if i < 0 {
			t.Fatalf("the output lacks %q after what came before it:\n%s", w, got)
		}
		at += i
	}
	if strings.Contains(got, "fine says hi") || strings.Contains(got, "another package's words") {
		t.Errorf("a passing test's output, or another package's, was shown:\n%s", got)
	}
	if len(r.failed) != 1 || r.failed[0] != "example.com/hang" {
		t.Errorf("failed = %v", r.failed)
	}
}
