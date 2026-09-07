package main

import (
	"flag"
	"strings"
	"testing"
)

func TestParseInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	raw := fs.Bool("raw", false, "")
	answer := fs.String("answer", "", "")
	rest, err := parseInterleaved(fs, []string{"show", "1788", "--raw"})
	if err != nil || !*raw || strings.Join(rest, " ") != "show 1788" {
		t.Fatalf("rest=%v raw=%v err=%v", rest, *raw, err)
	}
	fs2 := flag.NewFlagSet("t", flag.ContinueOnError)
	answer = fs2.String("answer", "", "")
	rest, err = parseInterleaved(fs2, []string{"resume", "1788", "--answer", "yes", "more", "words"})
	if err != nil || *answer != "yes" || strings.Join(rest, " ") != "resume 1788 more words" {
		t.Fatalf("rest=%v answer=%q err=%v", rest, *answer, err)
	}
	fs3 := flag.NewFlagSet("t", flag.ContinueOnError)
	fs3.Bool("p", false, "")
	rest, _ = parseInterleaved(fs3, []string{"-p", "--", "--not-a-flag", "prompt"})
	if strings.Join(rest, " ") != "--not-a-flag prompt" {
		t.Fatalf("rest=%v", rest)
	}
}
