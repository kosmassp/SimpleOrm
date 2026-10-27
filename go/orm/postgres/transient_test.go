package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
)

// IsTransient (ADR-0047, spec/dialects.md "Transient open failures"): SQLSTATE class 08, 53300, 57P03 anywhere in the
// chain, the runtime's transport failures, and nothing else.
func TestIsTransientAnswersBySQLState(t *testing.T) {
	d := postgres.New()
	for _, code := range []string{"08000", "08001", "08003", "08004", "08006", "08007", "08P01", "53300", "57P03"} {
		if !d.IsTransient(&pgconn.PgError{Code: code}) {
			t.Errorf("SQLSTATE %s is transient", code)
		}
	}
	for _, code := range []string{"28000", "28P01", "3D000", "40001", "40P01", "57014", "42P01", "53200"} {
		if d.IsTransient(&pgconn.PgError{Code: code}) {
			t.Errorf("SQLSTATE %s is not transient", code)
		}
	}
}

func TestIsTransientWalksTheChain(t *testing.T) {
	d := postgres.New()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"wrapped", fmt.Errorf("failed to connect: %w", &pgconn.PgError{Code: "57P03"}), true},
		{"joined", errors.Join(errors.New("first host"), &pgconn.PgError{Code: "53300"}), true},
		{"a missing database beside a refused address: the engine answered", errors.Join(&pgconn.PgError{Code: "3D000"}, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), false},
		{"a refused dial", fmt.Errorf("failed to connect: %w", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), true},
		{"an EOF in the handshake", fmt.Errorf("startup: %w", io.EOF), true},
		{"the socket deadline", fmt.Errorf("read: %w", os.ErrDeadlineExceeded), true},
		{"connect_timeout elapsing", fmt.Errorf("failed to connect: %w", context.DeadlineExceeded), true},
		{"a cancellation", fmt.Errorf("failed to connect: %w", context.Canceled), false},
		{"a malformed address", &net.OpError{Op: "dial", Net: "tcp", Err: &net.AddrError{Err: "too many colons in address", Addr: "a:b:c"}}, false},
		{"anything else", errors.New("cannot parse connection string"), false},
	}
	for _, c := range cases {
		if got := d.IsTransient(c.err); got != c.want {
			t.Errorf("%s: IsTransient = %v, want %v", c.name, got, c.want)
		}
	}
}
