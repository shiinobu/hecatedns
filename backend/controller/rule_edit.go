package controller

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
	rulesFileMode = 0o600
	rulesDirMode  = 0o700

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
		return rulesText{}, fmt.Errorf("baca %s: %w", path, err)
	}
	if info.IsDir() {
		return rulesText{}, fmt.Errorf("%s is a directory, not a rules file", path)
	}
	if info.Size() > engine.MaxRulesFileBytes {
		return rulesText{}, fmt.Errorf("%s is larger than %d bytes, the server ignores the rest; shrink it first", path, engine.MaxRulesFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rulesText{}, fmt.Errorf("baca %s: %w", path, err)
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

func (t *rulesText) remove(domain string) int {
	kept := t.lines[:0:0]
	for i, line := range t.lines {
		candidate := line
		if i == 0 {
			candidate = strings.TrimPrefix(candidate, byteOrderMark)
		}
		if rule, ok, _ := parseLine(candidate); ok && rule.Domain == domain {
			continue
		}
		kept = append(kept, line)
	}
	removed := len(t.lines) - len(kept)
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
		return fmt.Errorf("buat direktori %s: %w", dir, err)
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
		return fmt.Errorf("tulis %s: %w", tmp.Name(), err)
	}
	return renameWithRetry(tmp.Name(), path)
}

func renameWithRetry(from, to string) (err error) {
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(renameWait)
	}
	return fmt.Errorf("ganti %s: %w", to, err)
}
