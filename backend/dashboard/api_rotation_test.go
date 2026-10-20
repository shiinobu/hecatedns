package dashboard

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	rotLabel = "aaaaaaaaaaaaaaaaaaaa"
	rotHost  = rotLabel + "." + connMain
	rotNameB = "bbbbbbbbbbbbbbbbbbbb." + connMain
	rotNameC = "cccccccccccccccccccc." + connMain
	rotNameD = "dddddddddddddddddddd." + connMain
	rotNameE = "eeeeeeeeeeeeeeeeeeee." + connMain
)

type rotationRig struct {
	*apiFixture
	file string

	mu       sync.Mutex
	resolved []string
	served   []string
}

func newRotationRig(t *testing.T, mut func(*ConnectionOptions)) *rotationRig {
	t.Helper()
	rig := &rotationRig{file: filepath.Join(t.TempDir(), "dot-names.txt")}
	rig.apiFixture = newAPIFixture(t, func(o *APIOptions) {
		conn := ConnectionOptions{
			Names:        []string{connNameA},
			ProbeDomains: []string{connMain, connBackup},
			Scheme:       "https",
			Port:         443,
			Rotation: RotationOptions{
				File: rig.file,
				Resolves: func(_ context.Context, host string) error {
					rig.mu.Lock()
					defer rig.mu.Unlock()
					rig.resolved = append(rig.resolved, host)
					return nil
				},
				Random:         bytes.NewReader(make([]byte, 400)),
				ResolveTimeout: time.Second,
				ServeWait:      time.Second,
				ServePoll:      time.Millisecond,
			},
		}
		if mut != nil {
			mut(&conn)
		}
		o.Connection = conn
	})
	return rig
}

func writeRaw(path, text string) error {
	return os.WriteFile(path, []byte(text), 0o600)
}

func (r *rotationRig) seed(entries ...dotnames.Entry) {
	r.t.Helper()
	if err := dotnames.Write(r.file, entries); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rotationRig) fileHosts() []string {
	r.t.Helper()
	entries, _, err := dotnames.Read(r.file)
	if err != nil {
		r.t.Fatalf("read names file: %v", err)
	}
	return dotnames.Hosts(entries)
}

func (r *rotationRig) rotate(domain string) (int, map[string]any) {
	r.t.Helper()
	rec := r.post("/api/connection/rotate", `{"domain":"`+domain+`"}`)
	return rec.Code, decodeMap(r.t, rec)
}

func (r *rotationRig) remove(host string) (int, map[string]any) {
	r.t.Helper()
	rec := r.post("/api/connection/remove", `{"host":"`+host+`"}`)
	return rec.Code, decodeMap(r.t, rec)
}

func TestRotate_AddsAFreshNameAndReturnsTheList(t *testing.T) {
	rig := newRotationRig(t, nil)

	code, body := rig.rotate(connMain)

	if code != http.StatusOK {
		t.Fatalf("status %d, body %v", code, body)
	}
	if body["host"] != rotHost {
		t.Errorf("host = %v, want %s", body["host"], rotHost)
	}
	names := connectionNames(t, body)
	if len(names) != 2 {
		t.Fatalf("names = %v", names)
	}
	wantFirst := map[string]any{"host": connNameA, "base": connMain, "source": "config", "current": false, "removable": false, "added": nil, "expired": false}
	if !reflect.DeepEqual(names[0], wantFirst) {
		t.Errorf("names[0] = %v, want %v", names[0], wantFirst)
	}
	wantSecond := map[string]any{"host": rotHost, "base": connMain, "source": "file", "current": true, "removable": true, "added": apiNow.UTC().Format(time.RFC3339), "expired": false}
	if !reflect.DeepEqual(names[1], wantSecond) {
		t.Errorf("names[1] = %v, want %v", names[1], wantSecond)
	}
	if got := rig.fileHosts(); !reflect.DeepEqual(got, []string{rotHost}) {
		t.Errorf("file = %v", got)
	}
	if len(rig.resolved) != 1 || rig.resolved[0] != rotHost {
		t.Errorf("resolved = %v", rig.resolved)
	}
}

