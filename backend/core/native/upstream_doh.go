package native

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/miekg/dns"
)

const (
	dohMediaType  = "application/dns-message"
	dohDefaultURI = "/dns-query"

	// maxDNSMessage is the largest DNS message there is; a longer HTTP body is
	// not an answer.
	maxDNSMessage = 65535
)

// dohUpstream speaks DNS over HTTPS (RFC 8484, POST). It keeps connections
// alive, so a hostname is resolved through the bootstrap resolver only when a
// new connection is made.
type dohUpstream struct {
	endpoint string
	client   *http.Client
	timeout  time.Duration
}

// newDoH builds a DoH upstream for an https URL. boot resolves a hostname and
// may be nil when the URL holds an IP address. tlsCfg may be nil for the system
// roots.
func newDoH(rawURL string, boot *bootstrap, timeout time.Duration, tlsCfg *tls.Config) (*dohUpstream, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("%q is not an https URL with a host", rawURL)
	}
	if tlsCfg == nil {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	transport := &http.Transport{
		// Never the environment proxy: DNS must not leave through one.
		Proxy:                 nil,
		DialContext:           boot.dialContext(timeout),
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		// A DoH endpoint that redirects is misconfigured or hostile.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &dohUpstream{endpoint: u.String(), client: client, timeout: timeout}, nil
}

func (u *dohUpstream) Address() string { return u.endpoint }

func (u *dohUpstream) Close() error {
	u.client.CloseIdleConnections()
	return nil
}

// Exchange implements upstream. RFC 8484 asks for ID 0 so that identical
// queries are cacheable; the caller's ID is restored on the reply.
func (u *dohUpstream) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	sent := q.Copy()
	sent.Id = 0
	wire, err := sent.Pack()
	if err != nil {
		return nil, fmt.Errorf("%s: packing the query: %w", u.endpoint, err)
	}
	body, err := u.post(ctx, wire)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u.endpoint, err)
	}
	resp := new(dns.Msg)
	if err := resp.Unpack(body); err != nil {
		return nil, fmt.Errorf("%s: unpacking the answer: %w", u.endpoint, err)
	}
	if err := checkReply(sent, resp); err != nil {
		return nil, fmt.Errorf("%s: %w", u.endpoint, err)
	}
	resp.Id = q.Id
	return resp, nil
}

// post sends wire and returns the body of a 200 reply of the DNS media type.
func (u *dohUpstream) post(ctx context.Context, wire []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", dohMediaType)
	req.Header.Set("Accept", dohMediaType)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %s", resp.Status)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != dohMediaType {
		return nil, fmt.Errorf("content type %q, want %s", resp.Header.Get("Content-Type"), dohMediaType)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDNSMessage+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDNSMessage {
		return nil, errors.New("answer is larger than any DNS message")
	}
	return body, nil
}
