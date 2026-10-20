package ruleedit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/backend/engine"
)

const (
	rulesDirMode = 0o700

	renameAttempts = 5
	renameWait     = 40 * time.Millisecond

	byteOrderMark = "\xef\xbb\xbf"
	lf            = "\n"
	crlf          = "\r\n"
)

type rulesText struct {
	lines []string
	eol   string
}

func readRulesText(path string) (rulesText, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return rulesText{eol: lf}, nil
	}
	if err != nil {
		return rulesText{}, fmt.Errorf("read %s: %w", path, err)
	}
	if info.IsDir() {
		return rulesText{}, fmt.Errorf("%s is a directory, not a rules file", path)
	}
	if info.Size() > engine.MaxRulesFileBytes {
		return rulesText{}, fmt.Errorf("%w: %s is larger than %d bytes, the server ignores the rest; shrink it first", ErrFileFull, path, engine.MaxRulesFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rulesText{}, fmt.Errorf("read %s: %w", path, err)
	}
	return parseRulesText(string(data)), nil
}

func parseRulesText(text string) rulesText {
	t := rulesText{eol: lf}
	if strings.Contains(text, crlf) {
		t.eol = crlf
	}
	text = strings.ReplaceAll(text, crlf, lf)
	if text = strings.TrimSuffix(text, lf); text != "" {
		t.lines = strings.Split(text, lf)
	}
	return t
}

func parseLine(line string) (engine.Rule, bool, error) {
	if len(line) >= engine.MaxRuleLineBytes {
		return engine.Rule{}, false, engine.ErrLineTooLong
	}
	return engine.ParseRuleLine(line)
}

func lineForParsing(index int, line string) string {
	if index == 0 {
		return strings.TrimPrefix(line, byteOrderMark)
	}
	return line
}

func (t *rulesText) remove(domain string) []engine.Rule {
	kept := t.lines[:0:0]
	var removed []engine.Rule
	for i, line := range t.lines {
		if rule, ok, _ := parseLine(lineForParsing(i, line)); ok && rule.Domain == domain {
			removed = append(removed, rule)
			continue
		}
		kept = append(kept, line)
	}
	t.lines = kept
	return removed
}

func (t *rulesText) add(rule engine.Rule) {
	t.lines = append(t.lines, rule.String())
}

func (t rulesText) bytes() []byte {
	if len(t.lines) == 0 {
		return nil
	}
	return []byte(strings.Join(t.lines, t.eol) + t.eol)
}

func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, rulesDirMode); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".rules-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	return renameWithRetry(tmp.Name(), path)
}

func renameWithRetry(from, to string) (err error) {
	for range renameAttempts {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(renameWait)
	}
	return fmt.Errorf("replace %s: %w", to, err)
}
