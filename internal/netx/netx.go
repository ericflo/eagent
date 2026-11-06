// Package netx is what every network client in eagent shares: a transport
// that notices connections the network took down, and one answer to "is
// this a network failure that waiting will fix?".
package netx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"
)

var transport = sync.OnceValue(func() http.RoundTripper {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport // replaced (tests guard it); use it as is
	}
	t = t.Clone()
	// A connection that died with the network (Wi-Fi dropped, laptop
	// slept) is otherwise only noticed when TCP gives up, many minutes
	// later, and a request sent on it waits that long. Pinging idle HTTP/2
	// connections finds it in under a minute.
	t.HTTP2 = &http.HTTP2Config{SendPingTimeout: 30 * time.Second, PingTimeout: 15 * time.Second}
	return t
})

// Transport is the shared HTTP transport: the default one, plus HTTP/2
// health pings. It is created on first use.
func Transport() http.RoundTripper { return transport() }

// Transient reports a network-level failure that passes on its own: DNS
// that cannot resolve right now, an unreachable network, a refused or reset
// connection, a connection closed before any answer, a timeout, a stream
// cut short. A certificate that does not
// verify is not one; neither is a cancelled context.
func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &verify) || errors.As(err, &authority) || errors.As(err, &host) || errors.As(err, &invalid) {
		return false
	}
	var dns *net.DNSError
	var op *net.OpError
	if errors.As(err, &dns) || errors.As(err, &op) {
		return true
	}
	for _, e := range []error{syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ECONNABORTED, syscall.ENETUNREACH, syscall.ENETDOWN,
		syscall.EHOSTUNREACH, syscall.EHOSTDOWN, syscall.EADDRNOTAVAIL, syscall.ETIMEDOUT, syscall.EPIPE, io.ErrUnexpectedEOF, io.EOF} {
		if errors.Is(err, e) {
			return true
		}
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}
