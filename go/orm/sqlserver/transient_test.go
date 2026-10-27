package sqlserver_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// IsTransient (ADR-0047, spec/dialects.md "Transient open failures"): the error numbers of the spec's set anywhere in
// the chain and in any of the records an error carries, the runtime's transport failures, and nothing else.
func TestIsTransientAnswersByTheErrorNumberSet(t *testing.T) {
	d := sqlserver.New()
	every := []int32{-2, 20, 64, 121, 233, 258, 997, 4060, 4221, 10053, 10054, 10060, 10061, 10928, 10929, 11001, 17197, 18401,
		40143, 40197, 40501, 40540, 40613, 42108, 42109, 49918, 49919, 49920}
	for _, number := range every {
		if !d.IsTransient(mssql.Error{Number: number}) {
			t.Errorf("number %d is in the set", number)
		}
	}
	for _, number := range []int32{18456, 1204, 1205, 1222, 208, 0} {
		if d.IsTransient(mssql.Error{Number: number}) {
			t.Errorf("number %d is not transient", number)
		}
	}
}

func TestIsTransientWalksTheChainAndEveryRecord(t *testing.T) {
	d := sqlserver.New()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"a later record carries the number", mssql.Error{Number: 18456, All: []mssql.Error{{Number: 18456}, {Number: 40613}}}, true},
		{"wrapped", fmt.Errorf("open: %w", mssql.Error{Number: 4060}), true},
		{"a pointer", fmt.Errorf("open: %w", &mssql.Error{Number: 40501}), true},
		{"joined", errors.Join(errors.New("first"), mssql.Error{Number: 10054}), true},
		{"login failed", fmt.Errorf("login error: %w", mssql.Error{Number: 18456}), false},
		{"login failed beside a transport failure: the engine answered", errors.Join(mssql.Error{Number: 18456}, io.EOF), false},
		{"a dial failure", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, true},
		{"a DNS failure", &net.DNSError{Err: "no such host", Name: "nowhere", IsNotFound: true}, true},
		{"a reset", fmt.Errorf("read: %w", syscall.ECONNRESET), true},
		{"an EOF in the handshake", fmt.Errorf("prelogin: %w", io.ErrUnexpectedEOF), true},
		{"a deadline that is not the caller's", fmt.Errorf("dial: %w", context.DeadlineExceeded), true},
		{"a malformed address", &net.OpError{Op: "dial", Net: "tcp", Err: &net.AddrError{Err: "missing port in address", Addr: "host"}}, false},
		{"a cancellation", context.Canceled, false},
		{"anything else", errors.New("tls: bad certificate"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := d.IsTransient(c.err); got != c.want {
			t.Errorf("%s: IsTransient = %v, want %v", c.name, got, c.want)
		}
	}
}
