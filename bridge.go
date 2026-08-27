package main

import (
	"io"
	"net"
	"os"
)

// bridge attaches the calling client to a running daemon: stdin goes to the
// socket, the socket comes back on stdout. It is a pipe, not a server, so a
// docker exec'd stdio session shares the daemon's syncer and index instead
// of starting its own. Returns when stdin closes.
func bridge(sock string) error {
	return bridgeIO(sock, os.Stdin, os.Stdout)
}

func bridgeIO(sock string, in io.Reader, out io.Writer) error {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		_, _ = io.Copy(conn, in)
		if c, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
	}()
	_, err = io.Copy(out, conn)
	return err
}
