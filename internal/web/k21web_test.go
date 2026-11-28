package web

import (
	"net/http"
	"strings"
	"sync"
)

// sr11Accounts is one phone service with two accounts: the personal
// token is no longer valid (401 on every request), the work token has the
// session's thread "t".
type sr11Accounts struct {
	mu   sync.Mutex
	seen []string
}

func (f *sr11Accounts) serve(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path+" "+tok)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case tok != "fc_work":
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"token revoked"}}`))
	case strings.HasSuffix(r.URL.Path, "/me"):
		_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
	case r.Method == http.MethodPost:
		_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t"}}`))
	default:
		_, _ = w.Write([]byte(`{"thread":{"id":"t"}}`))
	}
}
