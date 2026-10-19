package native

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func TestDoT_AnswersAQueryForAConfiguredNameAndTheEngineSeesProtocolTLS(t *testing.T) {
	env := startDoT(t, nil)

	resp, _, err := env.client(dotName).Exchange(queryFor("fine.example", dns.TypeA), env.addr(t))

	if err != nil || len(resp.Answer) != 1 {
		t.Fatalf("query over DoT = %v, %v; want one answer", resp, err)
	}
	env.filter.mu.Lock()
	defer env.filter.mu.Unlock()
	if len(env.filter.queries) != 1 || env.filter.queries[0].Proto != "tls" {
		t.Errorf("engine saw %+v, want one query with protocol tls", env.filter.queries)
	}
}

func TestDoT_ServerNameIsComparedWithoutCase(t *testing.T) {
	env := startDoT(t, nil)

	resp, _, err := env.client(strings.ToUpper(dotName)).Exchange(queryFor("fine.example", dns.TypeA), env.addr(t))

	if err != nil || len(resp.Answer) != 1 {
		t.Errorf("query with an upper-case server name = %v, %v; want it served", resp, err)
	}
}

func TestDoT_RefusesEveryOtherServerNameBeforeAnyQueryIsRead(t *testing.T) {
	cases := map[string]string{
		"a name the certificate covers but nobody configured": "other." + dotBase,
		"the base name itself":                                dotBase,
		"no server name at all":                               "",
		"a name that differs by one label":                    "k7x9p2qm4t8v3w6y." + dotBase,
		"a Kelvin sign in place of the k":                     "K" + dotName[1:],
	}
	for name, serverName := range cases {
		t.Run(name, func(t *testing.T) {
			env := startDoT(t, nil)

			resp, _, err := env.client(serverName).Exchange(queryFor("fine.example", dns.TypeA), env.addr(t))

			if err == nil {
				t.Fatalf("a client with server name %q was served: %v", serverName, resp)
			}
			env.filter.mu.Lock()
			asked := len(env.filter.queries)
			env.filter.mu.Unlock()
			if asked != 0 {
				t.Errorf("the engine saw %d queries from a refused client", asked)
			}
			eventually(t, 2*time.Second, "a rate-limited warning about the refused handshake", func() bool {
				return env.logs.has(slog.LevelWarn, "dropping queries")
			})
			if env.logs.anywhere(dotName) || (serverName != "" && env.logs.anywhere(serverName)) {
				t.Error("a server name was written to the log")
			}
		})
	}
}

