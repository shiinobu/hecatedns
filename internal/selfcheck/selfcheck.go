// Package selfcheck runs pre-flight checks against upstreams, SafeSearch, and
// cached blocklists.
package selfcheck

// agy wrote this

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AdguardTeam/dnsproxy/upstream"
	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/internal/config"
	"github.com/shiinobu/hecatedns/internal/engine"
	"github.com/shiinobu/hecatedns/internal/lists"
)

const (
	// DefaultTimeout bounds each upstream exchange during self-check.
	DefaultTimeout = 5 * time.Second

	safeSearchProbe  = "www.google.com."
	safeSearchTarget = "forcesafesearch.google.com"
)

// Options configures Run.
type Options struct {
	// Timeout bounds each upstream exchange; zero means DefaultTimeout.
	Timeout time.Duration
}

// ListResult summarizes the check of one enabled blocklist source.
type ListResult struct {
	Name         string
	Kind         string
	Cached       bool
	Entries      int
	SampleDomain string
	SampleAnswer string
}

// Result holds the outcome of Run.
type Result struct {
	UpstreamsChecked int
	UpstreamsOK      int
	SafeSearchOK     bool
	Lists            []ListResult
	Warnings         []string
}

// Run executes the self-check suite against cfg:
//  1. Verifies no upstream or bootstrap loops back to a listen address (Q3).
//  2. Queries www.google.com (A) through the configured upstreams and verifies
//     that at least one upstream answers and none chains to forcesafesearch.google.com (S3).
//  3. Loads cached lists from disk and verifies that one sample domain from each
//     cached blocklist is blocked by the engine (S1).
func Run(ctx context.Context, cfg config.Config, opts Options) (Result, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	var res Result
	var errs []error

	for _, loop := range cfg.LoopWarnings() {
		errs = append(errs, errors.New(loop))
	}
	if len(errs) > 0 {
		return res, errors.Join(errs...)
	}

	if err := checkUpstreamsAndSafeSearch(ctx, cfg, timeout, &res); err != nil {
		errs = append(errs, err)
	}
	if err := checkCachedLists(ctx, cfg, &res); err != nil {
		errs = append(errs, err)
	}
	return res, errors.Join(errs...)
}

func checkUpstreamsAndSafeSearch(ctx context.Context, cfg config.Config, timeout time.Duration, res *Result) error {
	req := new(dns.Msg)
	req.SetQuestion(safeSearchProbe, dns.TypeA)

	var upstreamErrs []error
	var safeSearchErrs []error

	for _, addr := range cfg.Upstreams {
		res.UpstreamsChecked++
		resp, err := exchangeOne(ctx, addr, cfg.Bootstrap, timeout, req)
		if err != nil {
			upstreamErrs = append(upstreamErrs, fmt.Errorf("upstream %s: %w", addr, err))
			continue
		}
		if resp == nil || resp.Rcode != dns.RcodeSuccess {
			rcode := "nil"
			if resp != nil {
				rcode = dns.RcodeToString[resp.Rcode]
			}
			upstreamErrs = append(upstreamErrs, fmt.Errorf("upstream %s menjawab %s untuk %s", addr, rcode, safeSearchProbe))
			continue
		}
		if target, forced := hasSafeSearchCNAME(resp); forced {
			safeSearchErrs = append(safeSearchErrs, fmt.Errorf("S3 gagal: upstream %s mengarahkan %s ke %s (SafeSearch dipaksa)", addr, safeSearchProbe, target))
			continue
		}
		if !hasARecord(resp) {
			upstreamErrs = append(upstreamErrs, fmt.Errorf("upstream %s menjawab tanpa rekaman A untuk %s", addr, safeSearchProbe))
			continue
		}
		res.UpstreamsOK++
	}

	if len(safeSearchErrs) > 0 {
		return errors.Join(safeSearchErrs...)
	}
	if res.UpstreamsOK == 0 {
		return fmt.Errorf("semua upstream gagal dihubungi: %w", errors.Join(upstreamErrs...))
	}
	for _, uErr := range upstreamErrs {
		res.Warnings = append(res.Warnings, uErr.Error())
	}
	res.SafeSearchOK = true
	return nil
}

