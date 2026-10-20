package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	namesProgram   = "../../../deploy/lib/dot-names.awk"
	rewriteProgram = "../../../deploy/lib/dot-rewrite.awk"

	nameOne = "aaaaaaaaaaaaaaaaaaaa.ghost.example.org"
	nameTwo = "bbbbbbbbbbbbbbbbbbbb.ghost.example.org"
	file    = "/var/lib/hecate/dot-names.txt"
)

func awkRun(t *testing.T, program, config string, vars ...string) string {
	t.Helper()
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("no awk available")
	}
	path := filepath.Join(t.TempDir(), "hecate.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{}
	for _, v := range vars {
		args = append(args, "-v", v)
	}
	args = append(args, "-f", filepath.ToSlash(program), filepath.ToSlash(path))
	out, err := exec.Command("awk", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("awk: %v\n%s", err, out)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}

const blockStyle = `backend: native
dot:
  enabled: true
  listen: ["0.0.0.0:853"]
  cert_file: /var/lib/hecate/lego/certificates/c.crt
  key_file: /var/lib/hecate/lego/certificates/c.key
  names:                         # a long random label
    - ` + nameOne + `
    - "` + nameTwo + `"   # second
  reload_every: 10m

dashboard:
  enabled: false
  names:
    - dashhost.example.org
`

const flowStyle = `dot:
  enabled: true
  names: [` + nameOne + `, "` + nameTwo + `"]
  reload_every: 10m
dashboard:
  names: [dashhost.example.org]
`

func TestDotNames_ReadsABlockList(t *testing.T) {
	got := awkRun(t, namesProgram, blockStyle)

	if want := nameOne + "\n" + nameTwo + "\n"; got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestDotNames_ReadsAFlowList(t *testing.T) {
	got := awkRun(t, namesProgram, flowStyle)

	if want := nameOne + "\n" + nameTwo + "\n"; got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestDotNames_IgnoresNamesOutsideTheDoTSection(t *testing.T) {
	got := awkRun(t, namesProgram, "dashboard:\n  names:\n    - dashhost.example.org\ndot:\n  enabled: true\n")

	if got != "" {
		t.Errorf("names = %q, want none", got)
	}
}

func TestDotRewrite_ReplacesABlockListWithTheNamesFile(t *testing.T) {
	got := awkRun(t, rewriteProgram, blockStyle, "file="+file)

	want := `backend: native
dot:
  enabled: true
  listen: ["0.0.0.0:853"]
  cert_file: /var/lib/hecate/lego/certificates/c.crt
  key_file: /var/lib/hecate/lego/certificates/c.key
  names_file: ` + file + `
  reload_every: 10m

dashboard:
  enabled: false
  names:
    - dashhost.example.org
`
	if got != want {
		t.Errorf("config =\n%s\nwant\n%s", got, want)
	}
}

func TestDotRewrite_ReplacesAFlowListWithTheNamesFile(t *testing.T) {
	got := awkRun(t, rewriteProgram, flowStyle, "file="+file)

	want := `dot:
  enabled: true
  names_file: ` + file + `
  reload_every: 10m
dashboard:
  names: [dashhost.example.org]
`
	if got != want {
		t.Errorf("config =\n%s\nwant\n%s", got, want)
	}
}

func TestDotRewrite_LeavesAConfigWithoutDoTNamesAlone(t *testing.T) {
	config := "backend: native\ndot:\n  enabled: true\n  reload_every: 10m\n"

	if got := awkRun(t, rewriteProgram, config, "file="+file); got != config {
		t.Errorf("config changed:\n%s", got)
	}
}
