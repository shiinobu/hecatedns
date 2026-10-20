package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

func TestProbe_AWrongCertificateDoesNotEchoItsNames(t *testing.T) {
	env := startProbeDoT(t, []string{"certmarker-one.example.net", "certmarker-two.example.net"}, time.Now().Add(90*24*time.Hour))

	stages := probe(context.Background(), probeOptionsFor(env))

	s := stageOf(t, stages, "tls")
	if s.OK || strings.Contains(s.Detail, "certmarker") || !strings.Contains(s.Detail, "does not cover <name>") {
		t.Errorf("tls stage = %+v, want a fixed text without the names of the certificate", s)
	}
}

func TestProbe_TheSystemLookupIsBoundedByTheTimeout(t *testing.T) {
	opts := probeOptions{Name: probeName, Addr: closedTCPAddr(t), Canary: probeCanary, Timeout: 200 * time.Millisecond}
	opts.Lookup = func(ctx context.Context, _ string) ([]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	started := time.Now()
	stages := probe(context.Background(), opts)

	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("the probe took %v with a 200 ms timeout", took)
	}
	if stageOf(t, stages, "resolve system").OK {
		t.Error("the system stage passed")
	}
}

func TestProbe_CancellingDuringTheQueryReturnsAtOnce(t *testing.T) {
	slow := func(w dns.ResponseWriter, r *dns.Msg) {
		time.Sleep(3 * time.Second)
		probeAnswer("93.184.216.34")(w, r)
	}
	env := startProbeDoTSpec(t, doTSpec{names: []string{probeName}, notAfter: time.Now().Add(90 * 24 * time.Hour), handler: slow})
	opts := probeOptionsFor(env)
	opts.Timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	stages := probe(ctx, opts)

	if took := time.Since(started); took > 1500*time.Millisecond {
		t.Errorf("the probe took %v after the context ended", took)
	}
	if stageOf(t, stages, "query").OK {
		t.Error("the query stage passed")
	}
}

func TestProbe_TriesEachAddressInTurn(t *testing.T) {
	env := startProbeDoT(t, []string{probeName}, time.Now().Add(90*24*time.Hour))
	opts := probeOptionsFor(env)
	opts.Addr = ""
	opts.Port = env.port
	opts.Lookup = lookupReturning("127.0.0.2", "127.0.0.1")

	stages := probe(context.Background(), opts)

	if s := stageOf(t, stages, "tls"); !s.OK || !strings.Contains(s.Detail, "127.0.0.1:"+env.port) {
		t.Errorf("tls stage = %+v, want success on the second address", s)
	}
}

func TestProbe_WhenEveryAddressFailsTheStageSaysHowManyWereTried(t *testing.T) {
	_, port, _ := strings.Cut(closedTCPAddr(t), ":")
	opts := probeOptions{Name: probeName, Port: port, Canary: probeCanary, Timeout: 300 * time.Millisecond, Lookup: lookupReturning("127.0.0.2", "127.0.0.3")}

	stages := probe(context.Background(), opts)

	if s := stageOf(t, stages, "tls"); s.OK || !strings.Contains(s.Detail, "2 addresses tried") {
		t.Errorf("tls stage = %+v", s)
	}
}

func TestProbe_AServerThatFailsTheQueryFailsThePathButNotTheHandshake(t *testing.T) {
	servfail := func(w dns.ResponseWriter, r *dns.Msg) {
		_ = w.WriteMsg(new(dns.Msg).SetRcode(r, dns.RcodeServerFailure))
	}
	env := startProbeDoTSpec(t, doTSpec{names: []string{probeName}, notAfter: time.Now().Add(90 * 24 * time.Hour), handler: servfail})

	stages := probe(context.Background(), probeOptionsFor(env))

	if !stageOf(t, stages, "tls").OK || stageOf(t, stages, "query").OK || probePassed(stages) {
		t.Errorf("stages = %+v", stages)
	}
	if !strings.Contains(probeVerdict(stages), "did not answer") {
		t.Errorf("verdict = %q", probeVerdict(stages))
	}
}

func TestProbe_AnAnswerWithTheWrongIDFailsTheQueryStage(t *testing.T) {
	wrongID := func(w dns.ResponseWriter, r *dns.Msg) {
		resp := new(dns.Msg).SetReply(r)
		resp.Id = r.Id + 1
		_ = w.WriteMsg(resp)
	}
	env := startProbeDoTSpec(t, doTSpec{names: []string{probeName}, notAfter: time.Now().Add(90 * 24 * time.Hour), handler: wrongID})

	stages := probe(context.Background(), probeOptionsFor(env))

	if s := stageOf(t, stages, "query"); s.OK || !strings.Contains(s.Detail, "id") {
		t.Errorf("query stage = %+v", s)
	}
}

func TestProbe_AServerThatOnlySpeaksTLS12FailsTheTLSStage(t *testing.T) {
	env := startProbeDoTSpec(t, doTSpec{names: []string{probeName}, notAfter: time.Now().Add(90 * 24 * time.Hour), maxVersion: tls.VersionTLS12})

	stages := probe(context.Background(), probeOptionsFor(env))

	if s := stageOf(t, stages, "tls"); s.OK {
		t.Errorf("tls stage = %+v, want a failure for a server without TLS 1.3", s)
	}
}

