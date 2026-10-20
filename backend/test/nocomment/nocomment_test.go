package nocomment

import (
	"bufio"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var skippedDirs = map[string]bool{
	".git":    true,
	".agents": true,
	".claude": true,
	"bin":     true,
	"data":    true,
}

func walk(t *testing.T, suffix string, check func(path string)) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, suffix) {
			check(path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGoSourceHasNoComments(t *testing.T) {
	walk(t, ".go", func(path string) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return
		}
		for _, group := range file.Comments {
			for _, c := range group.List {
				if strings.HasPrefix(c.Text, "//go:") {
					continue
				}
				t.Errorf("%s:%d: comment", path, fset.Position(c.Pos()).Line)
			}
		}
	})
}

func TestScanners_FindCommentsAndIgnoreLookalikes(t *testing.T) {
	cases := []struct {
		name  string
		scan  func(string) int
		input string
		line  int
	}{
		{"js line comment", jsCommentLine, "let a = 1;\nlet b = 2; // note\n", 2},
		{"js block comment", jsCommentLine, "/* x */\nlet a;\n", 1},
		{"js url in a string", jsCommentLine, "const u = \"http://x.example\";\n", 0},
		{"js url in a template", jsCommentLine, "const u = `a//b`;\n", 0},
		{"js escaped quote", jsCommentLine, "const u = 'it\\'s // fine';\n", 0},
		{"js regex with escaped slash", jsCommentLine, "s.replace(/^#\\/?/, \"\");\n", 0},
		{"css block comment", cssCommentLine, "a { color: red; }\n/* c */\n", 2},
		{"css clean", cssCommentLine, "a { color: red; }\n", 0},
		{"html comment", htmlCommentLine, "<p>x</p>\n<!-- c -->\n", 2},
		{"html clean", htmlCommentLine, "<p>x</p>\n", 0},
	}
	for _, tc := range cases {
		if got := tc.scan(tc.input); got != tc.line {
			t.Errorf("%s: line = %d, want %d", tc.name, got, tc.line)
		}
	}
}

func TestFrontendHasNoComments(t *testing.T) {
	scanners := map[string]func(string) int{".js": jsCommentLine, ".css": cssCommentLine, ".html": htmlCommentLine}
	root := filepath.Join("..", "..", "..", "frontend")
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		scan, ok := scanners[filepath.Ext(path)]
		if !ok {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		if line := scan(string(data)); line > 0 {
			t.Errorf("%s:%d: comment", path, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no front-end file was checked")
	}
}

func jsCommentLine(src string) int {
	line, quote := 1, byte(0)
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			line++
		}
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*'):
			return line
		}
	}
	return 0
}

func cssCommentLine(src string) int {
	return lineOf(src, "/*")
}

func htmlCommentLine(src string) int {
	return lineOf(src, "<!--")
}

func lineOf(src, marker string) int {
	before, _, found := strings.Cut(src, marker)
	if !found {
		return 0
	}
	return 1 + strings.Count(before, "\n")
}

func TestPowerShellHasNoComments(t *testing.T) {
	walk(t, ".ps1", func(path string) {
		f, err := os.Open(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for n := 1; scanner.Scan(); n++ {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "#") || strings.Contains(line, "<#") {
				t.Errorf("%s:%d: comment", path, n)
			}
		}
		if err := scanner.Err(); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	})
}