func exchangeOne(ctx context.Context, addr string, bootstrap []string, timeout time.Duration, req *dns.Msg) (*dns.Msg, error) {
	baseOpts := &upstream.Options{Timeout: timeout}
	var resolvers []*upstream.UpstreamResolver
	defer func() {
		for _, r := range resolvers {
			_ = r.Close()
		}
	}()
	if len(bootstrap) > 0 {
		conseq := make(upstream.ConsequentResolver, 0, len(bootstrap))
		for _, ip := range bootstrap {
			r, err := upstream.NewUpstreamResolver(ip, baseOpts)
			if err != nil {
				if r != nil && r.Upstream != nil {
					_ = r.Close()
				}
				return nil, fmt.Errorf("bootstrap %s: %w", ip, err)
			}
			resolvers = append(resolvers, r)
			conseq = append(conseq, r)
		}
		baseOpts.Bootstrap = conseq
	}
	u, err := upstream.AddressToUpstream(addr, baseOpts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = u.Close() }()
	return u.Exchange(ctx, req)
}

func hasSafeSearchCNAME(resp *dns.Msg) (string, bool) {
	for _, rr := range resp.Answer {
		cname, ok := rr.(*dns.CNAME)
		if !ok {
			continue
		}
		target := engine.NormalizeName(cname.Target)
		if target == safeSearchTarget || strings.HasSuffix(target, "."+safeSearchTarget) {
			return target, true
		}
	}
	return "", false
}

func hasARecord(resp *dns.Msg) bool {
	for _, rr := range resp.Answer {
		if _, ok := rr.(*dns.A); ok {
			return true
		}
	}
	return false
}

func checkCachedLists(ctx context.Context, cfg config.Config, res *Result) error {
	dir := lists.Dir(cfg.DataDir)
	var allowSets, blockSets []engine.NamedSet
	var errs []error

	type loadedBlock struct {
		idx int
		ns  engine.NamedSet
	}
	var toProbe []loadedBlock

	for _, src := range cfg.Lists.Sources {
		if !src.Enabled {
			continue
		}
		path := filepath.Join(dir, src.Name+".txt")
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			res.Lists = append(res.Lists, ListResult{Name: src.Name, Kind: src.Kind, Cached: false})
			res.Warnings = append(res.Warnings, fmt.Sprintf("cache daftar %q belum ada; jalankan 'hecate lists update'", src.Name))
			continue
		}
		set, meta, err := lists.LoadCached(dir, src)
		if err != nil {
			errs = append(errs, fmt.Errorf("daftar %s: %w", src.Name, err))
			continue
		}
		idx := len(res.Lists)
		res.Lists = append(res.Lists, ListResult{
			Name:    src.Name,
			Kind:    src.Kind,
			Cached:  true,
			Entries: meta.Count,
		})
		ns := engine.NamedSet{Name: src.Name, Set: set}
		if src.Kind == config.KindAllow {
			allowSets = append(allowSets, ns)
		} else {
			blockSets = append(blockSets, ns)
			toProbe = append(toProbe, loadedBlock{idx: idx, ns: ns})
		}
	}

	eng, clientIP, err := buildProbeEngine(cfg, engine.NewListSet(allowSets, blockSets))
	if err != nil {
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	if len(errs) > 0 || len(toProbe) == 0 {
		return errors.Join(errs...)
	}

	fullListSet := engine.NewListSet(allowSets, blockSets)
	for _, p := range toProbe {
		sample, dec, ok := pickAndProbeSample(ctx, eng, fullListSet, allowSets, p.ns, clientIP)
		if !ok {
			errs = append(errs, fmt.Errorf("daftar %s: semua %d domain dikecualikan oleh aturan/daftar allow (tidak ada domain yang terblokir)",
				p.ns.Name, p.ns.Set.Len()))
			continue
		}
		res.Lists[p.idx].SampleDomain = sample
		ans, err := verifyBlockedAnswer(cfg.Block.Mode, p.ns.Name, sample, dec)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		res.Lists[p.idx].SampleAnswer = ans
	}
	return errors.Join(errs...)
}

