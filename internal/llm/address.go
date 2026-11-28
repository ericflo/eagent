package llm

import "strings"

// SameBaseURL reports whether two base URLs are one address: a trailing
// slash, which a configuration may or may not write, makes no difference.
// Everything that matches a route by its address compares it this way.
func SameBaseURL(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}
