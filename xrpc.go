package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"syscall"
	"time"
)

var httpClient = newHTTPClient(false)

// newHTTPClient builds the client for every request thumb makes. It never
// follows a redirect from HTTPS down to plain HTTP. With publicOnly set (used
// by `thumb serve`), it also refuses to connect to loopback, private,
// link-local and other non-public addresses. Strangers on the internet choose
// what a finger gateway looks up, and their DID documents choose which
// servers it talks to, so without this they could aim it at your internal
// network. The check runs on the resolved IP at connect time, so it also
// covers redirects and DNS tricks.
func newHTTPClient(publicOnly bool) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if publicOnly {
		d := &net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(network, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip, err := netip.ParseAddr(host)
				if err != nil || !isPublicAddr(ip) {
					return fmt.Errorf("refusing to connect to non-public address %s", host)
				}
				return nil
			},
		}
		t.DialContext = d.DialContext
		t.Proxy = nil // connect directly, so the address check sees the real destination
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: t,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing redirect from HTTPS to %s", req.URL.Scheme)
			}
			return nil
		},
	}
}

// Address ranges that aren't covered by netip's IsPrivate/IsLoopback/etc.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT, Tailscale
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 (can map to private IPv4)
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4 (can embed private IPv4)
}

func isPublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

type xrpcError struct {
	Status  int
	Code    string
	Message string
}

func (e *xrpcError) Error() string { return e.Code + ": " + e.Message }

// doJSON makes an HTTP request with an optional JSON body and decodes a JSON
// response into out (if non-nil).
func doJSON(method, endpoint string, params url.Values, body any, token string, out any) error {
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, endpoint, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "thumb/"+version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("cannot reach %s: %v", req.URL.Host, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strconv.Itoa(resp.StatusCode)
		}
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
		return &xrpcError{Status: resp.StatusCode, Code: e.Error, Message: e.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