func TestRotate_NamesFromTheFileShowInTheConnectionList(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.seed(dotnames.Entry{Host: rotNameB, Added: apiNow})

	body := decodeMap(t, rig.get("/api/connection"))

	if got := field(t, body, "rotation", "available"); got != true {
		t.Errorf("rotation.available = %v", got)
	}
	names := connectionNames(t, body)
	if len(names) != 2 || names[1]["host"] != rotNameB || names[1]["source"] != "file" || names[1]["current"] != true {
		t.Errorf("names = %v", names)
	}
}

func TestRotate_NotAvailableWithoutANamesFile(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) { c.Rotation.File = "" })

	code, body := rig.rotate(connMain)

	if code != http.StatusNotFound || body["error"] != "rotation_unavailable" {
		t.Errorf("status %d, body %v", code, body)
	}
	if got := field(t, decodeMap(t, rig.get("/api/connection")), "rotation", "available"); got != false {
		t.Errorf("rotation.available = %v", got)
	}
}

func TestRotate_NotAvailableWithoutAResolveCheck(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) { c.Rotation.Resolves = nil })

	code, body := rig.rotate(connMain)

	if code != http.StatusNotFound || body["error"] != "rotation_unavailable" {
		t.Errorf("status %d, body %v", code, body)
	}
}

func TestRotate_RefusesADomainThatIsNotConfigured(t *testing.T) {
	for _, domain := range []string{"example.com", "", "ghost.example.org.evil.com", "evil.ghost.example.org", "../x"} {
		t.Run(domain, func(t *testing.T) {
			rig := newRotationRig(t, nil)

			code, body := rig.rotate(domain)

			if code != http.StatusBadRequest || body["error"] != "bad_domain" {
				t.Errorf("status %d, body %v", code, body)
			}
			if len(rig.fileHosts()) != 0 {
				t.Errorf("the file changed: %v", rig.fileHosts())
			}
		})
	}
}

func TestRotate_AcceptsTheBackupDomainAndMixedCase(t *testing.T) {
	rig := newRotationRig(t, nil)

	code, body := rig.rotate(strings.ToUpper(connBackup))

	if code != http.StatusOK || body["host"] != rotLabel+"."+connBackup {
		t.Errorf("status %d, body %v", code, body)
	}
}

func TestRotate_RefusesAnUncoveredCertificate(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.api.opts.Cert = fakeCert{byName: map[string]time.Time{}}

	code, body := rig.rotate(connMain)

	if code != http.StatusBadRequest || body["error"] != "bad_domain" {
		t.Errorf("status %d, body %v", code, body)
	}
	if len(rig.resolved) != 0 || len(rig.fileHosts()) != 0 {
		t.Errorf("work happened before the certificate check: resolved %v, file %v", rig.resolved, rig.fileHosts())
	}
}

func TestRotate_AcceptsACoveredCertificate(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.api.opts.Cert = fakeCert{byName: map[string]time.Time{rotHost: apiNow.Add(24 * time.Hour)}}

	code, body := rig.rotate(connMain)

	if code != http.StatusOK {
		t.Errorf("status %d, body %v", code, body)
	}
}

func TestRotate_RefusesAtTheNameCap(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.seed(dotnames.Entry{Host: rotNameB}, dotnames.Entry{Host: rotNameC}, dotnames.Entry{Host: rotNameD}, dotnames.Entry{Host: rotNameE})

	code, body := rig.rotate(connMain)

	if code != http.StatusConflict || body["error"] != "too_many_names" {
		t.Errorf("status %d, body %v", code, body)
	}
	if len(rig.fileHosts()) != 4 || len(rig.resolved) != 0 {
		t.Errorf("file %v, resolved %v", rig.fileHosts(), rig.resolved)
	}
}

func TestRotate_ResolveFailureAddsNothingAndLeaksNoName(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.Resolves = func(_ context.Context, host string) error { return errors.New("1.1.1.1 did not answer for " + host) }
	})

	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "resolve_failed" {
		t.Errorf("status %d, body %v", code, body)
	}
	if strings.Contains(body["message"].(string), rotLabel) {
		t.Errorf("the message leaks the new name: %v", body["message"])
	}
	if len(rig.fileHosts()) != 0 {
		t.Errorf("the file changed: %v", rig.fileHosts())
	}
}

