package png2jxl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logName             = "convert-png-to-jxl.log"
	minValidJXLSize     = 32
	maxEffort           = 10
	minEffort           = 1
	maxWorkers          = 32
	minWorkers          = 1
	maxThreadsPerWorker = 64
	maxDistance         = 25.0
)

// Config is the public input to Run. Numeric ranges are enforced by validate.
type Config struct {
	Path             string
	Effort           int
	Distance         float64
	Lossless         bool
	Workers          int
	ThreadsPerWorker int
	KeepOriginals    bool
	Limit            int
	SkipNewerThan    time.Duration
}

func DefaultConfig() Config {
	return Config{
		Effort:           7,
		Distance:         1.0,
		Workers:          8,
		ThreadsPerWorker: 3,
		SkipNewerThan:    30 * time.Second,
	}
}

// FailedError means at least one file failed after the run finished.
type FailedError struct {
	Count int
}

func (e FailedError) Error() string {
	return fmt.Sprintf("failed=%d", e.Count)
}

func (c Config) validate() error {
	if c.Effort < minEffort || c.Effort > maxEffort {
		return fmt.Errorf("--effort must be in %d..%d", minEffort, maxEffort)
	}
	if c.Distance < 0 || c.Distance > maxDistance {
		return errors.New("--distance must be in 0..25")
	}
	if c.Workers < minWorkers || c.Workers > maxWorkers {
		return fmt.Errorf("--workers must be in %d..%d", minWorkers, maxWorkers)
	}
	if c.ThreadsPerWorker < 0 || c.ThreadsPerWorker > maxThreadsPerWorker {
		return fmt.Errorf("--threads-per-worker must be in 0..%d", maxThreadsPerWorker)
	}
	if c.Limit < 0 {
		return errors.New("--limit must be >= 0")
	}
	if c.SkipNewerThan < 0 {
		return errors.New("--skip-newer-than-seconds must be >= 0")
	}
	return nil
}

func (c Config) distance() float64 {
	if c.Lossless {
		return 0
	}
	return c.Distance
}

type tools struct {
	cjxl    string
	jxlinfo string
}

func lookTools() (tools, error) {
	cjxl, err := exec.LookPath("cjxl")
	if err != nil {
		return tools{}, errors.New("Required command 'cjxl' is not on PATH.")
	}
	jxlinfo, err := exec.LookPath("jxlinfo")
	if err != nil {
		return tools{}, errors.New("Required command 'jxlinfo' is not on PATH.")
	}
	return tools{cjxl: cjxl, jxlinfo: jxlinfo}, nil
}

func defaultFolder() (string, error) {
	return os.Getwd()
}

func resolveFolder(path string) (string, error) {
	var err error
	if strings.TrimSpace(path) == "" {
		path, err = defaultFolder()
		if err != nil {
			return "", err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("Path not found: %s", path)
		}
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}
	return path, nil
}

// Run converts non-empty *.png files in cfg.Path to JPEG XL.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	folder, err := resolveFolder(cfg.Path)
	if err != nil {
		return err
	}
	t, err := lookTools()
	if err != nil {
		return err
	}
	empty, pngs, err := inventory(folder)
	if err != nil {
		return err
	}
	already := 0
	work := make([]png, 0, len(pngs))
	for _, p := range pngs {
		if ctx.Err() != nil {
			break
		}
		if f, ok := adoptExisting(p, t); ok {
			if err := applyStamp(f, cfg.KeepOriginals); err != nil {
				work = append(work, p)
				continue
			}
			already++
			continue
		}
		work = append(work, p)
	}
	if cfg.Limit > 0 && len(work) > cfg.Limit {
		work = work[:cfg.Limit]
	}
	logF, err := os.OpenFile(filepath.Join(folder, logName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logF.Close()
	if err := writeHeader(io.MultiWriter(os.Stdout, logF), cfg, folder, len(work), already, empty); err != nil {
		return err
	}
	if len(work) == 0 {
		fmt.Fprintln(os.Stdout, "Nothing to convert.")
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	events := make(chan event)
	go func() {
		defer close(events)
		runWorkers(ctx, cfg.Workers, work, events, cfg, t)
	}()
	st := foldEvents(events, logF, os.Stdout, stats{Total: len(work)})
	if st.Failed > 0 {
		return FailedError{Count: st.Failed}
	}
	return ctx.Err()
}

func runWorkers(ctx context.Context, n int, jobs []png, events chan<- event, cfg Config, t tools) {
	if n > len(jobs) {
		n = len(jobs)
	}
	if n < 1 {
		n = 1
	}
	ch := make(chan png)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			for p := range ch {
				func() {
					defer func() {
						if r := recover(); r != nil {
							events <- workerError{detail: fmt.Sprint(r)}
						}
					}()
					if ctx.Err() != nil {
						return
					}
					ev := convertOne(ctx, p, cfg, t, time.Now())
					if ev != nil {
						events <- ev
					}
				}()
			}
		}()
	}
	for _, p := range jobs {
		if ctx.Err() != nil {
			break
		}
		ch <- p
	}
	close(ch)
	wg.Wait()
}
