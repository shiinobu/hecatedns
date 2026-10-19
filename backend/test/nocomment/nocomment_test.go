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
