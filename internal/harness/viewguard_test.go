package harness

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// The user can write while a model call is out, so "the newest message from
// the user" in the live record can be one the reply being run never saw.
// Code that acts for a reply takes it from what the reply's call was shown
// (turnCalls.view, userSeqAsOf). The functions below read the live value on
// purpose, each for the reason given; a new reader anywhere else fails here.
var readsNewestUserMessage = map[string]struct {
	times int
	why   string
}{
	"userSeqAsOf":      {1, "the one place a view is turned into a message"},
	"turn":             {1, "the closing word: is the newest message newer than what the last word's call saw"},
	"tail":             {1, "the same question, for the closing prompt"},
	"startSummaries":   {1, "whether the title and summary are behind the user"},
	"summaryDue":       {1, "whether the user has written at all"},
	"summaryRipe":      {1, "whether the user has written at all"},
	"finalSummary":     {1, "whether the user has written at all"},
	"acceptSummary":    {1, "whether the title was set before the user's newest message"},
	"SummarizeSession": {1, "whether the user has written at all"},
}

func TestOnlyNamedCodeReadsTheNewestUserMessageFromTheLiveRecord(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "LastUserSeq" {
						found[fn.Name.Name]++
					}
					return true
				})
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("found no reader at all: the scan is broken")
	}
	for name, n := range found {
		if want := readsNewestUserMessage[name]; n != want.times {
			t.Errorf("%s reads LastUserSeq from the live record %d times, and %d are accounted for. A reply's code must use what its call was shown (turnCalls.view, userSeqAsOf); if this reader is meant, add it to readsNewestUserMessage with its reason.", name, n, want.times)
		}
	}
	for name := range readsNewestUserMessage {
		if found[name] == 0 {
			t.Errorf("%s no longer reads LastUserSeq: take it off the list", name)
		}
	}
}
