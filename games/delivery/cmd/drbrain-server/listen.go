package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// bind listens on addr, turning the commonest local mistake — a server that is
// already running — into a message that says what is actually wrong.
//
// net.Listen's own "bind: address already in use" is accurate but easy to
// misread: the operator's next move is to rebuild and rerun, which changes
// nothing while the old process is the one still answering. The failure then
// surfaces much later and somewhere else, as the UI rejecting its own query
// because the running server still has the schema from before the last commit.
// So on a conflict we probe the port and report what is listening there.
func bind(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	if !isAddrInUse(err) {
		return nil, err
	}
	return nil, fmt.Errorf("%s is already in use%s\n%s", addr, occupant(addr), stopHint(addr))
}

// isAddrInUse reports whether the listen failed because something else holds
// the address. The errno is the authority; the string match is a fallback for
// paths that do not unwrap to a syscall.Errno.
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return strings.Contains(err.Error(), "address already in use")
}

// occupant names what is answering on addr, when it can be identified.
//
// A DrBrain server is recognised by answering the health query; that is the
// distinction that matters, because only a DrBrain server can serve a stale
// schema to this UI. An unrelated process on the same port is a different
// problem, and claiming it is one of ours would send the operator down the
// wrong path, so anything unrecognised yields no description at all.
func occupant(addr string) string {
	client := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Post(
		"http://"+dialable(addr)+"/graphql",
		"application/json",
		strings.NewReader(`{"query":"{ health { status } }"}`),
	)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var body struct {
		Data struct {
			Health struct {
				Status string `json:"status"`
			} `json:"health"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	if body.Data.Health.Status == "" {
		return ""
	}

	return fmt.Sprintf(
		" by a running DrBrain server (health %q).\n"+
			"If you just rebuilt, it is still serving its old GraphQL schema, so the UI\n"+
			"can fail with `Cannot query field \"...\" on type \"...\"` no matter how many\n"+
			"times the new build is started",
		body.Data.Health.Status,
	)
}

// stopHint names the address to inspect and how to inspect it.
func stopHint(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = addr
	}
	return "Stop it, then rerun:\n" +
		fmt.Sprintf("  lsof -nP -iTCP:%s -sTCP:LISTEN\n", port) +
		"  kill <pid>"
}

// dialable turns a listen address into one a client can reach. A wildcard host
// binds every interface but is not itself a destination, so probe loopback.
func dialable(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
