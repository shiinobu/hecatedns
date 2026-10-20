package frontend

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

var localReference = regexp.MustCompile(`(?:src|href)="([^"#]+)"`)

func TestFiles_TheIndexLinksAnIconOfEachKind(t *testing.T) {
	index, err := fs.ReadFile(Files, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`rel="icon" type="image/svg+xml"`, `rel="icon" type="image/png"`, `rel="apple-touch-icon"`} {
		if !strings.Contains(string(index), want) {
			t.Errorf("index.html has no link with %s", want)
		}
	}
}

func TestFiles_TheThemeTurnsOffTheBrowserTapHighlight(t *testing.T) {
	theme, err := fs.ReadFile(Files, "theme.css")
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)\nbody\s*\{[^}]*\}`).FindString(string(theme))
	if !strings.Contains(body, "-webkit-tap-highlight-color: transparent") {
		t.Errorf("the body rule of theme.css does not turn off the tap highlight, so every tap draws the browser's coloured box:\n%s", body)
	}
}

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

func TestFiles_HoldOnlyThePageStylesScriptsAndIcons(t *testing.T) {
	err := fs.WalkDir(Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch path.Ext(name) {
		case ".html", ".css", ".js", ".svg", ".png":
		default:
			t.Errorf("%s is embedded but is not a page, stylesheet, script or icon", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var (
	fontSizeValue   = regexp.MustCompile(`font-size:\s*([^;]+);`)
	textTokenUse    = regexp.MustCompile(`^var\((--text-[a-z0-9]+)\)$`)
	textTokenDefine = regexp.MustCompile(`(--text-[a-z0-9]+):`)
)

func TestFiles_EveryFontSizeOutsideTheThemeIsADefinedTextToken(t *testing.T) {
	theme, err := fs.ReadFile(Files, "theme.css")
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	for _, m := range textTokenDefine.FindAllStringSubmatch(string(theme), -1) {
		defined[m[1]] = true
	}
	err = fs.WalkDir(Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(name) != ".css" || name == "theme.css" {
			return err
		}
		data, err := fs.ReadFile(Files, name)
		if err != nil {
			return err
		}
		for _, m := range fontSizeValue.FindAllStringSubmatch(string(data), -1) {
			use := textTokenUse.FindStringSubmatch(strings.TrimSpace(m[1]))
			if use == nil || !defined[use[1]] {
				t.Errorf("%s: font-size %q is not a text token defined in theme.css", name, strings.TrimSpace(m[1]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
