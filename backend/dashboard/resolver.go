package dashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const (
	defaultProbeName       = "example.com"
	defaultResolverTimeout = 3 * time.Second
	netUDP                 = "udp"
	netTLS                 = "tcp-tls"
)

type ResolverOptions struct {
	Addr       string
	ServerName string
	Name       string
	RootCAs    *x509.CertPool
	Timeout    time.Duration
}

type ResolverResult struct {
	OK    bool   `json:"ok"`
	MS    int64  `json:"ms"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

func NewResolverCheck(o ResolverOptions) func(context.Context) ResolverResult {
	name := o.Name
	if name == "" {
		name = defaultProbeName
	}
	client := &dns.Client{Net: netUDP, Timeout: orDefault(o.Timeout, defaultResolverTimeout)}
	if o.ServerName != "" {
		client.Net = netTLS
		client.TLSConfig = &tls.Config{ServerName: o.ServerName, RootCAs: o.RootCAs, MinVersion: tls.VersionTLS13}
	}
	return func(ctx context.Context) ResolverResult {
		query := new(dns.Msg)
		query.SetQuestion(dns.Fqdn(name), dns.TypeA)
		start := time.Now()
		resp, _, err := client.ExchangeContext(ctx, query, o.Addr)
		return interpretAnswer(name, time.Since(start), resp, err)
	}
}

func interpretAnswer(name string, elapsed time.Duration, resp *dns.Msg, err error) ResolverResult {
	fail := func(reason string) ResolverResult {
		return ResolverResult{Name: name, Error: reason}
	}
	switch {
	case err != nil:
		return fail(shortResolverError(err))
	case resp.Rcode != dns.RcodeSuccess:
		return fail(dns.RcodeToString[resp.Rcode])
	case len(resp.Answer) == 0:
		return fail("empty answer")
	}
	return ResolverResult{OK: true, MS: elapsed.Milliseconds(), Name: name}
}

func shortResolverError(err error) string {
	var verify *tls.CertificateVerificationError
	var netErr net.Error
	text := err.Error()
	switch {
	case errors.As(err, &verify):
		return "certificate not accepted"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return "timeout"
	case strings.Contains(text, "tls:"):
		return "TLS handshake failed"
	case strings.Contains(text, "refused"):
		return "connection refused"
	}
	return "query failed"
}
