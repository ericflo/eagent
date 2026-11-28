package netx

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// SameOriginRedirects is a CheckRedirect for clients that send a key. Go
// copies headers onto the next hop: x-api-key always, Authorization to the
// same host on any port, its subdomains, and down from https to http. So a
// redirect is followed only to the origin of the hop before it (scheme,
// host and port), or from http up to https on the same host and port, or
// from http's default port to https's. Anything else comes back as the
// redirect response itself, which callers already read as a failed status.
// A local server moving a path on its own address keeps working.
func SameOriginRedirects(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	from, to := via[len(via)-1].URL, req.URL
	upgrade := from.Scheme == "http" && to.Scheme == "https" && strings.EqualFold(from.Hostname(), to.Hostname()) && (port(from) == port(to) || port(from) == "80" && port(to) == "443")
	if sameOrigin(from, to) || upgrade {
		return nil
	}
	return http.ErrUseLastResponse
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
