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
	maxProbeAddresses   = 4
	hoursPerDay         = 24
)

var defaultProbeResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

var systemLookup = func(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, name)
}

type probeOptions struct {
	Name      string
	Addr      string
	Port      string
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
	if opts.Port == "" {
		opts.Port = probePort
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
	p.stage(stageQuery, func() (string, error) { return p.query(ctx) })
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
	ctx, cancel := context.WithTimeout(ctx, p.opts.Timeout)
	defer cancel()
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

func (p *prober) targets() []string {
	if p.opts.Addr != "" {
		return []string{p.opts.Addr}
	}
	var targets []string
	seen := map[string]bool{}
	for _, address := range p.found {
		if seen[address] {
			continue
		}
		seen[address] = true
		targets = append(targets, net.JoinHostPort(address, p.opts.Port))
		if len(targets) == maxProbeAddresses {
			break
		}
	}
	return targets
}

func (p *prober) handshake(ctx context.Context) (string, error) {
	targets := p.targets()
	if len(targets) == 0 {
		return "", errors.New("no address to connect to")
	}
	var lastErr error
	for _, target := range targets {
		detail, err := p.dial(ctx, target)
		if err == nil {
			return detail, nil
		}
		if lastErr = err; ctx.Err() != nil {
			break
		}
	}
	if len(targets) > 1 {
		return "", fmt.Errorf("%w (%d addresses tried)", lastErr, len(targets))
	}
	return "", lastErr
}

func (p *prober) dial(ctx context.Context, target string) (string, error) {
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
		return "", hideCertificateNames(err)
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		_ = conn.Close()
		return "", errors.New("the connection is not a TLS connection")
	}
	state := tlsConn.ConnectionState()
	if state.NegotiatedProtocol != probeALPN || len(state.PeerCertificates) == 0 {
		_ = tlsConn.Close()
		return "", fmt.Errorf("the server did not offer ALPN %s and a certificate", probeALPN)
	}
	p.conn = tlsConn
	left := time.Until(state.PeerCertificates[0].NotAfter)
	return fmt.Sprintf("%s, ALPN %s, %d days left on the certificate (%s)", tls.VersionName(state.Version), state.NegotiatedProtocol, int(left/(hoursPerDay*time.Hour)), target), nil
}

func hideCertificateNames(err error) error {
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return errors.New("the certificate does not cover " + probeRedacted)
	}
	return err
}

func (p *prober) query(ctx context.Context) (string, error) {
	if p.conn == nil {
		return "", errors.New("skipped: no tls connection")
	}
	if err := p.conn.SetDeadline(time.Now().Add(p.opts.Timeout)); err != nil {
		return "", err
	}
	stop := context.AfterFunc(ctx, func() { _ = p.conn.SetDeadline(time.Now()) })
	defer stop()
	conn := &dns.Conn{Conn: p.conn}
	question := new(dns.Msg).SetQuestion(dns.Fqdn(p.opts.Canary), dns.TypeA)
	if err := conn.WriteMsg(question); err != nil {
		return "", err
	}
	resp, err := conn.ReadMsg()
	if err != nil {
		return "", err
	}
	if resp.Id != question.Id {
		return "", errors.New("the answer has the wrong id")
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

func resolverStages(stages []probeStage) (asked, ok int) {
	for _, s := range stages {
		if s.Stage != stageSystem && strings.HasPrefix(s.Stage, stageResolvePrefix) {
			asked++
			if s.OK {
				ok++
			}
		}
	}
	return asked, ok
}

func probeVerdict(stages []probeStage) string {
	system, handshake := findStage(stages, stageSystem), findStage(stages, stageTLS)
	asked, found := resolverStages(stages)
	switch {
	case probePassed(stages):
		return "PASS: the system resolver finds the server, the TLS handshake works and a query is answered"
	case !system.OK && asked == 0:
		return "FAIL: the system resolver cannot find the name and no public resolver was asked (check the DNS record and that this is the name in use, or give -resolvers)"
	case !system.OK && found == 0:
		return "FAIL: no resolver finds the name (check that the DNS record exists and is DNS only, and that this is the name in use)"
	case !system.OK && handshake.OK:
		return "FAIL: this network's own resolver cannot find the server while a public resolver can (a resolver problem: try another network, or set Private DNS to Automatic until it clears)"
	case !handshake.OK:
		return "FAIL: the name resolves but the TLS handshake failed (port 853 is blocked on this path, the server is down, or the name or the certificate is wrong)"
	}
	return "FAIL: the handshake works but the server did not answer a query"
}
