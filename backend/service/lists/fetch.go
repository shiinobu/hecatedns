package lists

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	DefaultFetchTimeout = 30 * time.Second

	maxRedirects         = 5
	maxNameBytes         = 64
	listDirMode          = 0o700
	renameAttempts       = 5
	renameWait           = 40 * time.Millisecond
	bootstrapDialTimeout = 5 * time.Second
	dnsDefaultPort       = "53"
)

var (
	ErrHTTPSRequired    = errors.New("list URL must use https://")
	ErrTooManyRedirects = errors.New("list download exceeded 5 redirects")
	ErrEmptyList        = errors.New("list has no valid domain entries")
	ErrBadListName      = errors.New("list name is not a safe file name")
)

type Meta struct {
	ETag      string    `json:"etag,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Count     int       `json:"count"`
	Rejected  int       `json:"rejected"`
}

type FetchResult struct {
	Set         *engine.DomainSet
	Meta        Meta
	NotModified bool
}

type Fetcher struct {
	Dir              string
	Bootstrap        []string
	BootstrapTimeout time.Duration
	Client           *http.Client
	TLSConfig        *tls.Config
	Timeout          time.Duration
	MaxBytes         int64
	Now              func() time.Time

	mu        sync.Mutex
	transport *http.Transport
}

func (f *Fetcher) Fetch(ctx context.Context, src config.Source) (FetchResult, error) {
	return f.FetchWithCached(ctx, src, nil)
}

func (f *Fetcher) FetchWithCached(ctx context.Context, src config.Source, cached *engine.DomainSet) (FetchResult, error) {
	if err := validateSource(src); err != nil {
		return FetchResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, f.timeout())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetch %s: %w", src.Name, err)
	}
	prevETag := f.cachedETag(src.Name)
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}

	resp, err := f.httpClient().Do(req)
	if err != nil {
		return FetchResult{}, unwrapRedirectErr(src.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified && prevETag != "" {
		return f.handleNotModified(src, cached)
	}
	if resp.StatusCode != http.StatusOK {
		return FetchResult{}, fmt.Errorf("fetch %s: unexpected HTTP status %d", src.Name, resp.StatusCode)
	}
	return f.saveResponse(src, resp)
}

func (f *Fetcher) handleNotModified(src config.Source, cached *engine.DomainSet) (FetchResult, error) {
	if cached != nil && cached.Len() > 0 {
		meta, err := ReadMeta(f.Dir, src.Name)
		if err == nil {
			meta.Count = cached.Len()
			meta.FetchedAt = f.now()
			if err := writeMetaAtomic(f.Dir, src.Name, meta); err != nil {
				return FetchResult{}, err
			}
			return FetchResult{Set: cached, Meta: meta, NotModified: true}, nil
		}
	}
	set, meta, err := LoadCached(f.Dir, src)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetch %s (304 cache): %w", src.Name, err)
	}
	meta.FetchedAt = f.now()
	if err := writeMetaAtomic(f.Dir, src.Name, meta); err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Set: set, Meta: meta, NotModified: true}, nil
}

func (f *Fetcher) saveResponse(src config.Source, resp *http.Response) (FetchResult, error) {
	maxB := f.maxBytes()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxB+1))
	if err != nil {
		return FetchResult{}, fmt.Errorf("read %s body: %w", src.Name, err)
	}
	if int64(len(data)) > maxB {
		return FetchResult{}, fmt.Errorf("fetch %s: %w", src.Name, ErrListTooLarge)
	}
	set, rejected, err := parseWithLimits(bytes.NewReader(data), src.Format, maxB, MaxEntries)
	if err != nil {
		return FetchResult{}, fmt.Errorf("parse %s: %w", src.Name, err)
	}
	if set.Len() == 0 {
		return FetchResult{}, fmt.Errorf("parse %s: %w", src.Name, ErrEmptyList)
	}
	if err := writeFileAtomic(listPath(f.Dir, src.Name), data); err != nil {
		return FetchResult{}, err
	}
	meta := Meta{
		ETag:      strings.TrimSpace(resp.Header.Get("ETag")),
		FetchedAt: f.now(),
		Count:     set.Len(),
		Rejected:  rejected,
	}
	if err := writeMetaAtomic(f.Dir, src.Name, meta); err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Set: set, Meta: meta}, nil
}

func (f *Fetcher) cachedETag(name string) string {
	if _, err := os.Stat(listPath(f.Dir, name)); err != nil {
		return ""
	}
	meta, err := ReadMeta(f.Dir, name)
	if err != nil {
		return ""
	}
	return meta.ETag
}

func (f *Fetcher) httpClient() *http.Client {
	var base http.Client
	if f.Client != nil {
		base = *f.Client
	} else {
		base.Transport = f.sharedTransport()
	}
	base.Timeout = f.timeout()
	base.CheckRedirect = checkHTTPSRedirect
	return &base
}

func (f *Fetcher) sharedTransport() *http.Transport {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.transport == nil {
		f.transport = f.newTransport()
	}
	return f.transport
}

func (f *Fetcher) newTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if f.TLSConfig != nil {
		tr.TLSClientConfig = f.TLSConfig.Clone()
	}
	dialer := &net.Dialer{Timeout: f.bootstrapTimeout()}
	addrs := normalizeBootstrap(f.Bootstrap)
	if len(addrs) > 0 {
		tr.DialContext = f.dialBootstrapContext(dialer, addrs)
	} else {
		tr.DialContext = dialer.DialContext
	}
	return tr
}

func (f *Fetcher) dialBootstrapContext(dialer *net.Dialer, addrs []string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host == "" {
			return dialer.DialContext(ctx, network, address)
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return dialer.DialContext(ctx, network, address)
		}
		ips, err := resolveBootstrapHost(ctx, addrs, host, f.bootstrapTimeout())
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func resolveBootstrapHost(ctx context.Context, addrs []string, host string, timeout time.Duration) ([]netip.Addr, error) {
	qname := dns.Fqdn(host)
	var lastErr error
	for _, bsAddr := range addrs {
		ips, err := queryBootstrapAddr(ctx, bsAddr, qname, dns.TypeA, timeout)
		if err == nil && len(ips) == 0 {
			ips, err = queryBootstrapAddr(ctx, bsAddr, qname, dns.TypeAAAA, timeout)
		}
		if err == nil && len(ips) > 0 {
			return ips, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("bootstrap %s returned no addresses for %s", bsAddr, host)
		}
	}
	return nil, lastErr
}

func queryBootstrapAddr(ctx context.Context, bsAddr, qname string, qtype uint16, timeout time.Duration) ([]netip.Addr, error) {
	req := new(dns.Msg)
	req.SetQuestion(qname, qtype)
	req.RecursionDesired = true

	client := &dns.Client{Net: "udp", Timeout: timeout}
	resp, _, err := client.ExchangeContext(ctx, req, bsAddr)
	if err == nil && resp != nil && resp.Truncated {
		tcpClient := &dns.Client{Net: "tcp", Timeout: timeout}
		resp, _, err = tcpClient.ExchangeContext(ctx, req, bsAddr)
	}
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("bootstrap %s rcode=%d", bsAddr, msgRcode(resp))
	}
	var ips []netip.Addr
	for _, rr := range resp.Answer {
		switch rec := rr.(type) {
		case *dns.A:
			if ip, ok := netip.AddrFromSlice(rec.A); ok {
				ips = append(ips, ip.Unmap())
			}
		case *dns.AAAA:
			if ip, ok := netip.AddrFromSlice(rec.AAAA); ok {
				ips = append(ips, ip)
			}
		}
	}
	return ips, nil
}

func msgRcode(m *dns.Msg) int {
	if m == nil {
		return -1
	}
	return m.Rcode
}

func normalizeBootstrap(raw []string) []string {
	var out []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, err := netip.ParseAddrPort(s); err == nil {
			out = append(out, s)
			continue
		}
		if ip, err := netip.ParseAddr(s); err == nil {
			out = append(out, net.JoinHostPort(ip.String(), dnsDefaultPort))
		}
	}
	return out
}

func checkHTTPSRedirect(req *http.Request, via []*http.Request) error {
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return ErrHTTPSRequired
	}
	if len(via) > maxRedirects {
		return ErrTooManyRedirects
	}
	return nil
}

func unwrapRedirectErr(name string, err error) error {
	if errors.Is(err, ErrHTTPSRequired) {
		return fmt.Errorf("fetch %s: %w", name, ErrHTTPSRequired)
	}
	if errors.Is(err, ErrTooManyRedirects) {
		return fmt.Errorf("fetch %s: %w", name, ErrTooManyRedirects)
	}
	return fmt.Errorf("fetch %s: %w", name, err)
}

func (f *Fetcher) timeout() time.Duration {
	if f != nil && f.Timeout > 0 {
		return f.Timeout
	}
	return DefaultFetchTimeout
}

func (f *Fetcher) bootstrapTimeout() time.Duration {
	if f != nil && f.BootstrapTimeout > 0 {
		return f.BootstrapTimeout
	}
	return bootstrapDialTimeout
}

func (f *Fetcher) maxBytes() int64 {
	if f != nil && f.MaxBytes > 0 {
		return f.MaxBytes
	}
	return MaxListBytes
}

func (f *Fetcher) now() time.Time {
	if f != nil && f.Now != nil {
		return f.Now()
	}
	return time.Now().Round(time.Second)
}

func LoadCached(dir string, src config.Source) (*engine.DomainSet, Meta, error) {
	if !validListName(src.Name) {
		return nil, Meta{}, ErrBadListName
	}
	if !validFormat(src.Format) {
		return nil, Meta{}, ErrUnknownFormat
	}
	p := listPath(dir, src.Name)
	file, err := os.Open(p)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("open cached list %s: %w", src.Name, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, Meta{}, fmt.Errorf("stat cached list %s: %w", src.Name, err)
	}
	set, rejected, err := Parse(file, src.Format)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("parse cached list %s: %w", src.Name, err)
	}
	if set.Len() == 0 {
		return nil, Meta{}, fmt.Errorf("cached list %s: %w", src.Name, ErrEmptyList)
	}
	meta, _ := ReadMeta(dir, src.Name)
	meta.Count = set.Len()
	meta.Rejected = rejected
	if meta.FetchedAt.IsZero() {
		meta.FetchedAt = info.ModTime()
	}
	return set, meta, nil
}

func ReadMeta(dir, name string) (Meta, error) {
	if !validListName(name) {
		return Meta{}, ErrBadListName
	}
	raw, err := os.ReadFile(metaPath(dir, name))
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return Meta{}, fmt.Errorf("decode meta %s: %w", name, err)
	}
	return m, nil
}

func writeMetaAtomic(dir, name string, m Meta) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode meta %s: %w", name, err)
	}
	raw = append(raw, '\n')
	return writeFileAtomic(metaPath(dir, name), raw)
}

func Dir(dataDir string) string {
	return filepath.Join(dataDir, "lists")
}

func listPath(dir, name string) string {
	return filepath.Join(dir, name+".txt")
}

func metaPath(dir, name string) string {
	return filepath.Join(dir, name+".meta.json")
}

func validateSource(src config.Source) error {
	if !validListName(src.Name) {
		return ErrBadListName
	}
	if !validFormat(src.Format) {
		return ErrUnknownFormat
	}
	u, err := url.Parse(src.URL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return fmt.Errorf("fetch %s: %w", src.Name, ErrHTTPSRequired)
	}
	return nil
}

func validListName(name string) bool {
	if len(name) == 0 || len(name) > maxNameBytes {
		return false
	}
	if !isAlphaNum(name[0]) {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isAlphaNum(c) && c != '-' && c != '_' {
			return false
		}
	}
	return !isWindowsDeviceName(name)
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isWindowsDeviceName(name string) bool {
	switch strings.ToLower(name) {
	case "con", "prn", "aux", "nul",
		"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return true
	}
	return false
}

func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, listDirMode); err != nil {
		return fmt.Errorf("create list dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".list-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write temp file %s: %w", tmp.Name(), err)
	}
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if err = os.Rename(tmp.Name(), path); err == nil {
			return nil
		}
		time.Sleep(renameWait)
	}
	return fmt.Errorf("replace %s: %w", path, err)
}
