package engine

import (
	"strings"

	"github.com/miekg/dns"
)

const (
	domainFirefoxCanary = "use-application-dns.net"
	domainDDR           = "_dns.resolver.arpa"

	SourceFirefoxCanary = "firefox_canary"
	SourceDDR           = "ddr"
)

func (e *Engine) checkSpecial(req *dns.Msg, name string) (Decision, bool) {
	if e.special.FirefoxCanary && matchesDomain(name, domainFirefoxCanary) {
		return Decision{
			Verdict: Block,
			Resp:    e.responder.NXDomain(req),
			Reason:  ReasonSpecial,
			Source:  SourceFirefoxCanary,
		}, true
	}
	if e.special.DDR && matchesDomain(name, domainDDR) {
		return Decision{
			Verdict: Block,
			Resp:    e.responder.NoData(req),
			Reason:  ReasonSpecial,
			Source:  SourceDDR,
		}, true
	}
	return Decision{}, false
}

func matchesDomain(name, domain string) bool {
	if name == domain {
		return true
	}
	return len(name) > len(domain) &&
		strings.HasSuffix(name, domain) &&
		name[len(name)-len(domain)-1] == '.'
}
