package dotnames

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	hostA = "aaaaaaaaaaaaaaaaaaaa.hecate.my.id"
	hostB = "bbbbbbbbbbbbbbbbbbbb.hecate.my.id"
)

func TestParse_AcceptsHostsWithAndWithoutTime(t *testing.T) {
	data := []byte(hostA + "\n\n" + hostB + " 2026-10-21T08:30:00Z\n")

	entries, err := Parse(data)

	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 2 || entries[0].Host != hostA || entries[1].Host != hostB {
		t.Fatalf("entries = %+v", entries)
	}
	if !entries[0].Added.IsZero() {
		t.Errorf("a host without a time got Added = %v", entries[0].Added)
	}
	want := time.Date(2026, 10, 21, 8, 30, 0, 0, time.UTC)
	if !entries[1].Added.Equal(want) {
		t.Errorf("Added = %v, want %v", entries[1].Added, want)
	}
}

func TestParse_ToleratesWindowsLineEndings(t *testing.T) {
	entries, err := Parse([]byte(hostA + "\r\n" + hostB + "\r\n"))

	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 2 || entries[0].Host != hostA {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestParse_EmptyInputGivesNoEntries(t *testing.T) {
	entries, err := Parse([]byte("\n  \n"))

	if err != nil || len(entries) != 0 {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}
}

func TestParse_RejectsBadLinesWithoutEchoingThem(t *testing.T) {
	bad := map[string]string{
		"uppercase":      "AAAAAAAAAAAAAAAAAAAA.hecate.my.id",
		"wildcard":       "*.hecate.my.id",
		"single label":   "aaaaaaaaaaaaaaaaaaaa",
		"ip address":     "192.0.2.1",
		"trailing dot":   "aaaaaaaaaaaaaaaaaaaa.hecate.my.id.",
		"underscore":     "aaaa_aaaaaaaaaaaaaaa.hecate.my.id",
		"bad time":       hostA + " yesterday",
		"three fields":   hostA + " 2026-10-21T08:30:00Z extra",
		"leading hyphen": "-aaaaaaaaaaaaaaaaaaa.hecate.my.id",
		"control char":   "aaaaaaaaaaaaaaaaaaaa\x00.hecate.my.id",
		"url":            "https://aaaaaaaaaaaaaaaaaaaa.hecate.my.id",
	}
	for name, line := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(hostA + "\n" + line + "\n"))

			if err == nil {
				t.Fatalf("Parse accepted %q", line)
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("error %q does not name line 2", err)
			}
			if strings.Contains(err.Error(), "hecate.my.id") {
				t.Errorf("error %q echoes the host name", err)
			}
		})
	}
}

func TestParse_RejectsDuplicateHost(t *testing.T) {
	_, err := Parse([]byte(hostA + "\n" + hostA + " 2026-10-21T08:30:00Z\n"))

	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestParse_RejectsMoreThanMaxEntries(t *testing.T) {
	var b strings.Builder
	for i := range MaxEntries + 1 {
		b.WriteString(strings.Repeat(string(rune('a'+i)), 20))
		b.WriteString(".hecate.my.id\n")
	}

	_, err := Parse([]byte(b.String()))

	if !errors.Is(err, ErrTooMany) {
		t.Fatalf("err = %v, want ErrTooMany", err)
	}
}

func TestRead_MissingFileIsEmptyNotAnError(t *testing.T) {
	entries, stamp, err := Read(filepath.Join(t.TempDir(), "absent.txt"))

	if err != nil || len(entries) != 0 || stamp != (Stamp{}) {
		t.Fatalf("entries = %+v, stamp = %+v, err = %v", entries, stamp, err)
	}
}

func TestRead_RefusesFileOverMaxBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := Read(path)

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestRead_ReportsTheBadLineOfAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(path, []byte(hostA+"\nnot a host\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := Read(path)

	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestWrite_RoundTripsAndLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "names.txt")
	added := time.Date(2026, 10, 21, 8, 30, 0, 0, time.UTC)
	want := []Entry{{Host: hostA}, {Host: hostB, Added: added}}

	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, stamp, err := Read(path)

	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 || got[0].Host != hostA || !got[0].Added.IsZero() || got[1].Host != hostB || !got[1].Added.Equal(added) {
		t.Fatalf("got = %+v", got)
	}
	if stamp.Size == 0 || stamp.Time.IsZero() {
		t.Errorf("stamp = %+v", stamp)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".*"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestWrite_ReplacesTheWholeFileAndChangesTheStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := Write(path, []Entry{{Host: hostA}, {Host: hostB}}); err != nil {
		t.Fatal(err)
	}
	_, before, _ := Read(path)

	if err := Write(path, []Entry{{Host: hostB}}); err != nil {
		t.Fatal(err)
	}
	got, after, _ := Read(path)

	if len(got) != 1 || got[0].Host != hostB {
		t.Fatalf("got = %+v", got)
	}
	if after == before {
		t.Errorf("the stamp did not change: %+v", after)
	}
}

func TestWrite_RefusesInvalidEntriesAndKeepsTheOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := Write(path, []Entry{{Host: hostA}}); err != nil {
		t.Fatal(err)
	}

	err := Write(path, []Entry{{Host: "Not A Host"}})

	if err == nil {
		t.Fatal("Write accepted an invalid host")
	}
	got, _, _ := Read(path)
	if len(got) != 1 || got[0].Host != hostA {
		t.Errorf("the old file changed: %+v", got)
	}
}

func TestWrite_FailsWhenTheDirectoryIsMissing(t *testing.T) {
	err := Write(filepath.Join(t.TempDir(), "absent", "names.txt"), []Entry{{Host: hostA}})

	if err == nil {
		t.Fatal("Write created a missing directory")
	}
}

func TestNewLabel_MapsBytesToTheAlphabet(t *testing.T) {
	src := bytes.NewReader(append(bytes.Repeat([]byte{0}, 10), bytes.Repeat([]byte{35}, 10)...))

	label, err := NewLabel(src)

	if err != nil {
		t.Fatalf("NewLabel: %v", err)
	}
	if want := "aaaaaaaaaa9999999999"; label != want {
		t.Errorf("label = %q, want %q", label, want)
	}
}

func TestNewLabel_SkipsBytesThatWouldBiasTheResult(t *testing.T) {
	input := append(bytes.Repeat([]byte{252, 253, 254, 255}, 5), bytes.Repeat([]byte{1}, LabelLen)...)

	label, err := NewLabel(bytes.NewReader(input))

	if err != nil {
		t.Fatalf("NewLabel: %v", err)
	}
	if want := strings.Repeat("b", LabelLen); label != want {
		t.Errorf("label = %q, want %q", label, want)
	}
}

func TestNewLabel_FailsWhenTheRandomSourceRunsDry(t *testing.T) {
	_, err := NewLabel(bytes.NewReader([]byte{1, 2, 3}))

	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewLabel_DefaultSourceGivesDistinctValidLabels(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		label, err := NewLabel(nil)
		if err != nil {
			t.Fatalf("NewLabel: %v", err)
		}
		if len(label) != LabelLen || strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
			t.Fatalf("label %q is not %d characters of a-z0-9", label, LabelLen)
		}
		seen[label] = true
	}
	if len(seen) != 50 {
		t.Errorf("only %d distinct labels out of 50", len(seen))
	}
}

func TestHosts_ListsTheHostsInOrder(t *testing.T) {
	got := Hosts([]Entry{{Host: hostA}, {Host: hostB}})

	if len(got) != 2 || got[0] != hostA || got[1] != hostB {
		t.Fatalf("got = %v", got)
	}
}
