package png2jxl

import (
	"fmt"
	"io"
	"strings"
	"time"
)

type stats struct {
	Converted int
	Failed    int
	Skipped   int
	BytesIn   int64
	BytesOut  int64
	Total     int
}

type event interface {
	isEvent()
	logLine() string
	apply(stats) stats
}

type converted struct {
	name              string
	bytesIn, bytesOut int64
}

func (converted) isEvent() {}

func (e converted) logLine() string {
	return fmt.Sprintf("OK\t%s\t%d\t%d", e.name, e.bytesIn, e.bytesOut)
}

func (e converted) apply(s stats) stats {
	s.Converted++
	s.BytesIn += e.bytesIn
	s.BytesOut += e.bytesOut
	return s
}

type already struct {
	name              string
	bytesIn, bytesOut int64
}

func (already) isEvent() {}

func (e already) logLine() string {
	return fmt.Sprintf("OK-EXISTING\t%s\t%d\t%d", e.name, e.bytesIn, e.bytesOut)
}

func (e already) apply(s stats) stats {
	s.Converted++
	s.BytesIn += e.bytesIn
	s.BytesOut += e.bytesOut
	return s
}

type skipped struct {
	name string
}

func (skipped) isEvent() {}

func (e skipped) logLine() string {
	return "SKIP-RECENT\t" + e.name
}

func (e skipped) apply(s stats) stats {
	s.Skipped++
	return s
}

type failed struct {
	name   string
	detail string
}

func (failed) isEvent() {}

func (e failed) logLine() string {
	return fmt.Sprintf("FAIL\t%s\t%s", e.name, e.detail)
}

func (e failed) apply(s stats) stats {
	s.Failed++
	return s
}

type workerError struct {
	detail string
}

func (workerError) isEvent() {}

func (e workerError) logLine() string {
	return "WORKER-ERROR\t" + e.detail
}

func (e workerError) apply(s stats) stats {
	s.Failed++
	return s
}

func writeHeader(w io.Writer, cfg Config, folder string, pending, alreadyHad, empty int) error {
	stamp := time.Now().Format("2006-01-02T15:04:05")
	var shown []string
	for _, f := range cjxlFlags(cfg) {
		if f != "--quiet" {
			shown = append(shown, f)
		}
	}
	mode := "cjxl"
	if len(shown) > 0 {
		mode = strings.Join(shown, " ")
	}
	keep := "False"
	if cfg.KeepOriginals {
		keep = "True"
	}
	lines := []string{
		fmt.Sprintf("=== %s convert PNG -> JXL ===", stamp),
		"folder=" + folder,
		fmt.Sprintf("mode=%s workers=%d keepOriginals=%s", mode, cfg.Workers, keep),
		fmt.Sprintf("pending=%d alreadyHadJxl=%d emptyPngsLeftAlone=%d", pending, alreadyHad, empty),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func footerLine(s stats) string {
	return fmt.Sprintf("=== done converted=%d failed=%d skipped=%d savedBytes=%d ===",
		s.Converted, s.Failed, s.Skipped, s.BytesIn-s.BytesOut)
}

func progressLine(s stats, elapsed time.Duration) string {
	done := s.Converted + s.Failed + s.Skipped
	elapsedS := elapsed.Seconds()
	rate := 0.0
	if elapsedS > 0 {
		rate = float64(done) / elapsedS
	}
	remaining := s.Total - done
	if remaining < 0 {
		remaining = 0
	}
	var eta time.Duration
	if rate > 0 {
		eta = time.Duration(float64(remaining) / rate * float64(time.Second))
	}
	saved := float64(s.BytesIn-s.BytesOut) / (1 << 30)
	return fmt.Sprintf("%d/%d  ok=%d fail=%d skip=%d  saved=%.2f GiB  elapsed=%s  eta=%s",
		done, s.Total, s.Converted, s.Failed, s.Skipped, saved, hms(elapsed), hms(eta))
}

func hms(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d / time.Second)
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func foldEvents(events <-chan event, logW, stdout io.Writer, init stats) stats {
	s := init
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	started := time.Now()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				line := footerLine(s)
				fmt.Fprintln(logW, line)
				fmt.Fprintln(stdout, line)
				return s
			}
			s = ev.apply(s)
			fmt.Fprintln(logW, ev.logLine())
		case <-ticker.C:
			fmt.Fprintln(stdout, progressLine(s, time.Since(started)))
		}
	}
}
