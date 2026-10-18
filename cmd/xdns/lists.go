package main

// agy wrote this

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"xdns/internal/config"
	"xdns/internal/lists"
)

const listsUsage = `penggunaan:
  xdns lists status [-config berkas]   tampilkan status cache daftar blokir
  xdns lists update [-config berkas]   unduh ulang semua daftar blokir aktif
`

// runLists implements `xdns lists status` and `xdns lists update`.
func runLists(args []string, stdout, stderr io.Writer) int {
	return runListsWith(context.Background(), args, stdout, stderr, nil)
}

func runListsWith(ctx context.Context, args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	configPath, rest, err := extractConfigFlag(args)
	if err != nil {
		fmt.Fprintf(stderr, "xdns lists: %v\n", err)
		return exitUsage
	}
	if len(rest) != 1 || (rest[0] != "status" && rest[0] != "update") {
		fmt.Fprint(stderr, listsUsage)
		return exitUsage
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "xdns lists: %v\n", err)
		return exitError
	}
	if rest[0] == "status" {
		return listsStatus(cfg, stdout)
	}
	return listsUpdate(ctx, cfg, stdout, stderr, httpClient)
}

func listsStatus(cfg config.Config, stdout io.Writer) int {
	if len(cfg.Lists.Sources) == 0 {
		fmt.Fprintln(stdout, "tidak ada sumber daftar yang dikonfigurasi")
		return exitOK
	}
	dir := lists.Dir(cfg.DataDir)
	for _, src := range cfg.Lists.Sources {
		if !src.Enabled {
			fmt.Fprintf(stdout, "%s (%s): nonaktif\n", src.Name, src.Kind)
			continue
		}
		_, meta, err := lists.LoadCached(dir, src)
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(stdout, "%s (%s): belum diunduh (jalankan 'xdns lists update')\n", src.Name, src.Kind)
		case err != nil:
			fmt.Fprintf(stdout, "%s (%s): rusak (%v)\n", src.Name, src.Kind, err)
		default:
			ts := "waktu tidak diketahui"
			if !meta.FetchedAt.IsZero() {
				ts = meta.FetchedAt.Local().Format(time.RFC3339)
			}
			fmt.Fprintf(stdout, "%s (%s): %d domain, ditolak %d, diperbarui %s\n",
				src.Name, src.Kind, meta.Count, meta.Rejected, ts)
		}
	}
	return exitOK
}

func listsUpdate(ctx context.Context, cfg config.Config, stdout, stderr io.Writer, httpClient *http.Client) int {
	fetcher := &lists.Fetcher{
		Dir:       lists.Dir(cfg.DataDir),
		Bootstrap: cfg.Bootstrap,
		Client:    httpClient,
	}
	failed := false
	for _, src := range cfg.Lists.Sources {
		if !src.Enabled {
			continue
		}
		res, err := fetcher.Fetch(ctx, src)
		if err != nil {
			fmt.Fprintf(stderr, "xdns lists update: %s: %v\n", src.Name, err)
			failed = true
			continue
		}
		if res.NotModified {
			fmt.Fprintf(stdout, "%s: belum berubah (%d domain)\n", src.Name, res.Meta.Count)
			continue
		}
		fmt.Fprintf(stdout, "%s: diperbarui, %d domain (ditolak %d)\n",
			src.Name, res.Meta.Count, res.Meta.Rejected)
	}
	if failed {
		return exitError
	}
	return exitOK
}
