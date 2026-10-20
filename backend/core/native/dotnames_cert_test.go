package native

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const uncoveredFileName = "q1w2e3r4t5y6u7i8.other.example"

func reissue(t *testing.T, env *dotEnv, serial int64, dnsNames ...string) {
	t.Helper()
	certPEM, keyPEM := env.pki.issue(t, serial, dnsNames, time.Now().Add(60*24*time.Hour))
	writePair(t, tempDirOf(env.certPath), certPEM, keyPEM)
}

func TestDoTNames_RefusesACertificateThatLosesTheCoverageOfAFileName(t *testing.T) {
	env, _ := startNamesEnv(t, []string{dotName}, namesLines(fileDoTName), nil)

	reissue(t, env, 2, dotName)

	eventually(t, namesTestWait, "a refusal of the narrower certificate", func() bool {
		return env.logs.has(slog.LevelError, "DoT certificate could not be reloaded")
	})
	if got := env.servedSerial(t); got != 1 {
		t.Errorf("served certificate serial = %d, want the old certificate (1) to stay in use", got)
	}
	if !env.accepts(t, fileDoTName) {
		t.Error("the file name stopped working")
	}
	assertNoSecretsLogged(t, env.logs)
}

func TestDoTNames_AcceptsACertificateThatNeverCoveredTheFileName(t *testing.T) {
	env, _ := startNamesEnv(t, []string{dotName}, namesLines(uncoveredFileName), nil)

	reissue(t, env, 2, dotBase, "*."+dotBase)

	eventually(t, namesTestWait, "the renewed certificate to be served", func() bool { return env.servedSerial(t) == 2 })
	if env.logs.anywhere(uncoveredFileName) {
		t.Error("the uncovered name was written to the log")
	}
}

func TestDoTNames_WarnsAgainAboutUncoveredNamesAfterACertificateReload(t *testing.T) {
	env, _ := startNamesEnv(t, []string{dotName}, namesLines(fileDoTName, uncoveredFileName), nil)
	before := env.logs.count(slog.LevelWarn, "uncovered=1")

	reissue(t, env, 2, dotBase, "*."+dotBase)

	eventually(t, namesTestWait, "a new warning after the reload", func() bool {
		return env.servedSerial(t) == 2 && env.logs.count(slog.LevelWarn, "uncovered=1") > before
	})
}

func TestDoTNames_StartupServesTheValidLinesOfAFileWithATypo(t *testing.T) {
	typo := secretLabel + "." + dotBase
	env, _ := startNamesEnv(t, nil, namesLines(fileDoTName, typo), nil)

	if !env.accepts(t, fileDoTName) {
		t.Fatal("the valid file name is refused")
	}
	time.Sleep(10 * namesTestPoll)
	if n := env.logs.count(slog.LevelError, "unusable lines"); n != 1 {
		t.Errorf("errors about unusable lines = %d, want exactly 1", n)
	}
	assertNoSecretsLogged(t, env.logs)
}

func TestDoTNames_CommentLinesInTheFileAreHarmless(t *testing.T) {
	seed := []byte("# the phone\n" + fileDoTName + "\n")
	env, _ := startNamesEnv(t, []string{dotName}, seed, nil)

	if !env.accepts(t, fileDoTName) {
		t.Error("the file name after a comment line is refused")
	}
	time.Sleep(10 * namesTestPoll)
	if n := env.logs.count(slog.LevelError, "names file"); n != 0 {
		t.Errorf("errors about the names file = %d, want none", n)
	}
}

func TestDoTNames_CheckDoTRejectsFileNamesTheCertificateDoesNotCover(t *testing.T) {
	pki := newTestPKI(t)
	certPEM, keyPEM := pki.issue(t, 1, []string{dotBase, "*." + dotBase}, time.Now().Add(48*time.Hour))
	certPath, keyPath := writePair(t, t.TempDir(), certPEM, keyPEM)
	cfg := dotConfigFor(certPath, keyPath, "127.0.0.1:53")
	path := filepath.Join(t.TempDir(), "dot-names.txt")
	if err := os.WriteFile(path, namesLines(uncoveredFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	namesConfig([]string{dotName}, path)(&cfg)

	_, err := CheckDoT(cfg)

	if err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("CheckDoT = %v, want an error about names the certificate does not cover", err)
	}
	if strings.Contains(err.Error(), uncoveredFileName) {
		t.Errorf("the error repeats the name: %v", err)
	}
}
