package native

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	namesTestPoll    = 10 * time.Millisecond
	namesTestWait    = 3 * time.Second
	fileDoTName      = "a1b2c3d4e5f6g7h8." + dotBase
	otherFileDoTName = "z9y8x7w6v5u4t3s2." + dotBase
	secretLabel      = "SECRETLABELQ7W2"
)

func namesLines(names ...string) []byte {
	if len(names) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(names, "\n") + "\n")
}

func namesConfig(names []string, path string) func(*config.Config) {
	return func(c *config.Config) {
		c.DoT.Names = names
		c.DoT.NamesFile = path
	}
}

func startNamesEnv(t *testing.T, names []string, seed []byte, tune func(*dotLimits)) (*dotEnv, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dot-names.txt")
	if seed != nil {
		if err := os.WriteFile(path, seed, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env, err := newDoTEnv(t, time.Now().Add(60*24*time.Hour), namesConfig(names, path))
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	env.s.names.every = namesTestPoll
	if tune != nil {
		tune(&env.s.dotLimits)
	}
	runServer(t, env.s)
	return env, path
}

func handshakeWith(addr string, pool *x509.CertPool, serverName string) error {
	dialer := &net.Dialer{Timeout: testTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{RootCAs: pool, ServerName: serverName})
	if err != nil {
		return err
	}
	return conn.Close()
}

func (e *dotEnv) accepts(t *testing.T, serverName string) bool {
	t.Helper()
	return handshakeWith(e.addr(t), e.pki.pool, serverName) == nil
}

func replaceNamesFile(t *testing.T, path string, data []byte) {
	t.Helper()
	tmp := path + ".next"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	for range 50 {
		if err = os.Rename(tmp, path); err == nil {
			return
		}
		time.Sleep(namesTestPoll)
	}
	t.Fatalf("replacing the names file: %v", err)
}

func (l *logSink) matching(level slog.Level, part string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.recs {
		if r.level == level && strings.Contains(r.text, part) {
			out = append(out, r.text)
		}
	}
	return out
}

func (l *logSink) count(level slog.Level, part string) int {
	return len(l.matching(level, part))
}

func assertNoSecretsLogged(t *testing.T, logs *logSink) {
	t.Helper()
	for _, secret := range []string{dotName, fileDoTName, otherFileDoTName, secretLabel, strings.ToLower(secretLabel)} {
		if logs.anywhere(secret) {
			t.Errorf("the log carries the secret %q", secret)
		}
	}
}

func TestDoTNames_AcceptsAFileNameOnceTheFileAppears(t *testing.T) {
	env, path := startNamesEnv(t, []string{dotName}, nil, nil)
	if env.accepts(t, fileDoTName) {
		t.Fatal("a name was served before any names file existed")
	}

	if err := dotnames.Write(path, []dotnames.Entry{{Host: fileDoTName}}); err != nil {
		t.Fatal(err)
	}

	eventually(t, namesTestWait, "the file name to be served", func() bool { return env.accepts(t, fileDoTName) })
	if !env.accepts(t, dotName) {
		t.Error("the configured name stopped being served")
	}
	assertNoSecretsLogged(t, env.logs)
}

func TestDoTNames_ServesOnlyTheFileNamesWhenTheConfigHasNone(t *testing.T) {
	env, _ := startNamesEnv(t, nil, namesLines(fileDoTName), nil)

	if !env.accepts(t, fileDoTName) {
		t.Error("the name from the file is refused")
	}
	if env.accepts(t, dotName) {
		t.Error("a name in neither the config nor the file is served")
	}
}

func TestDoTNames_RefusesARemovedNameForNewHandshakesButKeepsOpenConnections(t *testing.T) {
	env, path := startNamesEnv(t, []string{dotName}, namesLines(fileDoTName), nil)
	client := env.client(fileDoTName)
	conn, err := client.Dial(env.addr(t))
	if err != nil {
		t.Fatalf("dial with the file name: %v", err)
	}
	defer conn.Close()
	if _, _, err := client.ExchangeWithConn(queryFor("fine.example", dns.TypeA), conn); err != nil {
		t.Fatalf("query before the removal: %v", err)
	}

	if err := dotnames.Write(path, nil); err != nil {
		t.Fatal(err)
	}

	eventually(t, namesTestWait, "the removed name to be refused", func() bool { return !env.accepts(t, fileDoTName) })
	if _, _, err := client.ExchangeWithConn(queryFor("fine.example", dns.TypeA), conn); err != nil {
		t.Errorf("the connection opened before the removal was cut: %v", err)
	}
	if !env.accepts(t, dotName) {
		t.Error("the configured name stopped being served")
	}
}

func TestDoTNames_ANameInBothTheConfigAndTheFileStaysWhenTheFileDropsIt(t *testing.T) {
	env, path := startNamesEnv(t, []string{dotName}, namesLines(dotName, fileDoTName), nil)
	if !env.accepts(t, dotName) || !env.accepts(t, fileDoTName) {
		t.Fatal("a name from the union is refused")
	}
	if !env.logs.has(slog.LevelInfo, "names=2 from_file=2") {
		t.Error("the load was not logged with the union and file counts")
	}

	replaceNamesFile(t, path, namesLines())

	eventually(t, namesTestWait, "the file-only name to be refused", func() bool { return !env.accepts(t, fileDoTName) })
	if !env.accepts(t, dotName) {
		t.Error("a name that is still in the config was dropped with the file")
	}
	assertNoSecretsLogged(t, env.logs)
}

func TestDoTNames_KeepsTheCurrentNamesWhenTheFileTurnsBadAndLogsOnce(t *testing.T) {
	var tooMany []string
	for i := range dotnames.MaxEntries + 1 {
		tooMany = append(tooMany, fmt.Sprintf("name%02d.%s", i, dotBase))
	}
	cases := []struct {
		name   string
		config []string
		bad    []byte
		want   string
	}{
		{"an invalid line", []string{dotName}, namesLines(secretLabel + "." + dotBase), "line 1"},
		{"an oversize file", []string{dotName}, bytes.Repeat([]byte("a"), dotnames.MaxBytes+1), "too large"},
		{"too many names", []string{dotName}, namesLines(tooMany...), "too many"},
		{"an empty file with no config names", nil, namesLines(), "no server name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, path := startNamesEnv(t, tc.config, namesLines(fileDoTName), nil)

			replaceNamesFile(t, path, tc.bad)

			eventually(t, namesTestWait, "an error about the bad names file", func() bool {
				return env.logs.has(slog.LevelError, tc.want)
			})
			time.Sleep(10 * namesTestPoll)
			if n := env.logs.count(slog.LevelError, "names file"); n != 1 {
				t.Errorf("names file errors logged = %d, want 1 for one bad file", n)
			}
			if !env.accepts(t, fileDoTName) {
				t.Error("the bad file removed a name that was being served")
			}
			replaceNamesFile(t, path, namesLines(otherFileDoTName))
			eventually(t, namesTestWait, "the repaired file to be served", func() bool { return env.accepts(t, otherFileDoTName) })
			assertNoSecretsLogged(t, env.logs)
		})
	}
}

type namesSeed struct {
	name string
	data []byte
	dir  bool
}

func (s namesSeed) prepare(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dot-names.txt")
	switch {
	case s.dir:
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	case s.data != nil:
		if err := os.WriteFile(path, s.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestDoTNames_StartsWithTheConfigNamesWhenTheFileIsUnusable(t *testing.T) {
	cases := []namesSeed{
		{name: "an invalid line", data: namesLines(secretLabel + "." + dotBase)},
		{name: "an oversize file", data: bytes.Repeat([]byte("a"), dotnames.MaxBytes+1)},
		{name: "an unreadable file", dir: true},
		{name: "an empty file", data: namesLines()},
		{name: "a missing file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.prepare(t)
			env, err := newDoTEnv(t, time.Now().Add(60*24*time.Hour), namesConfig([]string{dotName}, path))
			if err != nil {
				t.Fatalf("newServer = %v, want the config names to be served", err)
			}
			env.s.names.every = namesTestPoll
			runServer(t, env.s)

			if !env.accepts(t, dotName) {
				t.Error("the configured name is refused")
			}
			time.Sleep(10 * namesTestPoll)
			if n := env.logs.count(slog.LevelError, "names file"); n > 1 {
				t.Errorf("names file errors logged = %d, want at most 1", n)
			}
			assertNoSecretsLogged(t, env.logs)
		})
	}
}

func TestDoTNames_StartupFailsWhenNoNameIsLeftToServe(t *testing.T) {
	cases := []namesSeed{
		{name: "a missing file"},
		{name: "an empty file", data: namesLines()},
		{name: "an invalid file", data: namesLines(secretLabel + "." + dotBase)},
		{name: "an unreadable file", dir: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.prepare(t)

			_, err := newDoTEnv(t, time.Now().Add(60*24*time.Hour), namesConfig(nil, path))

			if err == nil || !strings.Contains(err.Error(), "no server name") {
				t.Fatalf("newServer = %v, want an error about having no server name", err)
			}
			if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(secretLabel)) {
				t.Errorf("the error repeats a name from the file: %v", err)
			}
		})
	}
}

func TestDoTNames_CheckDoTReportsAnEmptyUnion(t *testing.T) {
	pki := newTestPKI(t)
	certPEM, keyPEM := pki.issue(t, 1, []string{dotBase, "*." + dotBase}, time.Now().Add(48*time.Hour))
	certPath, keyPath := writePair(t, t.TempDir(), certPEM, keyPEM)
	cfg := dotConfigFor(certPath, keyPath, "127.0.0.1:53")
	namesConfig(nil, filepath.Join(t.TempDir(), "dot-names.txt"))(&cfg)

	if _, err := CheckDoT(cfg); err == nil || !strings.Contains(err.Error(), "no server name") {
		t.Errorf("CheckDoT = %v, want an error about having no server name", err)
	}
}

func TestDoTNames_WarnsAboutFileNamesTheCertificateDoesNotCover(t *testing.T) {
	outside := "q1w2e3r4t5y6u7i8.other.example"
	env, _ := startNamesEnv(t, []string{dotName}, namesLines(fileDoTName, outside), nil)

	if !env.logs.has(slog.LevelWarn, "uncovered=1") {
		t.Error("no warning counts the file name the certificate does not cover")
	}
	if !env.accepts(t, fileDoTName) {
		t.Error("a covered file name is refused")
	}
	if env.logs.anywhere(outside) {
		t.Error("the uncovered name was written to the log")
	}
	assertNoSecretsLogged(t, env.logs)
}