func TestRotate_ResolveCheckIsBoundedByItsTimeout(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.ResolveTimeout = 20 * time.Millisecond
		c.Rotation.Resolves = func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}
	})

	started := time.Now()
	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "resolve_failed" || time.Since(started) > 2*time.Second {
		t.Errorf("status %d, body %v, took %v", code, body, time.Since(started))
	}
}

func TestRotate_WaitsUntilTheServerServesTheName(t *testing.T) {
	var calls int
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.Serves = func(context.Context, string) error {
			calls++
			if calls < 3 {
				return errors.New("not yet")
			}
			return nil
		}
	})

	code, body := rig.rotate(connMain)

	if code != http.StatusOK || calls != 3 {
		t.Errorf("status %d, body %v, calls %d", code, body, calls)
	}
}

func TestRotate_RollsBackWhenTheServerNeverServesTheName(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.ServeWait = 30 * time.Millisecond
		c.Rotation.Serves = func(context.Context, string) error { return errors.New("not served") }
	})
	rig.seed(dotnames.Entry{Host: rotNameB})

	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "not_served" {
		t.Errorf("status %d, body %v", code, body)
	}
	if got := rig.fileHosts(); !reflect.DeepEqual(got, []string{rotNameB}) {
		t.Errorf("the file was not rolled back: %v", got)
	}
}

func TestRotate_RandomSourceFailureIsInternal(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) { c.Rotation.Random = bytes.NewReader(nil) })

	code, body := rig.rotate(connMain)

	if code != http.StatusInternalServerError || body["error"] != "internal" {
		t.Errorf("status %d, body %v", code, body)
	}
	if len(rig.fileHosts()) != 0 {
		t.Errorf("the file changed: %v", rig.fileHosts())
	}
}

func TestRotate_CorruptFileIsInternalAndLeftAlone(t *testing.T) {
	rig := newRotationRig(t, nil)
	if err := writeRaw(rig.file, "not a host\n"); err != nil {
		t.Fatal(err)
	}

	code, body := rig.rotate(connMain)

	if code != http.StatusInternalServerError || body["error"] != "internal" {
		t.Errorf("status %d, body %v", code, body)
	}
	if got := field(t, decodeMap(t, rig.get("/api/connection")), "rotation", "available"); got != false {
		t.Errorf("rotation.available = %v with a corrupt file", got)
	}
}

func TestRotate_RejectsBadBodies(t *testing.T) {
	rig := newRotationRig(t, nil)
	for _, body := range []string{"", "{", `{"domain":1}`, `{"domain":"` + connMain + `","extra":1}`} {
		rec := rig.post("/api/connection/rotate", body)
		if rec.Code != http.StatusBadRequest || decodeMap(t, rec)["error"] != "bad_request" {
			t.Errorf("body %q gave %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestRotate_ConcurrentRequestsRespectTheCap(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) { c.Rotation.Random = nil })
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := rig.rotate(connMain)
			for body["error"] == "busy" {
				code, body = rig.rotate(connMain)
			}
			mu.Lock()
			codes[code]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if codes[http.StatusOK] != rotationMaxNames || codes[http.StatusConflict] != 10-rotationMaxNames {
		t.Errorf("codes = %v", codes)
	}
	if len(rig.fileHosts()) != rotationMaxNames {
		t.Errorf("file = %v", rig.fileHosts())
	}
}

func TestRotate_AnOverlappingRequestIsToldTheServerIsBusy(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.Resolves = func(ctx context.Context, _ string) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}
	})
	first := make(chan int, 1)
	go func() {
		code, _ := rig.rotate(connMain)
		first <- code
	}()
	<-entered

	code, body := rig.rotate(connMain)
	close(release)

	if code != http.StatusConflict || body["error"] != "busy" {
		t.Errorf("second request: status %d, body %v", code, body)
	}
	if got := <-first; got != http.StatusOK {
		t.Errorf("first request: status %d", got)
	}
	if len(rig.fileHosts()) != 1 {
		t.Errorf("file = %v", rig.fileHosts())
	}
}

