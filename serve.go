package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

func (a *app) cmdServe(args []string) error {
	fs := a.flags("serve", "thumb serve [--host 0.0.0.0] [--port 79] [-n 1]")
	host := fs.String("host", "0.0.0.0", "address to listen on")
	port := fs.Int("port", 79, "port to listen on (79 usually needs root)")
	n := fs.Int("n", 1, "plan entries per response")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*port)))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.errw, "finger gateway listening on %s\n", ln.Addr())
	return serveFinger(ln, max(1, min(*n, 100)))
}

// serveFinger runs an RFC 1288 finger server. `finger alice.bsky.social@host`
// asks it for "alice.bsky.social", which it looks up over the AT Protocol.
func serveFinger(ln net.Listener, n int) error {
	slots := make(chan struct{}, 64) // cap concurrent lookups
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				handleFinger(c, n)
			}()
		default:
			io.WriteString(c, "finger: server busy, try again later.\r\n")
			c.Close()
		}
	}
}

func handleFinger(c net.Conn, n int) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(c, 512)).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	c.SetWriteDeadline(time.Now().Add(30 * time.Second))

	q := strings.TrimSpace(line)
	long := strings.HasPrefix(q, "/W")
	if long {
		q = strings.TrimSpace(q[2:])
	}
	var out string
	switch {
	case q == "":
		out = "thumb gateway: AT Protocol .plan files over finger.\nUsage: finger <handle>@this-host\n"
	case strings.Contains(q, "@"):
		out = "finger: forwarding is not supported.\n"
	default:
		if s, err := render(q, n, long); err != nil {
			out = "finger: " + err.Error() + "\n"
		} else {
			out = s
		}
	}
	io.WriteString(c, strings.ReplaceAll(out, "\n", "\r\n"))
}
