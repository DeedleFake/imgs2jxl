package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"deedles.dev/imgs2jxl"
)

func main() {
	cfg := imgs2jxl.DefaultConfig()
	skipSec := int(cfg.SkipNewerThan / time.Second)
	flag.StringVar(&cfg.Path, "path", cfg.Path, "directory of image files (default cwd)")
	flag.Func("effort", "cjxl -e, 1 to 10", func(s string) error {
		v, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		cfg.Effort = &v
		return nil
	})
	flag.Func("distance", "cjxl -d, 0 to 25", func(s string) error {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		cfg.Distance = &v
		return nil
	})
	flag.BoolVar(&cfg.Lossless, "lossless", false, "force -d 0")
	flag.IntVar(&cfg.Workers, "workers", cfg.Workers, "parallel encodes, 1 to 32")
	flag.Func("threads-per-worker", "cjxl --num_threads, 0 to 64", func(s string) error {
		v, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		cfg.ThreadsPerWorker = &v
		return nil
	})
	flag.BoolVar(&cfg.KeepOriginals, "keep-originals", false, "leave PNG after a verified JXL")
	flag.IntVar(&cfg.Limit, "limit", cfg.Limit, "max new encodes, 0 unlimited")
	flag.IntVar(&skipSec, "skip-newer-than-seconds", skipSec, "skip PNGs younger than this many seconds")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %s\n", strings.Join(flag.Args(), ", "))
		os.Exit(1)
	}
	cfg.SkipNewerThan = time.Duration(skipSec) * time.Second

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := imgs2jxl.Run(ctx, cfg); err != nil {
		var failed imgs2jxl.FailedError
		if errors.As(err, &failed) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
