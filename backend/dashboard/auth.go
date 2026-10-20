package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	keyLength           = 26
	keyAlphabet         = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	keySymbolMask       = 0x1f
	secretBytes         = 32
	maxKeyHashFileBytes = 1 << 10

	problemMissing    = "missing"
	problemUnreadable = "unreadable"
	problemEmpty      = "empty"
	problemMalformed  = "malformed"
)

var errInvalidKey = errors.New("dashboard: the access key is not valid")

func GenerateKey() (string, error) {
	raw := make([]byte, keyLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("dashboard: generate key: %w", err)
	}
	key := make([]byte, keyLength)
	for i, b := range raw {
		key[i] = keyAlphabet[b&keySymbolMask]
	}
	return string(key), nil
}

func NormalizeKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	out := make([]byte, 0, keyLength)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '-' {
			continue
		}
		c = canonicalSymbol(c)
		if strings.IndexByte(keyAlphabet, c) < 0 {
			return "", errInvalidKey
		}
		out = append(out, c)
	}
	if len(out) != keyLength {
		return "", errInvalidKey
	}
	return string(out), nil
}

func canonicalSymbol(c byte) byte {
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}
	switch c {
	case 'O':
		return '0'
	case 'I', 'L':
		return '1'
	}
	return c
}

func HashKey(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

type keyHashFile struct {
	path string
	log  *slog.Logger

	mu      sync.Mutex
	loaded  bool
	size    int64
	mtime   time.Time
	hash    string
	problem string
}

func newKeyHashFile(path string, log *slog.Logger) *keyHashFile {
	return &keyHashFile{path: path, log: log}
}

func (k *keyHashFile) current() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	info, err := os.Stat(k.path)
	if err != nil {
		k.loaded, k.hash = false, ""
		k.report(problemMissing)
		return ""
	}
	if k.loaded && info.Size() == k.size && info.ModTime().Equal(k.mtime) {
		return k.hash
	}
	hash, problem := readKeyHash(k.path, info.Size())
	k.loaded = problem != problemUnreadable
	k.size, k.mtime, k.hash = info.Size(), info.ModTime(), hash
	k.report(problem)
	return hash
}

func (k *keyHashFile) report(problem string) {
	if problem == k.problem {
		return
	}
	k.problem = problem
	if problem == "" {
		k.log.Info("dashboard key hash loaded", "path", k.path)
		return
	}
	k.log.Warn("dashboard key hash file is not usable, every login is refused", "problem", problem, "path", k.path)
}

func readKeyHash(path string, size int64) (string, string) {
	if size > maxKeyHashFileBytes {
		return "", problemMalformed
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", problemUnreadable
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", problemEmpty
	}
	sum, err := hex.DecodeString(text)
	if err != nil || len(sum) != sha256.Size {
		return "", problemMalformed
	}
	return hex.EncodeToString(sum), ""
}

func loadSecret(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("dashboard: the secret file is not set")
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) < secretBytes:
		return nil, fmt.Errorf("dashboard: secret file %s is shorter than %d bytes", path, secretBytes)
	case err == nil:
		return data, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("dashboard: read secret file: %w", err)
	}
	return createSecret(path)
}

func createSecret(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("dashboard: create secret directory: %w", err)
	}
	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("dashboard: generate secret: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("dashboard: create secret file: %w", err)
	}
	_, writeErr := f.Write(secret)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return nil, errors.Join(fmt.Errorf("dashboard: write secret file: %w", err), os.Remove(path))
	}
	return secret, nil
}
