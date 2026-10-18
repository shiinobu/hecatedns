// Command compare sends the same sample of domain names to two running xdns
// servers (one per backend, same configuration) and reports every difference in
// the engine's decision (read from each server's query log) and in the answer
// the client got. It is a Phase 4 tool: block and pass decisions must be the same
// on both backends, and any difference needs an explanation.
//
//	go run ./scripts/compare -a 127.0.0.1:5301 -b 127.0.0.1:5302 \
//	    -log-a data/logs-a -log-b data/logs-b -queries data/logs -lists data/lists
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// logRecord holds the fields of a query-log line the comparison needs.
type logRecord struct {
	QName  string `json:"qname"`
	QType  string `json:"qtype"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	Source string `json:"source"`
	RCode  string `json:"rcode"`
}

// key identifies one query.
type key struct{ name, qtype string }

// outcome is what one backend did with one query: the engine's decision from the
// log, and the shape of the answer the client received.
type outcome struct {
	rec    logRecord
	wire   string // rcode plus a class of the answer section
	err    error
	hasRec bool
}

func main() {
	var (
		addrA   = flag.String("a", "127.0.0.1:5301", "address of server A")
		addrB   = flag.String("b", "127.0.0.1:5302", "address of server B")
		logA    = flag.String("log-a", "", "query log directory of server A")
		logB    = flag.String("log-b", "", "query log directory of server B")
		queries = flag.String("queries", "data/logs", "directory with real query logs to sample names from")
		lists   = flag.String("lists", "data/lists", "directory with the cached block lists to sample names from")
		total   = flag.Int("n", 1000, "number of domain names to sample")
		qps     = flag.Int("qps", 40, "queries per second sent to each server (stay below the rate limit)")
		seed    = flag.Uint64("seed", 1, "random seed of the sample")
		nameA   = flag.String("name-a", "A", "label of server A in the report")
		nameB   = flag.String("name-b", "B", "label of server B in the report")
	)
	flag.Parse()
	if *logA == "" || *logB == "" {
		fatal("both -log-a and -log-b are required")
	}

	names, err := sample(*queries, *lists, *total, *seed)
	if err != nil {
		fatal("sampling: %v", err)
	}
	fmt.Fprintf(os.Stderr, "sampled %d names, asking A and HTTPS for each (%d queries per server)\n", len(names), 2*len(names))

	start := time.Now().Add(-time.Second)
	wireA, wireB := ask(*addrA, names, *qps), ask(*addrB, names, *qps)
	time.Sleep(2 * time.Second) // let both servers flush their logs
	recA, err := readLog(*logA, start)
	if err != nil {
		fatal("reading log A: %v", err)
	}
	recB, err := readLog(*logB, start)
	if err != nil {
		fatal("reading log B: %v", err)
	}
	report(os.Stdout, *nameA, *nameB, merge(wireA, recA), merge(wireB, recB))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "compare: "+format+"\n", args...)
	os.Exit(1)
}

// sample picks total distinct names: about 60% from real query logs, the rest
// from the block lists (some as a subdomain, to exercise suffix matching).
func sample(queryDir, listDir string, total int, seed uint64) ([]string, error) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	logged, err := loggedNames(queryDir)
	if err != nil {
		return nil, err
	}
	listed, err := listedNames(listDir)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var out []string
	add := func(name string) {
		if name = strings.ToLower(strings.TrimSuffix(name, ".")); name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	shuffle(rng, logged)
	for _, n := range logged {
		if len(out) >= total*6/10 {
			break
		}
		add(n)
	}
	shuffle(rng, listed)
	for i, n := range listed {
		if len(out) >= total {
			break
		}
		if i%4 == 0 {
			n = "www." + n
		}
		add(n)
	}
	return out, nil
}

func shuffle(rng *rand.Rand, s []string) {
	rng.Shuffle(len(s), func(i, j int) { s[i], s[j] = s[j], s[i] })
}

func loggedNames(dir string) ([]string, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "queries-*.jsonl"))
	seen := make(map[string]bool)
	var names []string
	for _, f := range files {
		if err := eachRecord(f, func(r logRecord) {
			if !seen[r.QName] && r.QName != "" {
				seen[r.QName] = true
				names = append(names, r.QName)
			}
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(names)
	return names, nil
}

func listedNames(dir string) ([]string, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	var names []string
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
				names = append(names, line)
			}
		}
		fh.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	sort.Strings(names)
	return names, nil
}

func eachRecord(path string, fn func(logRecord)) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	dec := json.NewDecoder(fh)
	for {
		var r logRecord
		if err := dec.Decode(&r); err != nil {
			if err == io.EOF {
				return nil
			}
			return nil // a half-written last line is not an error
		}
		fn(r)
	}
}

// ask sends A and HTTPS for every name to addr at about qps queries per second
// and returns the shape of each answer.
func ask(addr string, names []string, qps int) map[key]string {
	out := make(map[key]string)
	var mu sync.Mutex
	tick := time.NewTicker(time.Second / time.Duration(qps))
	defer tick.Stop()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, name := range names {
		for _, qtype := range []uint16{dns.TypeA, dns.TypeHTTPS} {
			<-tick.C
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				shape := exchange(addr, name, qtype)
				mu.Lock()
				out[key{name, dns.TypeToString[qtype]}] = shape
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	return out
}

// exchange asks one question and describes the answer by what a client sees.
func exchange(addr, name string, qtype uint16) string {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(name), qtype)
	client := &dns.Client{Net: "udp", Timeout: 8 * time.Second}
	resp, _, err := client.Exchange(req, addr)
	if err != nil {
		return "error: " + shortErr(err)
	}
	if resp.Truncated {
		tcp := &dns.Client{Net: "tcp", Timeout: 8 * time.Second}
		if full, _, err := tcp.Exchange(req, addr); err == nil {
			resp = full
		}
	}
	return dns.RcodeToString[resp.Rcode] + "/" + answerClass(resp)
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}

// answerClass says what kind of answer section a reply has, without the values
// that legitimately differ between two lookups of the same name.
func answerClass(m *dns.Msg) string {
	if len(m.Answer) == 0 {
		return "empty"
	}
	allNull := true
	for _, rr := range m.Answer {
		switch v := rr.(type) {
		case *dns.A:
			allNull = allNull && v.A.IsUnspecified()
		case *dns.AAAA:
			allNull = allNull && v.AAAA.IsUnspecified()
		default:
			allNull = false
		}
	}
	if allNull {
		return "null-ip"
	}
	return "records"
}

// readLog returns the decisions the server logged since start, one per query.
func readLog(dir string, since time.Time) (map[key]logRecord, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "queries-*.jsonl"))
	out := make(map[key]logRecord)
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(fh)
		for {
			var raw struct {
				TS string `json:"ts"`
				logRecord
			}
			if err := dec.Decode(&raw); err != nil {
				break
			}
			if ts, err := time.Parse(time.RFC3339Nano, raw.TS); err == nil && ts.Before(since) {
				continue
			}
			out[key{strings.TrimSuffix(strings.ToLower(raw.QName), "."), raw.QType}] = raw.logRecord
		}
		fh.Close()
	}
	return out, nil
}

func merge(wire map[key]string, recs map[key]logRecord) map[key]outcome {
	out := make(map[key]outcome, len(wire))
	for k, w := range wire {
		rec, ok := recs[k]
		out[k] = outcome{rec: rec, hasRec: ok, wire: w}
	}
	return out
}

// report prints the summary and every difference.
func report(w io.Writer, nameA, nameB string, a, b map[key]outcome) {
	keys := make([]key, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].qtype < keys[j].qtype
	})

	var blockedA, blockedB, decisionDiff, wireDiff, missing int
	var lines []string
	for _, k := range keys {
		oa, ob := a[k], b[k]
		if !oa.hasRec || !ob.hasRec {
			missing++
			lines = append(lines, fmt.Sprintf("| %s | %s | no log record (%s: %v, %s: %v) | |", k.name, k.qtype, nameA, oa.hasRec, nameB, ob.hasRec))
			continue
		}
		if oa.rec.Action == "block" {
			blockedA++
		}
		if ob.rec.Action == "block" {
			blockedB++
		}
		da, db := decision(oa.rec), decision(ob.rec)
		switch {
		case da != db:
			decisionDiff++
			lines = append(lines, fmt.Sprintf("| %s | %s | decision: %s=%s, %s=%s | wire: %s / %s |", k.name, k.qtype, nameA, da, nameB, db, oa.wire, ob.wire))
		case oa.wire != ob.wire:
			wireDiff++
			lines = append(lines, fmt.Sprintf("| %s | %s | same decision (%s) | wire: %s=%s, %s=%s |", k.name, k.qtype, da, nameA, oa.wire, nameB, ob.wire))
		}
	}
	fmt.Fprintf(w, "Queries compared: %d (%s and %s)\n", len(keys), nameA, nameB)
	fmt.Fprintf(w, "Blocked: %s=%d, %s=%d\n", nameA, blockedA, nameB, blockedB)
	fmt.Fprintf(w, "Different engine decision: %d\n", decisionDiff)
	fmt.Fprintf(w, "Same decision, different answer shape: %d\n", wireDiff)
	fmt.Fprintf(w, "Missing log record: %d\n", missing)
	if len(lines) > 0 {
		fmt.Fprintf(w, "\n| name | type | difference | detail |\n|---|---|---|---|\n%s\n", strings.Join(lines, "\n"))
	}
}

// decision is what the engine decided, as the log records it.
func decision(r logRecord) string {
	if r.Action == "block" {
		return fmt.Sprintf("block/%s/%s", r.Reason, r.Source)
	}
	return r.Action
}
