package dotnames

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func numberedHosts(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(strings.Repeat(string(rune('a'+i)), 20))
		b.WriteString(".hecate.my.id\n")
	}
	return b.String()
}

func TestParse_IgnoresCommentLines(t *testing.T) {
	data := []byte("# names for the phones\n" + hostA + "\n   # an indented note with " + hostB + "\n\t#tabbed\n")

	entries, err := Parse(data)

	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := Hosts(entries); !reflect.DeepEqual(got, []string{hostA}) {
		t.Errorf("hosts = %v, want only %s", got, hostA)
	}
}

func TestParse_AHashAfterTheHostIsStillAnError(t *testing.T) {
	_, err := Parse([]byte(hostA + " # note\n"))

	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("err = %v, want line 1 rejected", err)
	}
}

func TestParseLenient_KeepsTheValidLinesAndNumbersTheRest(t *testing.T) {
	data := []byte(hostA + "\nNOT.A.HOST\n" + hostA + "\n# comment\n\n" + hostB + " 2026-10-21T08:30:00Z\n" + hostB + "x extra junk\n")

	entries, rejected := ParseLenient(data)

	if got := Hosts(entries); !reflect.DeepEqual(got, []string{hostA, hostB}) {
		t.Errorf("hosts = %v, want %s and %s", got, hostA, hostB)
	}
	if !reflect.DeepEqual(rejected, []int{2, 3, 7}) {
		t.Errorf("rejected lines = %v, want [2 3 7]", rejected)
	}
}

func TestParseLenient_RejectsTheLinesBeyondMaxEntries(t *testing.T) {
	entries, rejected := ParseLenient([]byte(numberedHosts(MaxEntries + 2)))

	if len(entries) != MaxEntries {
		t.Errorf("entries = %d, want %d", len(entries), MaxEntries)
	}
	if !reflect.DeepEqual(rejected, []int{MaxEntries + 1, MaxEntries + 2}) {
		t.Errorf("rejected lines = %v, want the two lines past the limit", rejected)
	}
}

func TestParseLenient_AValidFileRejectsNothing(t *testing.T) {
	entries, rejected := ParseLenient([]byte(numberedHosts(MaxEntries)))

	if len(entries) != MaxEntries || len(rejected) != 0 {
		t.Errorf("entries = %d, rejected = %v; want %d and none", len(entries), rejected, MaxEntries)
	}
}

func TestReadLenient_ReturnsTheValidEntriesOfAFileWithATypo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(path, []byte(hostA+"\nhecate typo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, rejected, stamp, err := ReadLenient(path)

	if err != nil || len(entries) != 1 || entries[0].Host != hostA {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}
	if !reflect.DeepEqual(rejected, []int{2}) || stamp.Size == 0 {
		t.Errorf("rejected = %v, stamp = %+v", rejected, stamp)
	}
}

func TestReadLenient_MissingFileIsEmptyNotAnError(t *testing.T) {
	entries, rejected, stamp, err := ReadLenient(filepath.Join(t.TempDir(), "absent.txt"))

	if err != nil || len(entries) != 0 || len(rejected) != 0 || stamp != (Stamp{}) {
		t.Fatalf("entries = %+v, rejected = %v, stamp = %+v, err = %v", entries, rejected, stamp, err)
	}
}

func TestReadLenient_StillRefusesAFileOverMaxBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, stamp, err := ReadLenient(path)

	if !errors.Is(err, ErrTooLarge) || stamp.Size != MaxBytes+1 {
		t.Fatalf("err = %v, stamp = %+v; want ErrTooLarge with the file stamp", err, stamp)
	}
}
