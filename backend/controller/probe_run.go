package controller

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const probeHeartbeat = 10 * time.Minute

func runProbe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "read the newest served DoT name from this configuration file")
	nameFile := flags.String("name-file", "", "read the name from this file (the last entry of a names file or a single line)")
	addr := flags.String("addr", "", "host:port to connect to (default: the first address found, port "+probePort+")")
	resolvers := flags.String("resolvers", strings.Join(defaultProbeResolvers, ","), "public resolvers to compare with the system resolver, comma separated")
	canary := flags.String("canary", defaultProbeCanary, "name to ask over the DoT connection")
	timeout := flags.Duration("timeout", defaultProbeTimeout, "time allowed for each stage")
	watch := flags.Duration("watch", 0, "repeat every interval until Ctrl+C and print only changes and a heartbeat")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 || (*configPath == "") == (*nameFile == "") {
		fmt.Fprintln(stderr, "hecate probe: give exactly one of -config <file> and -name-file <file>")
		return exitUsage
	}
	name, err := probeNameFrom(*configPath, *nameFile)
	if err != nil {
		fmt.Fprintf(stderr, "hecate probe: %v\n", err)
		return exitError
	}
	opts := probeOptions{Name: name, Addr: *addr, Resolvers: splitList(*resolvers), Canary: *canary, Timeout: *timeout}
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
	names := servedDoTNames(cfg)
	if len(names) == 0 {
		return "", fmt.Errorf("%s serves no DoT name", configPath)
	}
	return names[len(names)-1], nil
}

func readProbeName(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	name := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 && !strings.HasPrefix(fields[0], "#") {
			name = fields[0]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if !config.ValidHostName(name) {
		return "", fmt.Errorf("%s holds no valid host name", path)
	}
	return name, nil
}

func splitList(text string) []string {
	var out []string
	for part := range strings.SplitSeq(text, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
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