func pickAndProbeSample(
	ctx context.Context,
	eng *engine.Engine,
	fullListSet *engine.ListSet,
	allowSets []engine.NamedSet,
	ns engine.NamedSet,
	clientIP netip.Addr,
) (string, engine.Decision, bool) {
	probeOne := func(d string) engine.Decision {
		req := new(dns.Msg)
		req.SetQuestion(dns.Fqdn(d), dns.TypeA)
		return eng.Before(ctx, engine.Query{Req: req, Client: clientIP, Proto: "udp"})
	}

	// Pass 1: find an entry in ns.Set that is blocked by ns.Name in the full ListSet.
	eng.SwapLists(fullListSet)
	var chosenDec engine.Decision
	sample, ok := ns.Set.Find(func(entry string) bool {
		dec := probeOne(entry)
		if dec.Verdict == engine.Block && dec.Reason == engine.ReasonList && dec.Source == ns.Name {
			chosenDec = dec
			return true
		}
		return false
	})
	if ok {
		return sample, chosenDec, true
	}

	// Pass 2 (fallback when every entry in ns.Set also appears in an earlier blocklist):
	// test ns as the sole blocklist alongside allowSets and custom rules.
	eng.SwapLists(engine.NewListSet(allowSets, []engine.NamedSet{ns}))
	defer eng.SwapLists(fullListSet)
	sample, ok = ns.Set.Find(func(entry string) bool {
		dec := probeOne(entry)
		if dec.Verdict == engine.Block && dec.Reason == engine.ReasonList && dec.Source == ns.Name {
			chosenDec = dec
			return true
		}
		return false
	})
	return sample, chosenDec, ok
}

func verifyBlockedAnswer(mode, listName, sample string, dec engine.Decision) (string, error) {
	if dec.Verdict != engine.Block || dec.Resp == nil {
		return "", fmt.Errorf("daftar %s: domain contoh %q tidak terblokir (verdict=%s reason=%s)",
			listName, sample, dec.Verdict, dec.Reason)
	}
	ans := dns.RcodeToString[dec.Resp.Rcode]
	if mode == config.ModeNullIP {
		if len(dec.Resp.Answer) != 1 {
			return "", fmt.Errorf("daftar %s: jawaban contoh %q memiliki %d rekaman, ingin 1 rekaman A 0.0.0.0",
				listName, sample, len(dec.Resp.Answer))
		}
		aRec, ok := dec.Resp.Answer[0].(*dns.A)
		if !ok || aRec.A.String() != "0.0.0.0" {
			return "", fmt.Errorf("daftar %s: jawaban contoh %q = %v, ingin 0.0.0.0",
				listName, sample, dec.Resp.Answer[0])
		}
		ans = "0.0.0.0"
	}
	return ans, nil
}

func buildProbeEngine(cfg config.Config, listSet *engine.ListSet) (*engine.Engine, netip.Addr, error) {
	acl, err := engine.NewACL(cfg.AllowedClients)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	responder, err := engine.NewResponder(cfg.Block.Mode, cfg.Block.TTL)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	rules := engine.NewRulesFile(cfg.Rules.File, nil)
	if _, err := rules.Reload(); err != nil {
		return nil, netip.Addr{}, fmt.Errorf("muat aturan %s: %w", cfg.Rules.File, err)
	}
	eng, err := engine.New(engine.Options{
		ACL:       acl,
		Rules:     rules,
		Responder: responder,
		Special:   cfg.SpecialNames,
	})
	if err != nil {
		return nil, netip.Addr{}, err
	}
	eng.SwapLists(listSet)

	prefix, err := netip.ParsePrefix(cfg.AllowedClients[0])
	if err != nil {
		return nil, netip.Addr{}, err
	}
	return eng, prefix.Addr().Unmap(), nil
}
