package engine

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseRuleLine_Valid(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Rule
	}{
		{"block", "block ads.example.com", Rule{RuleBlock, "ads.example.com"}},
		{"allow", "allow cdn.example.com", Rule{RuleAllow, "cdn.example.com"}},
		{"keyword in upper case", "BLOCK ads.example.com", Rule{RuleBlock, "ads.example.com"}},
		{"domain in upper case", "block ADS.Example.COM", Rule{RuleBlock, "ads.example.com"}},
		{"tab separated", "allow\tcdn.example.com", Rule{RuleAllow, "cdn.example.com"}},
		{"many spaces", "  block    ads.example.com   ", Rule{RuleBlock, "ads.example.com"}},
		{"trailing comment", "block ads.example.com # tracker", Rule{RuleBlock, "ads.example.com"}},
		{"comment without space", "block ads.example.com#x", Rule{RuleBlock, "ads.example.com"}},
		{"wildcard prefix", "block *.example.com", Rule{RuleBlock, "example.com"}},
		{"trailing dot", "block example.com.", Rule{RuleBlock, "example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := ParseRuleLine(tt.line)
			if err != nil || !ok {
				t.Fatalf("ParseRuleLine(%q) = %+v, %v, %v; want a rule", tt.line, got, ok, err)
			}
			if got != tt.want {
				t.Errorf("ParseRuleLine(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseRuleLine_BlankAndCommentAreNotRules(t *testing.T) {
	for _, line := range []string{"", "   ", "\t", "# a comment", "   # indented comment", "#block example.com"} {
		rule, ok, err := ParseRuleLine(line)
		if ok || err != nil {
			t.Errorf("ParseRuleLine(%q) = %+v, %v, %v; want no rule and no error", line, rule, ok, err)
		}
	}
}

func TestParseRuleLine_Invalid(t *testing.T) {
	tests := []struct {
		name string
		line string
		want error
	}{
		{"unknown keyword", "deny example.com", ErrUnknownAction},
		{"keyword glued to domain", "blockexample.com", ErrMissingDomain},
		{"missing domain", "block", ErrMissingDomain},
		{"missing domain before comment", "allow # nothing", ErrMissingDomain},
		{"extra field", "block example.com extra", ErrExtraFields},
		{"single label", "block com", ErrSingleLabel},
		{"ip address", "block 10.0.0.1", ErrIPAddress},
		{"bad character", "block exa_mple!.com", ErrBadLabel},
		{"url instead of domain", "block https://example.com/x", ErrBadLabel},
		{"binary garbage", "block \x00\xff\xfe.com", ErrBadLabel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, ok, err := ParseRuleLine(tt.line)
			if ok || !errors.Is(err, tt.want) {
				t.Errorf("ParseRuleLine(%q) = %+v, %v, %v; want error %v", tt.line, rule, ok, err, tt.want)
			}
		})
	}
}

func TestRule_StringIsTheFileLine(t *testing.T) {
	if got := (Rule{RuleAllow, "a.example.com"}).String(); got != "allow a.example.com" {
		t.Errorf("String() = %q", got)
	}
}

func parseRulesText(t *testing.T, text string) (*RuleSet, []RuleIssue) {
	t.Helper()
	set, issues, err := ParseRules(strings.NewReader(text))
	if err != nil {
		t.Fatalf("ParseRules error = %v", err)
	}
	return set, issues
}

func TestParseRules_SkipsBrokenLinesWithTheirNumbers(t *testing.T) {
	text := "# header\nblock ads.example.com\n\ndeny oops.com\nblock com\nallow ok.example.net\nblock\n"
	set, issues := parseRulesText(t, text)

	wantLines := []int{4, 5, 7}
	if len(issues) != len(wantLines) {
		t.Fatalf("issues = %+v, want lines %v", issues, wantLines)
	}
	for i, line := range wantLines {
		if issues[i].Line != line || issues[i].Err == nil {
			t.Errorf("issue %d = %+v, want line %d with an error", i, issues[i], line)
		}
	}
	if _, ok := set.Blocked("x.ads.example.com"); !ok {
		t.Error("the good block rule was lost because of broken lines around it")
	}
	if _, ok := set.Allowed("ok.example.net"); !ok {
		t.Error("the good allow rule was lost because of broken lines around it")
	}
	if set.Len() != 2 {
		t.Errorf("Len() = %d, want 2", set.Len())
	}
}

func TestParseRules_MatchingSemantics(t *testing.T) {
	set, _ := parseRulesText(t, "block example.com\nallow good.example.com\n")
	if entry, ok := set.Blocked("a.b.example.com"); !ok || entry != "example.com" {
		t.Errorf("Blocked = %q, %v; want example.com, true", entry, ok)
	}
	if _, ok := set.Blocked("notexample.com"); ok {
		t.Error("notexample.com must not be blocked by example.com")
	}
	if entry, ok := set.Allowed("x.good.example.com"); !ok || entry != "good.example.com" {
		t.Errorf("Allowed = %q, %v; want good.example.com, true", entry, ok)
	}
	if _, ok := set.Allowed("example.com"); ok {
		t.Error("allowing a subdomain must not allow its parent")
	}
}

func TestParseRules_DuplicatesAndBothKinds(t *testing.T) {
	set, issues := parseRulesText(t, "block a.example.com\nblock A.EXAMPLE.COM\nallow a.example.com\n")
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
	_, blocked := set.Blocked("a.example.com")
	_, allowed := set.Allowed("a.example.com")
	if !blocked || !allowed {
		t.Errorf("blocked = %v, allowed = %v; the set must report both so the engine can apply allow > block", blocked, allowed)
	}
}

func TestParseRules_CRLFAndBOM(t *testing.T) {
	text := "\xef\xbb\xbfblock ads.example.com\r\nallow cdn.example.com\r\n# c\r\n"
	set, issues := parseRulesText(t, text)
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
	if _, ok := set.Blocked("ads.example.com"); !ok {
		t.Error("first rule after the BOM was lost")
	}
	if _, ok := set.Allowed("cdn.example.com"); !ok {
		t.Error("CRLF rule was lost")
	}
}

func TestParseRules_LastLineWithoutNewline(t *testing.T) {
	set, _ := parseRulesText(t, "block ads.example.com")
	if _, ok := set.Blocked("ads.example.com"); !ok {
		t.Error("a final line without newline was ignored")
	}
}

func TestParseRules_HugeLineIsOneIssueAndParsingContinues(t *testing.T) {
	huge := "block " + strings.Repeat("a", 2<<20) + ".com"
	text := "block before.example.com\n" + huge + "\nblock after.example.com\n"
	set, issues := parseRulesText(t, text)

	if len(issues) != 1 || issues[0].Line != 2 || !errors.Is(issues[0].Err, ErrLineTooLong) {
		t.Fatalf("issues = %+v, want exactly line 2 with ErrLineTooLong", issues)
	}
	for _, name := range []string{"before.example.com", "after.example.com"} {
		if _, ok := set.Blocked(name); !ok {
			t.Errorf("%s was lost after a huge line", name)
		}
	}
}

func TestParseRules_BinaryFileDoesNotPanic(t *testing.T) {
	binary := bytes.Repeat([]byte{0x00, 0xff, 0x0a, 0x80, 0x0d}, 5000)
	set, issues, err := ParseRules(bytes.NewReader(binary))
	if err != nil {
		t.Fatalf("ParseRules error = %v", err)
	}
	if set.Len() != 0 || len(issues) == 0 {
		t.Errorf("Len() = %d, issues = %d; want no rules and some issues", set.Len(), len(issues))
	}
}

func TestParseRules_FileOverTheSizeCapKeepsOnlyWholeLines(t *testing.T) {
	line := "block r.example.com\n"
	filler := strings.Repeat("# padding padding padding padding padding\n", maxRulesFileBytes/42+100)
	text := "block first.example.com\n" + filler + strings.Repeat(line, 2)
	set, issues := parseRulesText(t, text)

	if _, ok := set.Blocked("first.example.com"); !ok {
		t.Error("a rule before the cap was lost")
	}
	if len(issues) == 0 || !errors.Is(issues[len(issues)-1].Err, ErrFileTooLarge) {
		t.Fatalf("issues = %+v, want a final ErrFileTooLarge", issues)
	}
}

func TestParseRules_EmptyInput(t *testing.T) {
	set, issues := parseRulesText(t, "")
	if set.Len() != 0 || len(issues) != 0 {
		t.Errorf("Len() = %d, issues = %d; want 0 and 0", set.Len(), len(issues))
	}
}

func TestParseRules_ReaderErrorIsReturned(t *testing.T) {
	wantErr := errors.New("disk on fire")
	_, _, err := ParseRules(errReader{wantErr})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestRuleSet_NilIsEmpty(t *testing.T) {
	var set *RuleSet
	if _, ok := set.Blocked("example.com"); ok {
		t.Error("nil set blocked a name")
	}
	if _, ok := set.Allowed("example.com"); ok {
		t.Error("nil set allowed a name")
	}
	if set.Len() != 0 {
		t.Errorf("Len() = %d, want 0", set.Len())
	}
}

func FuzzParseRuleLine(f *testing.F) {
	seeds := []string{
		"block example.com", "allow a.b", "# c", "", "block", "block  *.x.com", "\xff\xfe",
		"block exa mple.com", "allow " + strings.Repeat("a.", 200) + "com", "BLOCK\t\tEXAMPLE.COM # c",
		"block ::1", "block 1.2.3.4", "block \x00.com", "\xef\xbb\xbfblock a.com",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		rule, ok, err := ParseRuleLine(line)
		if ok && err != nil {
			t.Fatalf("ok and err together for %q", line)
		}
		if ok {
			again, ok2, err2 := ParseRuleLine(rule.String())
			if err2 != nil || !ok2 || again != rule {
				t.Fatalf("rule %+v does not survive a round trip: %+v, %v, %v", rule, again, ok2, err2)
			}
		}
	})
}

func TestParseRules_FileTooLargeIsReportedEvenAfterManyIssues(t *testing.T) {
	bad := strings.Repeat("nonsense line\n", maxRuleIssues+50)
	padding := strings.Repeat("# padding padding padding padding padding\n", MaxRulesFileBytes/42+100)
	_, issues := parseRulesText(t, bad+padding)

	var tooLarge, tooMany bool
	for _, issue := range issues {
		tooLarge = tooLarge || errors.Is(issue.Err, ErrFileTooLarge)
		tooMany = tooMany || errors.Is(issue.Err, ErrTooManyIssues)
	}
	if !tooLarge || !tooMany {
		t.Errorf("tooLarge = %v, tooMany = %v; the truncation of the file must never be hidden by the issue cap", tooLarge, tooMany)
	}
}

func TestParseRules_TheLineCutByTheSizeCapIsDropped(t *testing.T) {
	prefix := "block first.example.com\n"
	filler := strings.Repeat("#", MaxRulesFileBytes-len(prefix)-len("block cut.exa")-1) + "\n"
	set, issues := parseRulesText(t, prefix+filler+"block cut.example.com\nblock after.example.com\n")

	if _, ok := set.Blocked("first.example.com"); !ok {
		t.Error("a rule before the cap was lost")
	}
	for _, name := range []string{"cut.example.com", "cut.exa", "after.example.com"} {
		if _, ok := set.Blocked(name); ok {
			t.Errorf("%s is blocked, but it lies beyond or across the size cap", name)
		}
	}
	if len(issues) == 0 || !errors.Is(issues[len(issues)-1].Err, ErrFileTooLarge) {
		t.Errorf("issues = %+v, want a final ErrFileTooLarge", issues)
	}
}
