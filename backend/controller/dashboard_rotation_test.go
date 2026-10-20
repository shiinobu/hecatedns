package controller

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	fileNameOne = "aaaaaaaaaaaaaaaaaaaa.example.org"
	fileNameTwo = "bbbbbbbbbbbbbbbbbbbb.example.org"
)

func withPublicResolvers(t *testing.T, addrs ...string) {
	t.Helper()
	old := publicResolvers
	publicResolvers = addrs
	t.Cleanup(func() { publicResolvers = old })
}

func (f dashboardRunFixture) withRotation(t *testing.T) string {
	t.Helper()
	path := filepath.Join(f.dir, "dot-names.txt")
	text := strings.Replace(readText(t, f.config), "dot:\n  names:", fmt.Sprintf("dot:\n  names_file: %q\n  names:", filepath.ToSlash(path)), 1)
	text = strings.Replace(text, "dashboard:\n", "dashboard:\n  probe_domains: [example.org]\n", 1)
	if err := os.WriteFile(f.config, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDashboardRun_RotationAddsAProtectedNameAndRemovalReleasesIt(t *testing.T) {
	withPublicResolvers(t, startTestDNSUpstream(t, false))
	f := newDashboardRunFixture(t, true)
	file := f.withRotation(t)
	f.start(t)
	c := newDashboardClient(t, f.port)
	c.waitUntilUp(t)
	c.login(t, f.key)

	status, body := c.do(http.MethodPost, "/api/connection/rotate", `{"domain":"example.org"}`)

	if status != http.StatusOK {
		t.Fatalf("rotate = %d %v", status, body)
	}
	host, _ := body["host"].(string)
	if !strings.HasSuffix(host, ".example.org") || len(host) != dotnames.LabelLen+len(".example.org") {
		t.Fatalf("host = %q", host)
	}
	entries, _, err := dotnames.Read(file)
	if err != nil || len(entries) != 1 || entries[0].Host != host {
		t.Fatalf("names file = %+v, err = %v", entries, err)
	}
	_, conn := c.do(http.MethodGet, "/api/connection", "")
	if rotation, _ := conn["rotation"].(map[string]any); rotation["available"] != true {
		t.Errorf("rotation = %v", conn["rotation"])
	}
	if names, _ := conn["names"].([]any); len(names) != 2 {
		t.Errorf("names = %v", conn["names"])
	}
	block := fmt.Sprintf(`{"action":"block","domain":%q}`, host)
	if status, body := c.do(http.MethodPost, "/api/rules", block); status != http.StatusBadRequest || body["error"] != "protected_name" {
		t.Errorf("block of the new DoT name = %d %v, want protected_name", status, body)
	}

	if status, body := c.do(http.MethodPost, "/api/connection/remove", fmt.Sprintf(`{"host":%q}`, host)); status != http.StatusOK {
		t.Fatalf("remove = %d %v", status, body)
	}
	if status, body := c.do(http.MethodPost, "/api/rules", block); status != http.StatusOK {
		t.Errorf("block of a removed name = %d %v, want it allowed again", status, body)
	}
}

func TestDashboardRun_RotationFailsWhenThePublicResolversDoNotAnswer(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := conn.LocalAddr().String()
	_ = conn.Close()
	withPublicResolvers(t, dead)
	f := newDashboardRunFixture(t, true)
	file := f.withRotation(t)
	f.start(t)
	c := newDashboardClient(t, f.port)
	c.waitUntilUp(t)
	c.login(t, f.key)

	status, body := c.do(http.MethodPost, "/api/connection/rotate", `{"domain":"example.org"}`)

	if status != http.StatusBadGateway || body["error"] != "resolve_failed" {
		t.Errorf("rotate = %d %v", status, body)
	}
	if entries, _, _ := dotnames.Read(file); len(entries) != 0 {
		t.Errorf("the names file changed: %+v", entries)
	}
}

func TestDashboardServes_NeedsDoTOnALoopbackTarget(t *testing.T) {
	cfg := config.Config{}
	if dashboardServes(cfg) != nil {
		t.Error("a check exists although DoT is disabled")
	}
	cfg.DoT = config.DoT{Enabled: true, Listen: []string{"0.0.0.0:853"}}
	if dashboardServes(cfg) == nil {
		t.Error("no check although DoT listens")
	}
}

func TestServedDoTNames_ConfigNamesFirstThenTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := dotnames.Write(path, []dotnames.Entry{{Host: fileNameOne}, {Host: fileNameTwo}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DoT: config.DoT{Names: []string{runDoTName}, NamesFile: path}}

	got := servedDoTNames(cfg)

	if want := []string{runDoTName, fileNameOne, fileNameTwo}; !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

func TestServedDoTNames_ABrokenFileLeavesTheConfigNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(path, []byte("not a host\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DoT: config.DoT{Names: []string{runDoTName}, NamesFile: path}}

	if got := servedDoTNames(cfg); !reflect.DeepEqual(got, []string{runDoTName}) {
		t.Errorf("names = %v", got)
	}
}

func TestFileNames_RereadsOnlyWhenTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	names := newFileNames(path)
	if got := names.Hosts(); len(got) != 0 {
		t.Fatalf("a missing file gave %v", got)
	}

	if err := dotnames.Write(path, []dotnames.Entry{{Host: fileNameOne}}); err != nil {
		t.Fatal(err)
	}
	if got := names.Hosts(); !reflect.DeepEqual(got, []string{fileNameOne}) {
		t.Fatalf("after the first write: %v", got)
	}

	past := time.Now().Add(-time.Hour)
	if err := dotnames.Write(path, []dotnames.Entry{{Host: fileNameOne}, {Host: fileNameTwo}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if got := names.Hosts(); !reflect.DeepEqual(got, []string{fileNameOne, fileNameTwo}) {
		t.Fatalf("after the second write: %v", got)
	}
}

func TestFileNames_KeepsTheLastGoodListWhenTheFileBreaks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := dotnames.Write(path, []dotnames.Entry{{Host: fileNameOne}}); err != nil {
		t.Fatal(err)
	}
	names := newFileNames(path)
	_ = names.Hosts()

	if err := os.WriteFile(path, []byte("not a host at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := names.Hosts(); !reflect.DeepEqual(got, []string{fileNameOne}) {
		t.Errorf("after the file broke: %v", got)
	}
}
