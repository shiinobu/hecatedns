// agy wrote this
package engine

import (
	"strings"

	"github.com/miekg/dns"
)

// Special-name domain suffixes and their Decision.Source values.
const (
	domainFirefoxCanary = "use-application-dns.net"
	domainDDR           = "_dns.resolver.arpa"

	SourceFirefoxCanary = "firefox_canary"
	SourceDDR           = "ddr"
)

// checkSpecial answers queries for built-in client-steering domains when their
// respective switch in SpecialNames is enabled:
//   - use-application-dns.net -> NXDOMAIN (disables Firefox DoH auto-upgrade)
//   - _dns.resolver.arpa      -> NODATA   (disables Discovery of Designated Resolvers)
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

// matchesDomain reports whether name is equal to domain or is a subdomain of it
// on a label boundary. Both name and domain must already be normalized.
func matchesDomain(name, domain string) bool {
	if name == domain {
		return true
	}
	return len(name) > len(domain) &&
		strings.HasSuffix(name, domain) &&
		name[len(name)-len(domain)-1] == '.'
}
