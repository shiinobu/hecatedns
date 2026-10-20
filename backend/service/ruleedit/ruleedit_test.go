package ruleedit

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shiinobu/hecatedns/backend/engine"
)

func writeRules(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write rules: %v", err)
	}
	return path
}

func readRules(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	return string(data)
}

func TestAdd_CreatesMissingFileAndDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "rules.txt")

	res, err := Add(path, engine.Rule{Action: engine.RuleBlock, Domain: "ads.example.com"})

	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if res.Replaced != nil || res.ReplacedLines != 0 {
		t.Fatalf("nothing should be replaced, got %+v", res)
	}
	if got := readRules(t, path); got != "block ads.example.com\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestAdd_ReturnsTheReplacedRule(t *testing.T) {
	path := writeRules(t, "allow a.example.com\nblock b.example.com\n")

	res, err := Add(path, engine.Rule{Action: engine.RuleBlock, Domain: "a.example.com"})

	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	want := engine.Rule{Action: engine.RuleAllow, Domain: "a.example.com"}
	if res.Replaced == nil || *res.Replaced != want || res.ReplacedLines != 1 {
		t.Fatalf("result = %+v, want replaced %v", res, want)
	}
	if got := readRules(t, path); got != "block b.example.com\nblock a.example.com\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestAdd_KeepsCRLFAndReplacesTheBOMLine(t *testing.T) {
	path := writeRules(t, "\xef\xbb\xbfblock a.example.com\r\nblock b.example.com\r\n")

	res, err := Add(path, engine.Rule{Action: engine.RuleAllow, Domain: "a.example.com"})

	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if res.ReplacedLines != 1 {
		t.Fatalf("replaced lines = %d, want 1", res.ReplacedLines)
	}
	if got := readRules(t, path); got != "block b.example.com\r\nallow a.example.com\r\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestAdd_RefusesAFileThatWouldPassTheCap(t *testing.T) {
	filler := strings.Repeat("block a.example.com\n", engine.MaxRulesFileBytes/len("block a.example.com\n"))
	path := writeRules(t, filler)

	_, err := Add(path, engine.Rule{Action: engine.RuleBlock, Domain: "new.example.com"})

	if !errors.Is(err, ErrFileFull) || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want ErrFileFull", err)
	}
}

func TestAdd_ReportsAFileAlreadyOverTheCapAsFull(t *testing.T) {
	path := writeRules(t, strings.Repeat("x", engine.MaxRulesFileBytes+1))

	_, err := Add(path, engine.Rule{Action: engine.RuleBlock, Domain: "new.example.com"})

	if !errors.Is(err, ErrFileFull) {
		t.Fatalf("err = %v, want ErrFileFull", err)
	}
}

func TestRemove_ReturnsTheRemovedRulesAndKeepsOtherLines(t *testing.T) {
	path := writeRules(t, "block a.example.com\n# note\nallow a.example.com\nblock b.example.com\n")

	removed, err := Remove(path, "a.example.com")

	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	want := []engine.Rule{
		{Action: engine.RuleBlock, Domain: "a.example.com"},
		{Action: engine.RuleAllow, Domain: "a.example.com"},
	}
	if !slices.Equal(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	if got := readRules(t, path); got != "# note\nblock b.example.com\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestRemove_UnknownDomainChangesNothing(t *testing.T) {
	path := writeRules(t, "block a.example.com\n")

	removed, err := Remove(path, "other.example.com")

	if err != nil || len(removed) != 0 {
		t.Fatalf("removed = %v, err = %v", removed, err)
	}
	if got := readRules(t, path); got != "block a.example.com\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestList_ReturnsRulesAndSkippedLines(t *testing.T) {
	long := "block " + strings.Repeat("a", engine.MaxRuleLineBytes)
	path := writeRules(t, "block a.example.com\n"+long+"\n# note\nallow b.example.com\n")

	listing, err := List(path)

	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listing.Rules) != 2 || listing.Rules[0].Domain != "a.example.com" || listing.Rules[1].Action != engine.RuleAllow {
		t.Fatalf("rules = %+v", listing.Rules)
	}
	if len(listing.Skipped) != 1 || listing.Skipped[0].Line != 2 || listing.Skipped[0].Err == nil {
		t.Fatalf("skipped = %+v", listing.Skipped)
	}
}

func TestList_MissingFileIsEmpty(t *testing.T) {
	listing, err := List(filepath.Join(t.TempDir(), "none.txt"))

	if err != nil || len(listing.Rules) != 0 || len(listing.Skipped) != 0 {
		t.Fatalf("listing = %+v, err = %v", listing, err)
	}
}

func TestList_RefusesADirectory(t *testing.T) {
	if _, err := List(t.TempDir()); err == nil {
		t.Fatal("a directory must be refused")
	}
}

func TestParseRule_NamesABadActionApartFromABadDomain(t *testing.T) {
	_, actionErr := ParseRule("deny", "x.example.com")
	_, domainErr := ParseRule("block", "localhost")

	if !errors.Is(actionErr, ErrBadAction) || errors.Is(domainErr, ErrBadAction) || domainErr == nil {
		t.Fatalf("action err = %v, domain err = %v", actionErr, domainErr)
	}
}

func TestParseRule(t *testing.T) {
	tests := []struct {
		name, action, domain string
		want                 engine.Rule
		errPart              string
	}{
		{name: "block", action: "BLOCK", domain: "Ads.Example.COM", want: engine.Rule{Action: engine.RuleBlock, Domain: "ads.example.com"}},
		{name: "allow", action: "allow", domain: "x.example.com", want: engine.Rule{Action: engine.RuleAllow, Domain: "x.example.com"}},
		{name: "bad action", action: "deny", domain: "x.example.com", errPart: "must be block or allow"},
		{name: "unicode name", action: "block", domain: "é.example.com", errPart: "punycode"},
		{name: "single label", action: "block", domain: "localhost", errPart: "rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRule(tt.action, tt.domain)
			if tt.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errPart) {
					t.Fatalf("err = %v, want part %q", err, tt.errPart)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, err %v, want %+v", got, err, tt.want)
			}
		})
	}
}
