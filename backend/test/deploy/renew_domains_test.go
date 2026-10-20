package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	renewLib = "../../../deploy/lib/renew-domains.sh"
	execHead = "ExecStart=/opt/hecate/lego --path /var/lib/hecate/lego --email ${LEGO_EMAIL} --accept-tos --dns cloudflare --dns.resolvers 1.1.1.1:53"
	execTail = " renew --days 30"
)

func normalize(t *testing.T, domains, base string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh available")
	}
	unit := filepath.Join(t.TempDir(), "hecate-renew.service")
	text := "[Service]\nUser=hecate-acme\n" + execHead + domains + execTail + "\nUMask=0027\n"
	if err := os.WriteFile(unit, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	script := ". " + filepath.ToSlash(renewLib) + " && normalize_renew_domains " + filepath.ToSlash(unit) + " " + base
	if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("sh: %v\n%s", err, out)
	}
	data, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			if !strings.HasPrefix(line, execHead) || !strings.HasSuffix(line, execTail) {
				t.Fatalf("the lego options around the domains changed: %s", line)
			}
			if !strings.Contains(got, "User=hecate-acme\n") || !strings.Contains(got, "UMask=0027\n") {
				t.Fatalf("other unit lines changed:\n%s", got)
			}
			return strings.TrimSuffix(strings.TrimPrefix(line, execHead), execTail)
		}
	}
	t.Fatalf("no ExecStart in\n%s", got)
	return ""
}

const wanted = " --domains *.hecate.my.id --domains *.ghost.hecate.my.id"

func TestRenewDomains_DropsTheNameTheWildcardCoversAndPutsTheWildcardFirst(t *testing.T) {
	got := normalize(t, " --domains ghost.hecate.my.id --domains *.ghost.hecate.my.id", "hecate.my.id")

	if got != wanted {
		t.Errorf("domains = %q, want %q", got, wanted)
	}
}

func TestRenewDomains_RepairsTheEarlierAppendedWildcard(t *testing.T) {
	got := normalize(t, " --domains ghost.hecate.my.id --domains *.ghost.hecate.my.id --domains *.hecate.my.id", "hecate.my.id")

	if got != wanted {
		t.Errorf("domains = %q, want %q", got, wanted)
	}
}

func TestRenewDomains_IsIdempotent(t *testing.T) {
	got := normalize(t, wanted, "hecate.my.id")

	if got != wanted {
		t.Errorf("domains = %q, want them unchanged", got)
	}
}

func TestRenewDomains_KeepsTheApexAndOtherZones(t *testing.T) {
	got := normalize(t, " --domains hecate.my.id --domains *.ghost.hecate.my.id --domains other.example.org", "hecate.my.id")

	want := " --domains *.hecate.my.id --domains hecate.my.id --domains *.ghost.hecate.my.id --domains other.example.org"
	if got != want {
		t.Errorf("domains = %q, want %q", got, want)
	}
}
