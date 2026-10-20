package controller

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	probeHeartbeat = 10 * time.Minute
	minProbeWatch  = 5 * time.Second
	defaultDNSPort = "53"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

func runProbe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "read the newest served DoT name from this configuration file")
	nameFile := flags.String("name-file", "", "read the name from this file (the last entry of a names file or a single line)")
	addr := flags.String("addr", "", "host:port to connect to (default: the addresses found, on -port)")
	port := flags.String("port", probePort, "DoT port to connect to on the addresses found")
	resolvers := flags.String("resolvers", strings.Join(defaultProbeResolvers, ","), "public resolvers to compare with the system resolver, comma separated")
	canary := flags.String("canary", defaultProbeCanary, "name to ask over the DoT connection")
	timeout := flags.Duration("timeout", defaultProbeTimeout, "time allowed for each stage")
	watch := flags.Duration("watch", 0, "repeat every interval (at least "+minProbeWatch.String()+") until Ctrl+C and print only changes and a heartbeat")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 || (*configPath == "") == (*nameFile == "") {
		fmt.Fprintln(stderr, "hecate probe: give exactly one of -config <file> and -name-file <file>")
		return exitUsage
	}
	if *watch < 0 || (*watch > 0 && *watch < minProbeWatch) {
		fmt.Fprintf(stderr, "hecate probe: -watch must be at least %s (or 0 to run once)\n", minProbeWatch)
		return exitUsage
	}
	name, err := probeNameFrom(*configPath, *nameFile)
	if err != nil {
		fmt.Fprintf(stderr, "hecate probe: %v\n", err)
		return exitError
	}
	opts := probeOptions{Name: name, Addr: *addr, Port: *port, Resolvers: normalizeResolvers(*resolvers), Canary: *canary, Timeout: *timeout}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *watch > 0 {
		watchProbe(ctx, opts, *watch, probeHeartbeat, stdout)
		return exitOK
	}
	stages := probe(ctx, opts)
	writeProbe(stdout, stages)
	if probePassed(stages) {
		return exitOK
	}
	return exitError
}

func probeNameFrom(configPath, nameFile string) (string, error) {
	if nameFile != "" {
		return readProbeName(nameFile)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	names, err := configuredDoTNames(cfg)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fmt.Errorf("%s serves no DoT name", configPath)
	}
	return names[len(names)-1], nil
}

func configuredDoTNames(cfg config.Config) ([]string, error) {
	names := slices.Clone(cfg.DoT.Names)
	if cfg.DoT.NamesFile == "" {
		return names, nil
	}
	entries, _, _, err := dotnames.ReadLenient(cfg.DoT.NamesFile)
	if errors.Is(err, fs.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the names file: %w", err)
	}
	return append(names, dotnames.Hosts(entries)...), nil
}

func readProbeName(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, dotnames.MaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > dotnames.MaxBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", path, dotnames.MaxBytes)
	}
	name := ""
	for line := range strings.Lines(string(bytes.TrimPrefix(data, utf8BOM))) {
		if fields := strings.Fields(line); len(fields) > 0 && !strings.HasPrefix(fields[0], "#") {
			name = fields[0]
		}
	}
	if !config.ValidHostName(name) {
		return "", fmt.Errorf("%s holds no valid host name (save it as plain UTF-8 or ASCII text)", path)
	}
	return name, nil
}

func normalizeResolvers(text string) []string {
	var out []string
	for part := range strings.SplitSeq(text, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, withPort(part, defaultDNSPort))
		}
	}
	return out
}

func withPort(address, port string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.Trim(address, "[]"), port)
}

func writeProbe(w io.Writer, stages []probeStage) {
	fmt.Fprintf(w, "probing %s\n", probeRedacted)
	for _, s := range stages {
		status := "PASS"
		if !s.OK {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%s  %-30s %8s  %s\n", status, s.Stage, s.Took.Round(time.Millisecond), s.Detail)
	}
	fmt.Fprintf(w, "result: %s\n", probeVerdict(stages))
}

func probeState(stages []probeStage) string {
	if probePassed(stages) {
		return "ok"
	}
	var failing []string
	for _, s := range stages {
		if !s.OK {
			failing = append(failing, s.Stage)
		}
	}
	return "failing: " + strings.Join(failing, ", ")
}

func watchProbe(ctx context.Context, opts probeOptions, interval, heartbeat time.Duration, out io.Writer) {
	var last string
	var printed time.Time
	for {
		stages := probe(ctx, opts)
		if ctx.Err() != nil {
			return
		}
		state, now := probeState(stages), time.Now()
		switch {
		case state != last:
			fmt.Fprintln(out, now.Format(time.RFC3339))
			writeProbe(out, stages)
			printed = now
		case now.Sub(printed) >= heartbeat:
			fmt.Fprintf(out, "%s still %s\n", now.Format("15:04:05"), state)
			printed = now
		}
		last = state
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