func TestRemove_AnOverlappingRequestIsToldTheServerIsBusy(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.Resolves = func(context.Context, string) error {
			close(entered)
			<-release
			return nil
		}
	})
	rig.seed(dotnames.Entry{Host: rotNameB}, dotnames.Entry{Host: rotNameC})
	rotated := make(chan struct{})
	go func() {
		defer close(rotated)
		rig.rotate(connMain)
	}()
	<-entered

	code, body := rig.remove(rotNameB)
	close(release)
	<-rotated

	if code != http.StatusConflict || body["error"] != "busy" {
		t.Errorf("status %d, body %v", code, body)
	}
}

func TestRotate_SaysSoWhenTheRollbackItselfFails(t *testing.T) {
	var rig *rotationRig
	rig = newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.ServeWait = 30 * time.Millisecond
		c.Rotation.Serves = func(context.Context, string) error {
			_ = os.Remove(rig.file)
			_ = os.Mkdir(rig.file, 0o700)
			return errors.New("not served")
		}
	})

	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "not_served" {
		t.Fatalf("status %d, body %v", code, body)
	}
	if message, _ := body["message"].(string); !strings.Contains(message, "could not be restored") {
		t.Errorf("message = %q, want it to say the names file could not be restored", message)
	}
}

func TestRemove_DropsAFileName(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.seed(dotnames.Entry{Host: rotNameB}, dotnames.Entry{Host: rotNameC})

	code, body := rig.remove(rotNameB)

	if code != http.StatusOK {
		t.Fatalf("status %d, body %v", code, body)
	}
	names := connectionNames(t, body)
	if len(names) != 2 || names[0]["host"] != connNameA || names[1]["host"] != rotNameC {
		t.Errorf("names = %v", names)
	}
	if got := rig.fileHosts(); !reflect.DeepEqual(got, []string{rotNameC}) {
		t.Errorf("file = %v", got)
	}
}

func TestRemove_IgnoresCaseAndSpaces(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.seed(dotnames.Entry{Host: rotNameB}, dotnames.Entry{Host: rotNameC})

	code, _ := rig.remove("  " + strings.ToUpper(rotNameB) + " ")

	if code != http.StatusOK || len(rig.fileHosts()) != 1 {
		t.Errorf("status %d, file %v", code, rig.fileHosts())
	}
}

func TestRemove_RefusesConfigDefinedNames(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.seed(dotnames.Entry{Host: rotNameB})

	code, body := rig.remove(connNameA)

	if code != http.StatusBadRequest || body["error"] != "not_removable" {
		t.Errorf("status %d, body %v", code, body)
	}
}

func TestRemove_RefusesTheLastName(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) { c.Names = nil })
	rig.seed(dotnames.Entry{Host: rotNameB})

	code, body := rig.remove(rotNameB)

	if code != http.StatusBadRequest || body["error"] != "last_name" {
		t.Errorf("status %d, body %v", code, body)
	}
	if len(rig.fileHosts()) != 1 {
		t.Errorf("file = %v", rig.fileHosts())
	}
}

func TestRemove_UnknownNameIsNotFound(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.seed(dotnames.Entry{Host: rotNameB})

	code, body := rig.remove(rotNameC)

	if code != http.StatusNotFound || body["error"] != "not_found" {
		t.Errorf("status %d, body %v", code, body)
	}
}

func TestRemove_NotAvailableWithoutANamesFile(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) { c.Rotation.File = "" })

	code, body := rig.remove(rotNameB)

	if code != http.StatusNotFound || body["error"] != "rotation_unavailable" {
		t.Errorf("status %d, body %v", code, body)
	}
}

func TestConnection_ALastFileNameIsNotRemovable(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) { c.Names = nil })
	rig.seed(dotnames.Entry{Host: rotNameB})

	names := connectionNames(t, decodeMap(t, rig.get("/api/connection")))

	if len(names) != 1 || names[0]["removable"] != false {
		t.Errorf("names = %v", names)
	}
}

func TestConnection_ANameInBothSourcesCountsOnceAndStaysReadOnly(t *testing.T) {
	rig := newRotationRig(t, nil)
	rig.seed(dotnames.Entry{Host: connNameA})

	names := connectionNames(t, decodeMap(t, rig.get("/api/connection")))

	if len(names) != 1 || names[0]["source"] != "config" || names[0]["removable"] != false {
		t.Errorf("names = %v", names)
	}
}
