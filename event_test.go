package png2jxl

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestEventLogLines(t *testing.T) {
	tests := []struct {
		ev   event
		line string
	}{
		{converted{name: "a.png", bytesIn: 10, bytesOut: 4}, "OK\ta.png\t10\t4"},
		{already{name: "a.png", bytesIn: 10, bytesOut: 4}, "OK-EXISTING\ta.png\t10\t4"},
		{skipped{name: "a.png"}, "SKIP-RECENT\ta.png"},
		{failed{name: "a.png", detail: "nope"}, "FAIL\ta.png\tnope"},
		{workerError{detail: "boom"}, "WORKER-ERROR\tboom"},
	}
	for _, tt := range tests {
		if got := tt.ev.logLine(); got != tt.line {
			t.Errorf("got %q want %q", got, tt.line)
		}
	}
}

func TestFoldEvents(t *testing.T) {
	evs := make(chan event, 3)
	evs <- converted{name: "a.png", bytesIn: 10, bytesOut: 4}
	evs <- skipped{name: "b.png"}
	close(evs)
	var logB, outB bytes.Buffer
	s := foldEvents(evs, &logB, &outB, stats{Total: 2})
	if s.Converted != 1 || s.Skipped != 1 || s.Failed != 0 {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(logB.String(), "OK\ta.png\t10\t4") {
		t.Fatalf("log %q", logB.String())
	}
	wantFooter := "=== done converted=1 failed=0 skipped=1 savedBytes=6 ==="
	if !strings.Contains(logB.String(), wantFooter) || !strings.Contains(outB.String(), wantFooter) {
		t.Fatalf("footer log=%q out=%q", logB.String(), outB.String())
	}
}

func TestWriteHeader(t *testing.T) {
	var b bytes.Buffer
	cfg := DefaultConfig()
	if err := writeHeader(&b, cfg, "/tmp/x", 3, 2, 1); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.Contains(s, " convert PNG -> JXL ===") {
		t.Fatal(s)
	}
	if !strings.Contains(s, "folder=/tmp/x") {
		t.Fatal(s)
	}
	if !strings.Contains(s, "mode=cjxl workers=8 keepOriginals=False") {
		t.Fatal(s)
	}
	if strings.Contains(s, "-e ") || strings.Contains(s, "threads/worker=") {
		t.Fatal(s)
	}
	if !strings.Contains(s, "pending=3 alreadyHadJxl=2 emptyPngsLeftAlone=1") {
		t.Fatal(s)
	}
	b.Reset()
	cfg.Lossless = true
	cfg.KeepOriginals = true
	if err := writeHeader(&b, cfg, "/tmp/x", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	s = b.String()
	if !strings.Contains(s, "mode=-d 0 workers=8 keepOriginals=True") {
		t.Fatal(s)
	}
	b.Reset()
	cfg = DefaultConfig()
	cfg.Effort = ptr(7)
	cfg.Distance = ptr(1.0)
	cfg.ThreadsPerWorker = ptr(3)
	if err := writeHeader(&b, cfg, "/tmp/x", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	s = b.String()
	if !strings.Contains(s, "mode=-d 1 -e 7 --num_threads 3 workers=8 keepOriginals=False") {
		t.Fatal(s)
	}
}

func TestProgressLine(t *testing.T) {
	got := progressLine(stats{Converted: 1, BytesIn: 1 << 30, Total: 2}, 5*time.Second)
	want := "1/2  ok=1 fail=0 skip=0  saved=1.00 GiB  elapsed=00:00:05  eta=00:00:05"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAlreadyApplyCountsConverted(t *testing.T) {
	s := already{name: "a.png", bytesIn: 8, bytesOut: 3}.apply(stats{})
	if s.Converted != 1 || s.BytesIn != 8 || s.BytesOut != 3 {
		t.Fatalf("%+v", s)
	}
}
