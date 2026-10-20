package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const (
	probePort           = "853"
	probeALPN           = "dot"
	defaultProbeCanary  = "example.com"
	defaultProbeTimeout = 5 * time.Second
	probeRedacted       = "<name>"
	stageSystem         = "resolve system"
	stageResolvePrefix  = "resolve "
	stageTLS            = "tls"
	stageQuery          = "query"
	maxShownAddresses   = 4
	hoursPerDay         = 24
)

var defaultProbeResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

var systemLookup = func(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, name)
}

type probeOptions struct {
	Name      string
	Addr      string
	Resolvers []string
	Canary    string
	Timeout   time.Duration
	RootCAs   *x509.CertPool
	Lookup    func(ctx context.Context, name string) ([]string, error)
}

type probeStage struct {
	Stage  string
	OK     bool
	Detail string
	Took   time.Duration
}

type prober struct {
	opts   probeOptions
	found  []string
	conn   *tls.Conn
	stages []probeStage
}

func probe(ctx context.Context, opts probeOptions) []probeStage {
	if opts.Lookup == nil {
		opts.Lookup = systemLookup
	}
	if opts.Canary == "" {
		opts.Canary = defaultProbeCanary
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultProbeTimeout
	}
	p := &prober{opts: opts}
	p.stage(stageSystem, func() (string, error) { return p.system(ctx) })
	for _, resolver := range opts.Resolvers {
		p.stage(stageResolvePrefix+resolver, func() (string, error) { return p.resolver(ctx, resolver) })
	}
	p.stage(stageTLS, func() (string, error) { return p.handshake(ctx) })
	p.stage(stageQuery, func() (string, error) { return p.query() })
	if p.conn != nil {
		_ = p.conn.Close()
	}
	return p.stages
}

func (p *prober) stage(name string, run func() (string, error)) {
	start := time.Now()
	detail, err := run()
	if err != nil {
		detail = err.Error()
	}
	p.stages = append(p.stages, probeStage{
		Stage:  name,
		OK:     err == nil,
		Detail: strings.ReplaceAll(detail, p.opts.Name, probeRedacted),
		Took:   time.Since(start),
	})
}

func (p *prober) system(ctx context.Context) (string, error) {
	addresses, err := p.opts.Lookup(ctx, p.opts.Name)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", errors.New("no address")
	}
	p.found = append(p.found, addresses...)
	return describeAddresses(addresses), nil
}

func (p *prober) resolver(ctx context.Context, resolver string) (string, error) {
	client := &dns.Client{Net: "udp", Timeout: p.opts.Timeout}
	query := new(dns.Msg).SetQuestion(dns.Fqdn(p.opts.Name), dns.TypeA)
	resp, _, err := client.ExchangeContext(ctx, query, resolver)
	if err != nil {
		return "", err
	}
	if resp.Rcode != dns.RcodeSuccess {
		return "", errors.New(dns.RcodeToString[resp.Rcode])
	}
	var addresses []string
	for _, record := range resp.Answer {
		if a, ok := record.(*dns.A); ok {
			addresses = append(addresses, a.A.String())
		}
	}
	if len(addresses) == 0 {
		return "", errors.New("no A record")
	}
	p.found = append(p.found, addresses...)
	return describeAddresses(addresses), nil
}

func (p *prober) target() (string, error) {
	if p.opts.Addr != "" {
		return p.opts.Addr, nil
	}
	if len(p.found) == 0 {
		return "", errors.New("no address to connect to")
	}
	return net.JoinHostPort(p.found[0], probePort), nil
}

func (p *prober) handshake(ctx context.Context) (string, error) {
	target, err := p.target()
	if err != nil {
		return "", err
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: p.opts.Timeout},
		Config: &tls.Config{
			ServerName: p.opts.Name,
			RootCAs:    p.opts.RootCAs,
			MinVersion: tls.VersionTLS13,
			NextProtos: []string{probeALPN},
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return "", err
	}
	tlsConn := conn.(*tls.Conn)
	state := tlsConn.ConnectionState()
	if state.NegotiatedProtocol != probeALPN {
		_ = tlsConn.Close()
		return "", fmt.Errorf("the server did not offer ALPN %s", probeALPN)
	}
	p.conn = tlsConn
	left := time.Until(state.PeerCertificates[0].NotAfter)
	return fmt.Sprintf("%s, ALPN %s, %d days left on the certificate (%s)", tls.VersionName(state.Version), state.NegotiatedProtocol, int(left/(hoursPerDay*time.Hour)), target), nil
}

func (p *prober) query() (string, error) {
	if p.conn == nil {
		return "", errors.New("skipped: no tls connection")
	}
	if err := p.conn.SetDeadline(time.Now().Add(p.opts.Timeout)); err != nil {
		return "", err
	}
	conn := &dns.Conn{Conn: p.conn}
	if err := conn.WriteMsg(new(dns.Msg).SetQuestion(dns.Fqdn(p.opts.Canary), dns.TypeA)); err != nil {
		return "", err
	}
	resp, err := conn.ReadMsg()
	if err != nil {
		return "", err
	}
	detail := fmt.Sprintf("%s, %s", dns.RcodeToString[resp.Rcode], countAnswers(len(resp.Answer)))
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		return "", errors.New(detail)
	}
	return detail, nil
}

func countAnswers(n int) string {
	if n == 1 {
		return "1 answer"
	}
	return fmt.Sprintf("%d answers", n)
}

func describeAddresses(addresses []string) string {
	if len(addresses) > maxShownAddresses {
		return strings.Join(addresses[:maxShownAddresses], ", ") + ", ..."
	}
	return strings.Join(addresses, ", ")
}

func findStage(stages []probeStage, name string) probeStage {
	for _, s := range stages {
		if s.Stage == name {
			return s
		}
	}
	return probeStage{Stage: name}
}

func probePassed(stages []probeStage) bool {
	return findStage(stages, stageSystem).OK && findStage(stages, stageTLS).OK && findStage(stages, stageQuery).OK
}

func anyResolverOK(stages []probeStage) bool {
	for _, s := range stages {
		if strings.HasPrefix(s.Stage, stageResolvePrefix) && s.OK {
			return true
		}
	}
	return false
}

func probeVerdict(stages []probeStage) string {
	switch {
	case probePassed(stages):
		return "PASS: the system resolver finds the server, the TLS handshake works and a query is answered"
	case !findStage(stages, stageSystem).OK && anyResolverOK(stages):
		return "FAIL: this network's own resolver cannot find the server while a public resolver can (a resolver problem: try another network, or set Private DNS to Automatic until it clears)"
	case !findStage(stages, stageSystem).OK:
		return "FAIL: no resolver finds the name (check that the DNS record exists and is DNS only, and that this is the name in use)"
	case !findStage(stages, stageTLS).OK:
		return "FAIL: the name resolves but the TLS handshake failed (port 853 is blocked on this path, the server is down, or the name or the certificate is wrong)"
	}
	return "FAIL: the handshake works but the server did not answer a query"
}
