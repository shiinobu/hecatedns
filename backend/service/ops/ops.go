package ops

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	Health     = "health"
	Backup     = "backup"
	RenewTest  = "renew-test"
	Renew      = "renew"
	RenewForce = "renew-force"

	StateIdle    = "idle"
	StateQueued  = "queued"
	StateRunning = "running"
	StateOK      = "ok"
	StateFailed  = "failed"

	maxOutputBytes = 64 << 10
	maxHeaderBytes = 4 << 10
	staleRunning   = 30 * time.Minute
	maxBackups     = 14
	maxBackupLines = 200
	backupsFile    = "backups.list"
	resultSuffix   = ".result"
	separator      = "---"
	truncatedNote  = "\n[output truncated]\n"
)

var (
	ErrUnknown  = errors.New("ops: unknown action")
	ErrDisabled = errors.New("ops: not available on this host")
	ErrBusy     = errors.New("ops: the action is already queued or running")
	ErrCooldown = errors.New("ops: the action ran too recently")
)

var order = []string{Health, Backup, RenewTest, Renew, RenewForce}

var cooldowns = map[string]time.Duration{
	Health:     10 * time.Second,
	Backup:     time.Minute,
	RenewTest:  time.Hour,
	Renew:      time.Hour,
	RenewForce: 48 * time.Hour,
}

var backupName = regexp.MustCompile(`^hecate-[0-9]{8}-[0-9]{6}\.tar\.gz$`)

type Options struct {
	RequestDir string
	ResultDir  string
	Now        func() time.Time
}

type Service struct {
	opts Options
}

type Status struct {
	Action          string
	State           string
	Started         time.Time
	Finished        time.Time
	Exit            int
	CooldownSeconds int
}

type BackupFile struct {
	Name  string
	Bytes int64
	Time  time.Time
}

func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{opts: o}
}

func Actions() []string { return slices.Clone(order) }

func (s *Service) Enabled() bool {
	return s != nil && s.opts.RequestDir != "" && s.opts.ResultDir != ""
}

func (s *Service) Run(action string) error {
	if !slices.Contains(order, action) {
		return ErrUnknown
	}
	if !s.Enabled() {
		return ErrDisabled
	}
	st := s.status(action)
	switch {
	case st.State == StateQueued || st.State == StateRunning:
		return ErrBusy
	case st.CooldownSeconds > 0:
		return ErrCooldown
	}
	f, err := os.OpenFile(filepath.Join(s.opts.RequestDir, action), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o660)
	if errors.Is(err, os.ErrExist) {
		return ErrBusy
	}
	if err != nil {
		return err
	}
	return f.Close()
}

func (s *Service) Status() []Status {
	if !s.Enabled() {
		return nil
	}
	out := make([]Status, len(order))
	for i, action := range order {
		out[i] = s.status(action)
	}
	return out
}

func (s *Service) Output(action string) (string, bool) {
	if !slices.Contains(order, action) || !s.Enabled() {
		return "", false
	}
	f, err := os.Open(s.resultPath(action))
	if err != nil {
		return "", true
	}
	defer f.Close()
	r := bufio.NewReader(f)
	if _, ok := readHeader(r); !ok {
		return "", true
	}
	body, _ := io.ReadAll(io.LimitReader(r, maxOutputBytes+1))
	if len(body) > maxOutputBytes {
		return string(body[:maxOutputBytes]) + truncatedNote, true
	}
	return string(body), true
}

func (s *Service) Backups() []BackupFile {
	if !s.Enabled() {
		return nil
	}
	f, err := os.Open(filepath.Join(s.opts.ResultDir, backupsFile))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []BackupFile
	seen := make(map[string]bool)
	sc := bufio.NewScanner(io.LimitReader(f, maxBackupLines*128))
	for lines := 0; sc.Scan() && lines < maxBackupLines; lines++ {
		bk, ok := parseBackup(sc.Text())
		if !ok || seen[bk.Name] {
			continue
		}
		seen[bk.Name] = true
		out = append(out, bk)
	}
	slices.SortStableFunc(out, func(a, b BackupFile) int { return b.Time.Compare(a.Time) })
	if len(out) > maxBackups {
		out = out[:maxBackups]
	}
	return out
}

func parseBackup(line string) (BackupFile, bool) {
	fields := strings.Fields(line)
	if len(fields) != 3 || !backupName.MatchString(fields[2]) {
		return BackupFile{}, false
	}
	epoch, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || epoch <= 0 {
		return BackupFile{}, false
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size < 0 {
		return BackupFile{}, false
	}
	return BackupFile{Name: fields[2], Bytes: size, Time: time.Unix(epoch, 0).UTC()}, true
}

func (s *Service) resultPath(action string) string {
	return filepath.Join(s.opts.ResultDir, action+resultSuffix)
}

func (s *Service) status(action string) Status {
	st := Status{Action: action, State: StateIdle}
	now := s.opts.Now()
	if info, err := os.Lstat(filepath.Join(s.opts.RequestDir, action)); err == nil && info.Mode().IsRegular() {
		st.State = StateQueued
	}
	h, ok := s.header(action)
	if !ok {
		return st
	}
	switch h["status"] {
	case "ok", "failed":
		st.Started, st.Finished = parseTime(h["started"]), parseTime(h["finished"])
		st.Exit, _ = strconv.Atoi(h["exit"])
		if st.State != StateQueued {
			st.State = StateOK
			if h["status"] == "failed" {
				st.State = StateFailed
			}
		}
		st.CooldownSeconds = remaining(cooldowns[action], st.Finished, now)
	case "running":
		st.Started = parseTime(h["started"])
		if !st.Started.IsZero() && now.Sub(st.Started) > staleRunning {
			if st.State != StateQueued {
				st.State = StateFailed
			}
			return st
		}
		if st.State != StateQueued {
			st.State = StateRunning
		}
	}
	return st
}

func (s *Service) header(action string) (map[string]string, bool) {
	f, err := os.Open(s.resultPath(action))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	return readHeader(bufio.NewReader(io.LimitReader(f, maxHeaderBytes)))
}

func readHeader(r *bufio.Reader) (map[string]string, bool) {
	h := make(map[string]string)
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == separator {
			return h, true
		}
		if key, value, found := strings.Cut(line, "="); found {
			h[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
		if err != nil {
			return h, len(h) > 0
		}
	}
}

func parseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func remaining(cooldown time.Duration, finished, now time.Time) int {
	if finished.IsZero() {
		return 0
	}
	left := cooldown - now.Sub(finished)
	if left <= 0 {
		return 0
	}
	return int((left + time.Second - 1) / time.Second)
}
