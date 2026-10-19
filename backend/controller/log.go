package controller

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/service/qlog"
)

const (
	defaultLogSince  = "10m"
	defaultLogWindow = 30 * time.Second
)

func runLog(args []string, stdout, stderr io.Writer) int {
	return runLogAt(args, stdout, stderr, time.Now())
}

func runLogAt(args []string, stdout, stderr io.Writer, now time.Time) int {
	flags := flag.NewFlagSet("log", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path to the YAML configuration file")
	since := flags.String("since", "", "show records from this long ago, e.g. 10m, 2h, 3d (default 10m)")
	grep := flags.String("grep", "", "only records containing this text (name, client, list, source)")
	blocked := flags.Bool("blocked", false, "only blocked queries")
	around := flags.String("around", "", "show records around HH:MM, e.g. 14:32")
	window := flags.Duration("window", defaultLogWindow, "half the window width for -around")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "hecate log: argumen tak terduga %q\n", flags.Args())
		return exitUsage
	}
	filter, err := buildLogFilter(now, *since, *around, *window)
	if err != nil {
		fmt.Fprintf(stderr, "hecate log: %v\n", err)
		return exitUsage
	}
	filter.Grep, filter.BlockedOnly = *grep, *blocked

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "hecate log: %v\n", err)
		return exitError
	}
	return printLog(cfg, filter, stdout, stderr)
}

func buildLogFilter(now time.Time, since, around string, window time.Duration) (qlog.Filter, error) {
	filter := qlog.Filter{Location: now.Location()}
	switch {
	case since != "" && around != "":
		return filter, errors.New("-since and -around cannot be used together")
	case around != "":
		from, until, err := qlog.ParseAround(now, around, window)
		if err != nil {
			return filter, err
		}
		filter.Since, filter.Until = from, until
	default:
		if since == "" {
			since = defaultLogSince
		}
		from, err := qlog.ParseSince(now, since)
		if err != nil {
			return filter, err
		}
		filter.Since = from
	}
	return filter, nil
}

func printLog(cfg config.Config, filter qlog.Filter, stdout, stderr io.Writer) int {
	if !cfg.Log.Enabled {
		fmt.Fprintln(stderr, "note: logging is off (log.enabled: false); only old files are shown")
	}
	stats, err := qlog.Read(cfg.Log.Dir, filter, func(rec qlog.Record) {
		fmt.Fprintln(stdout, rec.Line(filter.Location))
	})
	if err != nil {
		fmt.Fprintf(stderr, "hecate log: %v\n", err)
		return exitError
	}
	if stats.Matched == 0 {
		fmt.Fprintf(stderr, "no matching records in %s\n", cfg.Log.Dir)
	} else {
		fmt.Fprintf(stderr, "%d records\n", stats.Matched)
	}
	if stats.Malformed > 0 {
		fmt.Fprintf(stderr, "malformed lines skipped: %d\n", stats.Malformed)
	}
	return exitOK
}
