package frontend

import (
	"io/fs"
	"path"
	"regexp"
	"testing"
)

var localReference = regexp.MustCompile(`(?:src|href)="([^"#]+)"`)

func TestFiles_HoldTheIndexAndEveryFileItLinks(t *testing.T) {
	index, err := fs.ReadFile(Files, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := localReference.FindAllStringSubmatch(string(index), -1)
	if len(refs) == 0 {
		t.Fatal("index.html links no stylesheet or script")
	}
	for _, m := range refs {
		if _, err := fs.Stat(Files, m[1]); err != nil {
			t.Errorf("index.html links %q, which is not embedded: %v", m[1], err)
		}
	}
}

func TestFiles_HoldOnlyThePageStylesAndScripts(t *testing.T) {
	err := fs.WalkDir(Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch path.Ext(name) {
		case ".html", ".css", ".js":
		default:
			t.Errorf("%s is embedded but is not a page, stylesheet or script", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