func TestDoT_ClientOutsideTheACLIsDisconnectedBeforeTheHandshake(t *testing.T) {
	env := startDoT(t, func(c *config.Config) { c.AllowedClients = []string{"10.0.0.0/8"} })

	conn, err := net.Dial("tcp", env.addr(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(make([]byte, 16))

	var netErr net.Error
	if err == nil || (errors.As(err, &netErr) && netErr.Timeout()) {
		t.Errorf("read = %d bytes, %v; want the server to close the connection at once", n, err)
	}
	if n != 0 {
		t.Errorf("the server sent %d bytes to a client outside allowed_clients", n)
	}
}

func TestDoT_OneConnectionCarriesMoreQueriesThanAPlainTCPConnection(t *testing.T) {
	env := startDoT(t, nil)
	client := env.client(dotName)
	conn, err := client.Dial(env.addr(t))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	for i := 0; i < maxTCPQueries+50; i++ {
		if _, _, err := client.ExchangeWithConn(queryFor("fine.example", dns.TypeA), conn); err != nil {
			t.Fatalf("query %d on one connection: %v", i+1, err)
		}
	}
}

func TestDoT_CanBeTheOnlyListener(t *testing.T) {
	env := startDoT(t, func(c *config.Config) { c.Listen = nil })

	if env.s.Addr("udp") != nil || env.s.Addr("tcp") != nil {
		t.Errorf("plain DNS addresses %v and %v are reported although none was configured", env.s.Addr("udp"), env.s.Addr("tcp"))
	}
	if _, _, err := env.client(dotName).Exchange(queryFor("fine.example", dns.TypeA), env.addr(t)); err != nil {
		t.Errorf("query over DoT: %v", err)
	}
}

func TestDoT_PlainAndDoTListenersCanRunTogether(t *testing.T) {
	env := startDoT(t, func(c *config.Config) { c.Listen = []string{"127.0.0.1:0"} })

	if env.s.Addr("udp") == nil || env.s.Addr("tcp") == nil {
		t.Fatal("the plain DNS addresses are missing")
	}
	plain := &dns.Client{Net: "tcp", Timeout: testTimeout}
	if _, _, err := plain.Exchange(queryFor("fine.example", dns.TypeA), env.s.Addr("tcp").String()); err != nil {
		t.Errorf("plain TCP query: %v", err)
	}
	if _, _, err := env.client(dotName).Exchange(queryFor("fine.example", dns.TypeA), env.addr(t)); err != nil {
		t.Errorf("DoT query: %v", err)
	}
}

func TestDoT_ReloadsTheCertificateWhenTheFilesChange(t *testing.T) {
	env := startDoT(t, nil)
	if got := env.servedSerial(t); got != 1 {
		t.Fatalf("served certificate serial = %d, want 1", got)
	}

	certPEM, keyPEM := env.pki.issue(t, 2, []string{dotBase, "*." + dotBase}, time.Now().Add(60*24*time.Hour))
	writePair(t, tempDirOf(env.certPath), certPEM, keyPEM)

	eventually(t, 3*time.Second, "the renewed certificate to be served", func() bool { return env.servedSerial(t) == 2 })
}

func tempDirOf(path string) string {
	return path[:strings.LastIndexAny(path, `/\`)]
}

func TestDoT_KeepsTheOldCertificateWhenTheNewFilesAreBroken(t *testing.T) {
	env := startDoT(t, nil)

	if err := os.WriteFile(env.certPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	eventually(t, 3*time.Second, "an error about the broken certificate", func() bool {
		return env.logs.has(slog.LevelError, "DoT certificate could not be reloaded")
	})
	if got := env.servedSerial(t); got != 1 {
		t.Errorf("served certificate serial = %d, want the old certificate (1) to stay in use", got)
	}
}

func TestDoT_WarnsWhileTheCertificateExpiresWithinTwoWeeks(t *testing.T) {
	env, err := newDoTEnv(t, time.Now().Add(10*24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	runServer(t, env.s)

	eventually(t, 3*time.Second, "an expiry warning", func() bool {
		return env.logs.has(slog.LevelWarn, "DoT certificate expires soon")
	})
}

func TestDoT_DoesNotWarnWhileTheCertificateIsFarFromExpiry(t *testing.T) {
	env := startDoT(t, nil)

	time.Sleep(200 * time.Millisecond)

	if env.logs.has(slog.LevelWarn, "expires soon") {
		t.Error("an expiry warning was logged for a certificate valid for 60 more days")
	}
}

func TestDoT_RefusesToStartWithACertificateThatCannotServeTheConfiguredNames(t *testing.T) {
	pki := newTestPKI(t)
	good, _ := pki.issue(t, 1, []string{dotBase, "*." + dotBase}, time.Now().Add(48*time.Hour))
	wrongNames, wrongKey := pki.issue(t, 2, []string{"*.other.example"}, time.Now().Add(48*time.Hour))
	expired, expiredKey := pki.issue(t, 3, []string{dotBase, "*." + dotBase}, time.Now().Add(-time.Minute))
	_, otherKey := pki.issue(t, 4, []string{dotBase}, time.Now().Add(48*time.Hour))
	cases := []struct {
		name          string
		cert, key     []byte
		missing       bool
		wantInMessage string
	}{
		{"certificate does not cover a name", wrongNames, wrongKey, false, "dot.names[0]"},
		{"certificate has expired", expired, expiredKey, false, "expired"},
		{"key does not belong to the certificate", good, otherKey, false, "key"},
		{"files are missing", nil, nil, true, "cert"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			certPath, keyPath := dir+"/cert.pem", dir+"/key.pem"
			if !tc.missing {
				certPath, keyPath = writePair(t, dir, tc.cert, tc.key)
			}
			cfg := dotConfigFor(certPath, keyPath, goodDNS(t).addr)

			_, err := newServer(cfg, &fakeFilter{}, nil, testTimeout)

			if err == nil || !strings.Contains(err.Error(), tc.wantInMessage) {
				t.Errorf("newServer = %v, want an error mentioning %q", err, tc.wantInMessage)
			}
			if err != nil && strings.Contains(err.Error(), dotName) {
				t.Errorf("the error repeats the secret server name: %v", err)
			}
		})
	}
}

func TestDoT_ReloadErrorsNeverCarryTheServerName(t *testing.T) {
	env := startDoT(t, nil)
	wrongNames, wrongKey := env.pki.issue(t, 2, []string{"*.other.example"}, time.Now().Add(48*time.Hour))

	writePair(t, tempDirOf(env.certPath), wrongNames, wrongKey)

	eventually(t, 3*time.Second, "an error about the certificate that does not cover the name", func() bool {
		return env.logs.has(slog.LevelError, "DoT certificate could not be reloaded")
	})
	if env.logs.anywhere(dotName) {
		t.Error("the secret server name was written to the log")
	}
}

func TestDoT_NegotiatesTheDotALPNWhenTheClientOffersIt(t *testing.T) {
	env := startDoT(t, nil)

	conn, err := tls.Dial("tcp", env.addr(t), &tls.Config{RootCAs: env.pki.pool, ServerName: dotName, NextProtos: []string{"dot"}})

	if err != nil {
		t.Fatalf("handshake offering ALPN dot: %v", err)
	}
	defer conn.Close()
	if got := conn.ConnectionState().NegotiatedProtocol; got != "dot" {
		t.Errorf("negotiated protocol = %q, want dot", got)
	}
}

func TestDoT_TLSOneTwoIsTheOldestVersionServed(t *testing.T) {
	env := startDoT(t, nil)

	conn, err := tls.Dial("tcp", env.addr(t), &tls.Config{RootCAs: env.pki.pool, ServerName: dotName, MaxVersion: tls.VersionTLS11})

	if err == nil {
		conn.Close()
		t.Error("a TLS 1.1 handshake succeeded")
	}
}
