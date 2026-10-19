package lists

import (
	"errors"
	"strings"
	"testing"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func TestParseLine_Domains(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    string
		wantOK  bool
		wantErr error
	}{
		{"plain domain", "ads.example.com", "ads.example.com", true, nil},
		{"uppercase and trailing dot", "  Sub.Ads.Example.COM.  ", "sub.ads.example.com", true, nil},
		{"wildcard prefix stripped", "*.Popup.Example.net", "popup.example.net", true, nil},
		{"utf8 bom stripped", "\xef\xbb\xbftracker.example.org", "tracker.example.org", true, nil},
		{"underscore in label", "_ad.example.com", "_ad.example.com", true, nil},
		{"punycode idn", "xn--bcher-kva.example", "xn--bcher-kva.example", true, nil},
		{"trailing hash comment", "ads.example.com # comment here", "ads.example.com", true, nil},
		{"trailing bang comment", "ads.example.com ! adblock comment", "ads.example.com", true, nil},
		{"trailing semicolon comment", "ads.example.com ; zone comment", "ads.example.com", true, nil},
		{"blank line", "   \t  ", "", false, nil},
		{"hash comment line", "# full line comment", "", false, nil},
		{"bang comment line", "! Title: HaGeZi", "", false, nil},
		{"semicolon comment line", "; comment", "", false, nil},
		{"single label rejected", "com", "", false, engine.ErrSingleLabel},
		{"ipv4 rejected", "192.0.2.1", "", false, engine.ErrIPAddress},
		{"ipv6 rejected", "2001:db8::1", "", false, engine.ErrIPAddress},
		{"hosts line in domains format rejected", "0.0.0.0 ads.example.com", "", false, engine.ErrBadLabel},
		{"adblock syntax rejected", "||ads.example.com^", "", false, engine.ErrBadLabel},
		{"double wildcard rejected", "*.*.example.com", "", false, engine.ErrBadLabel},
		{"non-ascii rejected", "iklan-\u212a.example.com", "", false, engine.ErrBadLabel},
		{"overlong line rejected", strings.Repeat("a", MaxLineBytes), "", false, ErrLineTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := ParseLine(tt.line, config.FormatDomains)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ParseLine(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.line, got, ok, err, tt.want, tt.wantOK, tt.wantErr)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseLine(%q) err = %v, want %v", tt.line, err, tt.wantErr)
			}
			if tt.wantErr == nil && err != nil {
				t.Fatalf("ParseLine(%q) unexpected err = %v", tt.line, err)
			}
		})
	}
}

func TestParseLine_Hosts(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    string
		wantOK  bool
		wantErr error
	}{
		{"zero ip", "0.0.0.0 ads.example.com", "ads.example.com", true, nil},
		{"loopback ip", "127.0.0.1 tracker.example.org", "tracker.example.org", true, nil},
		{"tabs and uppercase", "0.0.0.0\t\tBanners.Example.NET.\t# inline note", "banners.example.net", true, nil},
		{"wildcard in hosts target", "0.0.0.0 *.ads.example.com", "ads.example.com", true, nil},
		{"ignore 127.0.0.1 localhost", "127.0.0.1 localhost", "", false, nil},
		{"ignore 127.0.0.1 localhost.localdomain", "127.0.0.1 localhost.localdomain", "", false, nil},
		{"ignore multi-alias localhost", "127.0.0.1 localhost localhost.localdomain local", "", false, nil},
		{"ignore 0.0.0.0 0.0.0.0", "0.0.0.0 0.0.0.0", "", false, nil},
		{"ignore ipv6 localhost", "::1 localhost ip6-localhost ip6-loopback", "", false, nil},
		{"ignore broadcasthost", "255.255.255.255 broadcasthost", "", false, nil},
		{"ignore subdomain of localhost", "0.0.0.0 metric.localhost", "", false, nil},
		{"reject bare domain without ip", "ads.example.com", "", false, ErrBadHostsLine},
		{"reject non-sinkhole ip", "192.168.1.1 router.example.com", "", false, ErrBadHostsLine},
		{"reject extra fields", "0.0.0.0 ads.example.com other.example.com", "", false, ErrBadHostsLine},
		{"reject single-label target", "0.0.0.0 org", "", false, engine.ErrSingleLabel},
		{"reject ip target", "0.0.0.0 1.2.3.4", "", false, engine.ErrIPAddress},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := ParseLine(tt.line, config.FormatHosts)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ParseLine(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.line, got, ok, err, tt.want, tt.wantOK, tt.wantErr)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseLine(%q) err = %v, want %v", tt.line, err, tt.wantErr)
			}
			if tt.wantErr == nil && err != nil {
				t.Fatalf("ParseLine(%q) unexpected err = %v", tt.line, err)
			}
		})
	}
}