func TestProbeVerdict_ADeadServerIsNotBlamedOnTheResolver(t *testing.T) {
	pass := func(name string) probeStage { return probeStage{Stage: name, OK: true} }
	fail := func(name string) probeStage { return probeStage{Stage: name, Detail: "x"} }

	dead := probeVerdict([]probeStage{fail("resolve system"), pass("resolve 1.1.1.1:53"), fail("tls"), fail("query")})
	noPublic := probeVerdict([]probeStage{fail("resolve system"), fail("tls"), fail("query")})

	if !strings.Contains(dead, "handshake") || strings.Contains(dead, "own resolver") {
		t.Errorf("verdict = %q, want the handshake text", dead)
	}
	if !strings.Contains(noPublic, "no public resolver was asked") {
		t.Errorf("verdict = %q", noPublic)
	}
}

func TestReadProbeName_AcceptsABOMAndRefusesAnOversizedFile(t *testing.T) {
	dir := t.TempDir()
	bom := filepath.Join(dir, "bom.txt")
	content := append([]byte{0xef, 0xbb, 0xbf}, []byte(probeName+"\r\n")...)
	if err := os.WriteFile(bom, content, 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("# filler line\n", 400)+probeName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readProbeName(bom)
	_, bigErr := readProbeName(big)

	if err != nil || got != probeName {
		t.Errorf("a file with a BOM: name %q, err %v", got, err)
	}
	if bigErr == nil || !strings.Contains(bigErr.Error(), "larger") {
		t.Errorf("an oversized file: err = %v", bigErr)
	}
}

func TestReadProbeName_TheErrorTellsWhatToDoWithAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "utf16.txt")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 'a', 0}, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readProbeName(path)

	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("err = %v, want a hint about the file encoding", err)
	}
}

func TestNormalizeResolvers_DefaultsThePort(t *testing.T) {
	got := normalizeResolvers("1.1.1.1, 8.8.8.8:5353,2606:4700:4700::1111,[2001:4860:4860::8888]:53,,")

	want := []string{"1.1.1.1:53", "8.8.8.8:5353", "[2606:4700:4700::1111]:53", "[2001:4860:4860::8888]:53"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("resolvers = %v, want %v", got, want)
	}
}

func TestRunProbe_RejectsAWatchIntervalThatIsTooSmallOrNegative(t *testing.T) {
	nameFile := filepath.Join(t.TempDir(), "name.txt")
	if err := os.WriteFile(nameFile, []byte(probeName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, interval := range []string{"1ms", "4s", "-5s"} {
		var stdout, stderr bytes.Buffer

		code := Execute([]string{"probe", "-name-file", nameFile, "-watch", interval}, &stdout, &stderr)

		if code != exitUsage || !strings.Contains(stderr.String(), "at least") {
			t.Errorf("-watch %s: exit %d, stderr %q", interval, code, stderr.String())
		}
	}
}

func probeConfigWithNamesFile(t *testing.T, namesFile string) string {
	t.Helper()
	dir := t.TempDir()
	certPath, keyPath := writeSelfSignedPair(t, dir, []string{checkDoTName}, time.Now().Add(90*24*time.Hour))
	configPath := dotCheckConfig(t, dir, startTestDNSUpstream(t, false), certPath, keyPath)
	text := strings.Replace(readText(t, configPath), "dot:\n  enabled: true\n", fmt.Sprintf("dot:\n  enabled: true\n  names_file: %q\n", filepath.ToSlash(namesFile)), 1)
	if err := os.WriteFile(configPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func TestRunProbe_ProbesTheNewestNameOfTheConfigAndItsNamesFile(t *testing.T) {
	namesFile := filepath.Join(t.TempDir(), "dot-names.txt")
	if err := dotnames.Write(namesFile, []dotnames.Entry{{Host: fileNameOne}, {Host: fileNameTwo}}); err != nil {
		t.Fatal(err)
	}
	var asked string
	withSystemLookup(t, func(_ context.Context, name string) ([]string, error) {
		asked = name
		return nil, errors.New("no such host")
	})
	var stdout, stderr bytes.Buffer

	Execute([]string{"probe", "-config", probeConfigWithNamesFile(t, namesFile), "-resolvers", "", "-timeout", "200ms", "-addr", closedTCPAddr(t)}, &stdout, &stderr)

	if asked != fileNameTwo {
		t.Errorf("the probe asked for %q, want the newest name of the names file", asked)
	}
}

func TestRunProbe_AnUnreadableNamesFileFailsInsteadOfProbingAnOlderName(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"probe", "-config", probeConfigWithNamesFile(t, t.TempDir())}, &stdout, &stderr)

	if code != exitError || !strings.Contains(stderr.String(), "names file") || stdout.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestRunProbe_AMissingNamesFileIsNotAnError(t *testing.T) {
	withSystemLookup(t, func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") })
	var stdout, stderr bytes.Buffer

	Execute([]string{"probe", "-config", probeConfigWithNamesFile(t, filepath.Join(t.TempDir(), "missing.txt")), "-resolvers", "", "-timeout", "200ms", "-addr", closedTCPAddr(t)}, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "resolve system") {
		t.Errorf("the probe did not run: %s%s", stdout.String(), stderr.String())
	}
}
