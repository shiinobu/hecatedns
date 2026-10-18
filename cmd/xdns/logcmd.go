package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"xdns/internal/config"
	"xdns/internal/qlog"
)

const (
	defaultLogSince  = "10m"
	defaultLogWindow = 30 * time.Second
)

// runLog implements `xdns log`: print the query log, newest part of it.
func runLog(args []string, stdout, stderr io.Writer) int {
	return runLogAt(args, stdout, stderr, time.Now())
}

// runLogAt is runLog with an explicit clock, so tests can fix "now". The local
// zone of now decides the day files and the printed times.
func runLogAt(args []string, stdout, stderr io.Writer, now time.Time) int {
	flags := flag.NewFlagSet("log", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path ke berkas konfigurasi YAML")
	since := flags.String("since", "", "tampilkan catatan sejak sekian lalu, mis. 10m, 2h, 3d (bawaan 10m)")
	grep := flags.String("grep", "", "hanya catatan yang memuat teks ini (nama, klien, daftar, sumber)")
	blocked := flags.Bool("blocked", false, "hanya query yang diblokir")
	around := flags.String("around", "", "tampilkan sekitar jam JJ:MM, mis. 14:32")
	window := flags.Duration("window", defaultLogWindow, "setengah lebar jendela untuk -around")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "xdns log: argumen tak terduga %q\n", flags.Args())
		return exitUsage
	}
	filter, err := buildLogFilter(now, *since, *around, *window)
	if err != nil {
		fmt.Fprintf(stderr, "xdns log: %v\n", err)
		return exitUsage
	}
	filter.Grep, filter.BlockedOnly = *grep, *blocked

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "xdns log: %v\n", err)
		return exitError
	}
	return printLog(cfg, filter, stdout, stderr)
}

// buildLogFilter turns -since or -around into a time window. They exclude each
// other; with neither, the last defaultLogSince is shown.
func buildLogFilter(now time.Time, since, around string, window time.Duration) (qlog.Filter, error) {
	filter := qlog.Filter{Location: now.Location()}
	switch {
	case since != "" && around != "":
		return filter, errors.New("-since dan -around tidak bisa dipakai bersamaan")
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
		fmt.Fprintln(stderr, "catatan: log dimatikan (log.enabled: false); hanya berkas lama yang ditampilkan")
	}
	stats, err := qlog.Read(cfg.Log.Dir, filter, func(rec qlog.Record) {
		fmt.Fprintln(stdout, rec.Line(filter.Location))
	})
	if err != nil {
		fmt.Fprintf(stderr, "xdns log: %v\n", err)
		return exitError
	}
	if stats.Matched == 0 {
		fmt.Fprintf(stderr, "tidak ada catatan yang cocok di %s\n", cfg.Log.Dir)
	} else {
		fmt.Fprintf(stderr, "%d catatan\n", stats.Matched)
	}
	if stats.Malformed > 0 {
		fmt.Fprintf(stderr, "%d baris rusak dilewati\n", stats.Malformed)
	}
	return exitOK
}
