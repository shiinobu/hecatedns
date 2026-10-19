package lists

// agy wrote this

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shiinobu/hecatedns/internal/config"
	"github.com/shiinobu/hecatedns/internal/engine"
)

// Limits applied when parsing an untrusted blocklist stream.
const (
	// MaxLineBytes is the byte length from which a single list line is
	// rejected without allocating a larger buffer.
	MaxLineBytes = 4096

	// MaxListBytes is the maximum decoded byte size of one list (64 MiB).
	MaxListBytes = 64 << 20

	// MaxEntries is the maximum number of distinct domains accepted from one list.
	MaxEntries = 1_000_000

	readBufSize  = 32 << 10
	commentChars = "#!;"
	utf8BOM      = "\xef\xbb\xbf"
)

// Errors returned by Parse and ParseLine. Messages never echo untrusted input.
var (
	ErrUnknownFormat  = errors.New("unknown list format, want domains or hosts")
	ErrLineTooLong    = errors.New("list line is longer than 4096 bytes")
	ErrBadHostsLine   = errors.New("hosts line must be '0.0.0.0 <domain>' or '127.0.0.1 <domain>'")
	ErrListTooLarge   = errors.New("list exceeds 64 MiB limit")
	ErrTooManyEntries = errors.New("list exceeds maximum entry count")
)

// Parse reads a blocklist stream in format ("domains" or "hosts"), validates
// every entry with engine.ParseEntry, and returns an immutable DomainSet
// along with the number of rejected lines. Blank lines, comments (#, !, ;)
// and localhost boilerplate in hosts files are ignored without counting as
// rejected. Streams larger than MaxListBytes or exceeding MaxEntries fail.
func Parse(r io.Reader, format string) (set *engine.DomainSet, rejected int, err error) {
	return parseWithLimits(r, format, MaxListBytes, MaxEntries)
}

func parseWithLimits(r io.Reader, format string, maxBytes int64, maxEntries int) (*engine.DomainSet, int, error) {
	if !validFormat(format) {
		return nil, 0, ErrUnknownFormat
	}
	lr := &io.LimitedReader{R: r, N: maxBytes + 1}
	br := bufio.NewReaderSize(lr, readBufSize)

	builder := engine.NewDomainSetBuilder(0)
	var rejected int
	firstLine := true

	for {
		line, overlong, readErr := readBoundedLine(br, firstLine)
		firstLine = false
		if lr.N <= 0 {
			return nil, rejected, ErrListTooLarge
		}
		if overlong {
			rejected++
		} else if len(line) > 0 {
			domain, ok, lineErr := parseSlice(line, format)
			switch {
			case lineErr != nil:
				rejected++
			case ok:
				if !builder.Has(domain) {
					if builder.Len() >= maxEntries {
						return nil, rejected, ErrTooManyEntries
					}
					builder.AddNormalized(domain)
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, rejected, fmt.Errorf("read list: %w", readErr)
		}
	}
	return builder.Build(), rejected, nil
}

// readBoundedLine reads one line from br (whose buffer is larger than
// MaxLineBytes). If the line is at least MaxLineBytes long after stripping a
// leading BOM (on line 1) and trailing CRLF/LF, it drains until the next
// newline and returns overlong=true.
func readBoundedLine(br *bufio.Reader, stripBOM bool) (line []byte, overlong bool, err error) {
	slice, err := br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = br.ReadSlice('\n')
		}
		return nil, true, err
	}
	slice = trimLineEnd(slice)
	if stripBOM {
		slice = bytes.TrimPrefix(slice, []byte(utf8BOM))
	}
	if len(slice) >= MaxLineBytes {
		return nil, true, err
	}
	return slice, false, err
}

func trimLineEnd(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}

// ParseLine parses a single line in format ("domains" or "hosts"). It returns
// ok=false and err=nil for blank lines, comments (#, !, ;) and ignored
// localhost entries in hosts format.
func ParseLine(line, format string) (domain string, ok bool, err error) {
	if !validFormat(format) {
		return "", false, ErrUnknownFormat
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	line = strings.TrimPrefix(line, utf8BOM)
	if len(line) >= MaxLineBytes {
		return "", false, ErrLineTooLong
	}
	return parseSlice([]byte(line), format)
}

func parseSlice(line []byte, format string) (string, bool, error) {
	if i := bytes.IndexAny(line, commentChars); i >= 0 {
		line = line[:i]
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", false, nil
	}
	if format == config.FormatHosts {
		return parseHostsLine(string(line))
	}
	domain, err := engine.ParseEntry(string(line))
	if err != nil {
		return "", false, err
	}
	return domain, true, nil
}

func parseHostsLine(line string) (string, bool, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", false, ErrBadHostsLine
	}
	if isLocalhostPreamble(fields) {
		return "", false, nil
	}
	if len(fields) != 2 || !isSinkholeIP(fields[0]) {
		return "", false, ErrBadHostsLine
	}
	domain, err := engine.ParseEntry(fields[1])
	if err != nil {
		return "", false, err
	}
	return domain, true, nil
}

func validFormat(format string) bool {
	return format == config.FormatDomains || format == config.FormatHosts
}

func isSinkholeIP(ip string) bool {
	return ip == "0.0.0.0" || ip == "127.0.0.1"
}

func isLocalhostPreamble(fields []string) bool {
	if !isPreambleIP(fields[0]) {
		return false
	}
	for _, host := range fields[1:] {
		if !isLocalhostName(engine.NormalizeName(host)) {
			return false
		}
	}
	return true
}

func isPreambleIP(ip string) bool {
	return ip == "0.0.0.0" ||
		ip == "127.0.0.1" ||
		ip == "::1" ||
		ip == "::" ||
		ip == "255.255.255.255" ||
		strings.HasPrefix(ip, "fe80:") ||
		strings.HasPrefix(ip, "ff00:") ||
		strings.HasPrefix(ip, "ff02:")
}

func isLocalhostName(name string) bool {
	switch name {
	case "localhost", "localhost.localdomain", "local", "broadcasthost",
		"ip6-localhost", "ip6-loopback", "ip6-localnet",
		"ip6-mcastprefix", "ip6-allnodes", "ip6-allrouters", "ip6-allhosts",
		"0.0.0.0":
		return true
	}
	return strings.HasSuffix(name, ".localhost")
}
