package netx

import (
	"net/http"
	"net/url"
	"testing"
)

func TestSameOriginRedirectsFollowsOnlyTheSameOriginOrAnUpgrade(t *testing.T) {
	for _, c := range []struct {
		from, to string
		follow   bool
	}{
		{"http://localhost:11434/v1/models", "http://localhost:11434/v1/models/", true},
		{"https://api.example.com/v1", "https://API.example.com:443/v2", true},
		{"http://gpu-box/v1", "https://gpu-box/v1", true},
		{"https://api.example.com/v1", "http://api.example.com/v1", false},
		{"https://api.example.com/v1", "https://evil.example.com/v1", false},
		{"https://example.com/v1", "https://api.example.com/v1", false},
		{"http://127.0.0.1:8080/v1", "http://127.0.0.1:9090/v1", false},
		{"http://gpu-box/v1", "https://evil/v1", false},
	} {
		from, _ := url.Parse(c.from)
		to, _ := url.Parse(c.to)
		err := SameOriginRedirects(&http.Request{URL: to}, []*http.Request{{URL: from}})
		if (err == nil) != c.follow {
			t.Errorf("%s -> %s: err %v, want follow %v", c.from, c.to, err, c.follow)
		}
	}
}
