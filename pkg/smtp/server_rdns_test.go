package smtp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

// TestClassifyRDNSResult locks in the behavior introduced when we split the
// PTR-lookup outcome into rdnsSuccess / rdnsMissing / rdnsTransient. Collapsing
// rdnsTransient back into rdnsMissing would re-introduce the bug where
// legitimate senders with a valid `dig -x` PTR were rejected on any DNS hiccup.
func TestClassifyRDNSResult(t *testing.T) {
	// A *net.DNSError that LookupAddr would produce for an NXDOMAIN or empty
	// answer — the "PTR genuinely does not exist" signal.
	notFound := &net.DNSError{Err: "no such host", Name: "1.2.3.4.in-addr.arpa.", IsNotFound: true}

	// What Go's resolver synthesizes when the underlying transport fails
	// (e.g. our Dial returned an error, or the server didn't answer in time).
	timeout := &net.DNSError{Err: "i/o timeout", Name: "1.2.3.4.in-addr.arpa.", IsTimeout: true}
	servfail := &net.DNSError{Err: "server misbehaving", Name: "1.2.3.4.in-addr.arpa.", IsTemporary: true}

	tests := []struct {
		name  string
		names []string
		err   error
		want  rdnsResult
	}{
		{
			name:  "success with one PTR",
			names: []string{"mail.example.com."},
			err:   nil,
			want:  rdnsSuccess,
		},
		{
			name:  "success with multiple PTRs",
			names: []string{"a.example.com.", "b.example.com."},
			err:   nil,
			want:  rdnsSuccess,
		},
		{
			name:  "NXDOMAIN — no PTR exists",
			names: nil,
			err:   notFound,
			want:  rdnsMissing,
		},
		{
			name:  "empty answer with no error — no PTR exists",
			names: []string{},
			err:   nil,
			want:  rdnsMissing,
		},
		{
			name:  "DNS timeout — transient",
			names: nil,
			err:   timeout,
			want:  rdnsTransient,
		},
		{
			name:  "SERVFAIL — transient",
			names: nil,
			err:   servfail,
			want:  rdnsTransient,
		},
		{
			name:  "context deadline exceeded — transient",
			names: nil,
			err:   context.DeadlineExceeded,
			want:  rdnsTransient,
		},
		{
			name:  "generic non-DNS error — transient",
			names: nil,
			err:   errors.New("connection refused"),
			want:  rdnsTransient,
		},
		{
			name:  "wrapped IsNotFound — still no PTR",
			names: nil,
			err:   fmt.Errorf("lookup failed: %w", notFound),
			want:  rdnsMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyRDNSResult(tt.names, tt.err)
			if got != tt.want {
				t.Errorf("classifyRDNSResult(%v, %v) = %v; want %v", tt.names, tt.err, got, tt.want)
			}
		})
	}
}
