package netx

import (
	"net/http"
	"net/url"
	"testing"
)

// Each hop is checked against the hop before it, where the key was just
// sent: after an upgrade to https a redirect back down to http is not
// followed. And an upgrade keeps the port: from http's default to https's,
// or the same port; another port on the host is another server.
func TestK1RedirectsAreCheckedHopByHopWithTheirPorts(t *testing.T) {
	for _, c := range []struct {
		via    []string
		to     string
		follow bool
	}{
		{[]string{"http://gpu-box/v1", "https://gpu-box/v1"}, "http://gpu-box/v1", false},
		{[]string{"http://gpu-box/v1", "https://gpu-box/v1"}, "https://gpu-box/v2", true},
		{[]string{"http://gpu-box:8080/v1"}, "https://gpu-box:9443/v1", false},
		{[]string{"http://gpu-box/v1"}, "https://gpu-box:9443/v1", false},
		{[]string{"http://gpu-box:8080/v1"}, "https://gpu-box/v1", false},
		{[]string{"http://gpu-box:80/v1"}, "https://gpu-box/v1", true},
		{[]string{"http://gpu-box:8443/v1"}, "https://gpu-box:8443/v1", true},
	} {
		var via []*http.Request
		for _, v := range c.via {
			u, _ := url.Parse(v)
			via = append(via, &http.Request{URL: u})
		}
		to, _ := url.Parse(c.to)
		err := SameOriginRedirects(&http.Request{URL: to}, via)
		if (err == nil) != c.follow {
			t.Errorf("%v -> %s: err %v, want follow %v", c.via, c.to, err, c.follow)
		}
	}
}
