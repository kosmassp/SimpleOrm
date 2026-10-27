package core

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
)

// IsTransientTransport is the runtime row of spec/dialects.md "Transient open failures" (ADR-0047), shared by the
// network dialects: a socket, I/O, or timeout failure of the Go runtime anywhere in err's cause chain (errors.As and
// errors.Is walk both the single and the joined Unwrap form) — a net.Error (a dial failure, *net.OpError, a DNS
// failure, a timeout), ECONNREFUSED/ECONNRESET/ECONNABORTED, io.EOF or io.ErrUnexpectedEOF during the handshake,
// os.ErrDeadlineExceeded, or context.DeadlineExceeded. The last one is transient only when it is not the caller's own
// context ending; the session checks the caller's context before it asks the dialect (spec/session.md "Resilience"), so
// this function — a pure function of the failure — answers true for it.
//
// A malformed address (*net.AddrError, *net.ParseError, net.InvalidAddrError, net.UnknownNetworkError) would fail the
// same way the second time; a chain carrying one is not transient even though those types satisfy net.Error.
func IsTransientTransport(err error) bool {
	if err == nil || isMalformedAddress(err) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	for _, target := range []error{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED,
		io.EOF, io.ErrUnexpectedEOF, os.ErrDeadlineExceeded, context.DeadlineExceeded,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func isMalformedAddress(err error) bool {
	var addrErr *net.AddrError
	var parseErr *net.ParseError
	var invalidAddr net.InvalidAddrError
	var unknownNetwork net.UnknownNetworkError
	return errors.As(err, &addrErr) || errors.As(err, &parseErr) || errors.As(err, &invalidAddr) || errors.As(err, &unknownNetwork)
}
