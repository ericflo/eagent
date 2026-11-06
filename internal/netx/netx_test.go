package netx

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
)

func TestTransient(t *testing.T) {
	wrap := func(err error) error { return &url.Error{Op: "Post", URL: "https://api.example.com/v1", Err: err} }
	for _, err := range []error{
		wrap(&net.DNSError{Err: "Temporary failure in name resolution", Name: "api.example.com", IsTemporary: true}),
		wrap(&net.DNSError{Err: "server misbehaving", Name: "api.example.com"}),
		wrap(&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENETUNREACH}),
		wrap(fmt.Errorf("dial: %w", syscall.EHOSTUNREACH)),
		wrap(fmt.Errorf("read: %w", syscall.ECONNRESET)),
		wrap(io.ErrUnexpectedEOF),
		context.DeadlineExceeded,
	} {
		if !Transient(err) {
			t.Errorf("not transient: %v", err)
		}
	}
	for _, err := range []error{
		nil,
		context.Canceled,
		wrap(x509.UnknownAuthorityError{}),
		wrap(x509.HostnameError{Host: "x"}),
		errors.New("api error 400: bad request"),
	} {
		if Transient(err) {
			t.Errorf("transient: %v", err)
		}
	}
	if Transport() == nil {
		t.Fatal("no transport")
	}
}
