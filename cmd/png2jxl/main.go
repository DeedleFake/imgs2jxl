package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"png2jxl"
)

func main() {
	cfg := png2jxl.DefaultConfig()
	skipSec := int(cfg.SkipNewerThan / time.Second)
	flag.StringVar(&cfg.Path, "path", cfg.Path, "directory of PNG files (default cwd)")
	flag.IntVar(&cfg.Effort, "effort", cfg.Effort, "cjxl -e, 1 to 10")
	flag.Float64Var(&cfg.Distance, "distance", cfg.Distance, "cjxl -d, 0 to 25")
	flag.BoolVar(&cfg.Lossless, "lossless", false, "force -d 0")
	flag.IntVar(&cfg.Workers, "workers", cfg.Workers, "parallel encodes, 1 to 32")
	flag.IntVar(&cfg.ThreadsPerWorker, "threads-per-worker", cfg.ThreadsPerWorker, "cjxl --num_threads, 0 to 64")
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
	if err := png2jxl.Run(ctx, cfg); err != nil {
		var failed png2jxl.FailedError
		if errors.As(err, &failed) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