func TestParse_DomainsAndCounts(t *testing.T) {
	input := strings.Join([]string{
		"\xef\xbb\xbf# Title: sample list",
		"! Expires: 1 day",
		"; another comment",
		"",
		"ads.example.com",
		"ADS.EXAMPLE.COM.",
		"*.popups.example.net # trailing comment",
		"com",
		"10.0.0.1",
		"||bad.example.org^",
		"tracker.example.id",
	}, "\r\n")

	set, rejected, err := Parse(strings.NewReader(input), config.FormatDomains)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if rejected != 3 {
		t.Errorf("rejected = %d, want 3", rejected)
	}
	if set.Len() != 3 {
		t.Errorf("set.Len() = %d, want 3", set.Len())
	}
	for _, want := range []string{"ads.example.com", "sub.popups.example.net", "tracker.example.id"} {
		if !set.Match(want) {
			t.Errorf("set.Match(%q) = false, want true", want)
		}
	}
	if set.Match("localhost") || set.Match("bad.example.org") {
		t.Errorf("rejected entries must not match")
	}
}

func TestParse_HostsPreambleAndRejections(t *testing.T) {
	input := strings.Join([]string{
		"# StevenBlack style header",
		"127.0.0.1 localhost",
		"127.0.0.1 localhost.localdomain",
		"127.0.0.1 local",
		"255.255.255.255 broadcasthost",
		"::1 localhost",
		"::1 ip6-localhost",
		"::1 ip6-loopback",
		"fe80::1%lo0 localhost",
		"ff00::0 ip6-localnet",
		"ff00::0 ip6-mcastprefix",
		"ff02::1 ip6-allnodes",
		"ff02::2 ip6-allrouters",
		"ff02::3 ip6-allhosts",
		"0.0.0.0 0.0.0.0",
		"0.0.0.0 ad1.example.com",
		"127.0.0.1 ad2.example.org ; comment",
		"0.0.0.0",
		"8.8.8.8 dns.google",
		"0.0.0.0 a.example b.example",
	}, "\n")

	set, rejected, err := Parse(strings.NewReader(input), config.FormatHosts)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if rejected != 3 {
		t.Errorf("rejected = %d, want 3", rejected)
	}
	if set.Len() != 2 {
		t.Errorf("set.Len() = %d, want 2", set.Len())
	}
	if set.Match("localhost.localdomain") {
		t.Errorf("localhost.localdomain must be ignored, not blocked")
	}
	if !set.Match("ad1.example.com") || !set.Match("sub.ad2.example.org") {
		t.Errorf("expected ad1.example.com and sub.ad2.example.org to match")
	}
}

func TestParse_OverlongLineSkippedAndCounted(t *testing.T) {
	over4k := "#" + strings.Repeat("x", MaxLineBytes+500)
	over64k := strings.Repeat("y", (64<<10)+500)
	input := "first.example.com\n" + over4k + "\n" + over64k + "\nsecond.example.com"

	set, rejected, err := Parse(strings.NewReader(input), config.FormatDomains)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if rejected != 2 {
		t.Errorf("rejected = %d, want 2", rejected)
	}
	if set.Len() != 2 || !set.Match("first.example.com") || !set.Match("second.example.com") {
		t.Errorf("expected both valid lines around the overlong lines to load, got len=%d", set.Len())
	}
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestParse_ReaderError(t *testing.T) {
	boom := errors.New("disk read failed")
	if _, _, err := Parse(failingReader{err: boom}, config.FormatDomains); !errors.Is(err, boom) {
		t.Fatalf("Parse reader err = %v, want %v", err, boom)
	}
}

func TestParse_BoundsAndUnknownFormat(t *testing.T) {
	if _, _, err := Parse(strings.NewReader("example.com\n"), "adblock"); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("Parse unknown format err = %v, want %v", err, ErrUnknownFormat)
	}
	if _, _, err := ParseLine("example.com", "adblock"); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("ParseLine unknown format err = %v, want %v", err, ErrUnknownFormat)
	}

	many := "a.example.com\nb.example.com\nc.example.com\n"
	if _, _, err := parseWithLimits(strings.NewReader(many), config.FormatDomains, MaxListBytes, 2); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("parseWithLimits entry cap err = %v, want %v", err, ErrTooManyEntries)
	}

	dups := "a.example.com\na.example.com\nb.example.com\n"
	set, _, err := parseWithLimits(strings.NewReader(dups), config.FormatDomains, MaxListBytes, 2)
	if err != nil || set.Len() != 2 {
		t.Fatalf("parseWithLimits duplicates = (len %d, %v), want (2, nil)", set.Len(), err)
	}

	if _, _, err := parseWithLimits(strings.NewReader(many), config.FormatDomains, 10, MaxEntries); !errors.Is(err, ErrListTooLarge) {
		t.Fatalf("parseWithLimits byte cap err = %v, want %v", err, ErrListTooLarge)
	}
}

func FuzzParseLine(f *testing.F) {
	seeds := []string{
		"",
		"# comment",
		"! comment",
		"; comment",
		"ads.example.com",
		"*.ads.example.com",
		"0.0.0.0 ads.example.com",
		"127.0.0.1 localhost.localdomain",
		"::1 localhost",
		"com",
		"127.0.0.1",
		"\xef\xbb\xbf0.0.0.0 Sub.Example.COM. # note\r\n",
		strings.Repeat("a", MaxLineBytes+10),
		"\xff\xfe\xfd\x00",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		for _, format := range []string{config.FormatDomains, config.FormatHosts} {
			domain, ok, err := ParseLine(line, format)
			if ok && err != nil {
				t.Fatalf("format %s: ok=true with err=%v", format, err)
			}
			if ok && domain == "" {
				t.Fatalf("format %s: ok=true with empty domain", format)
			}
		}
	})
}
