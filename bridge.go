package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
)

// errNoDaemon reports that nothing answered on the socket. A stale mcp.sock
// outlives an unclean exit and looks exactly like a live daemon to main's
// mode check, so the dial failure is wrapped rather than returned raw: main
// falls back to running the daemon itself, which clears the file.
var errNoDaemon = errors.New("no daemon on the socket")

// bridge attaches the calling client to a running daemon: stdin goes to the
// socket, the socket comes back on stdout. It is a pipe, not a server, so a
// docker exec'd stdio session shares the daemon's syncer and index instead
// of starting its own. Returns when stdin closes or ctx is cancelled.
func bridge(ctx context.Context, sock string) error {
	return bridgeIO(ctx, sock, os.Stdin, os.Stdout)
}

func bridgeIO(ctx context.Context, sock string, in io.Reader, out io.Writer) error {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return fmt.Errorf("%w: %w", errNoDaemon, err)
	}
	defer conn.Close()
	// main registers a signal handler for every mode, which suppresses the
	// runtime's default terminate-on-SIGINT/SIGTERM. bridge has no other way
	// to hear about it, so closing conn on ctx.Done is what makes Ctrl-C (or
	// docker stop) actually end a bridge session instead of hanging until
	// the daemon side closes first.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	go func() {
		_, _ = io.Copy(conn, in)
		if c, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
	}()
	_, err = io.Copy(out, conn)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
