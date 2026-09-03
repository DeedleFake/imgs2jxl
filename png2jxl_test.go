package png2jxl

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	pngenc "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireTools(t *testing.T) tools {
	t.Helper()
	tl, err := lookTools()
	if err != nil {
		t.Skip(err.Error())
	}
	return tl
}

func testCfg(dir string) Config {
	cfg := DefaultConfig()
	cfg.Path = dir
	cfg.Effort = 1
	cfg.Workers = 1
	cfg.ThreadsPerWorker = 1
	cfg.SkipNewerThan = 0
	return cfg
}

func writeSyntheticPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 8), G: uint8(y * 8), B: 200, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pngenc.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func mustRun(t *testing.T, cfg Config) {
	t.Helper()
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readLog(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, logName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConvertSyntheticPNG(t *testing.T) {
	tl := requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "Screenshot (11820).png")
	writeSyntheticPNG(t, pngPath)
	want := time.Date(2021, 6, 7, 8, 9, 10, 0, time.Local)
	if err := os.Chtimes(pngPath, want, want); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testCfg(dir))
	dest := filepath.Join(dir, "Screenshot (11820).jxl")
	if exists(pngPath) {
		t.Fatal("png still present")
	}
	if err := verifyJXL(dest, tl.jxlinfo); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(want) {
		t.Fatalf("mtime %v want %v", fi.ModTime(), want)
	}
	log := readLog(t, dir)
	if !strings.Contains(log, "OK\tScreenshot (11820).png\t") {
		t.Fatalf("log %q", log)
	}
}

func TestLeftoverPartialDeleted(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "a.png")
	writeSyntheticPNG(t, pngPath)
	partial := filepath.Join(dir, "a.jxl.partial")
	other := filepath.Join(dir, "z.jxl.partial")
	if err := os.WriteFile(partial, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testCfg(dir))
	if exists(partial) || exists(other) {
		t.Fatal("partial remains")
	}
	if !exists(filepath.Join(dir, "a.jxl")) {
		t.Fatal("missing dest")
	}
}

func TestAdoptValidSibling(t *testing.T) {
	tl := requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "Screenshot (1).png")
	dest := filepath.Join(dir, "Screenshot (1).jxl")
	writeSyntheticPNG(t, pngPath)
	tmpPNG := filepath.Join(dir, "tmp.png")
	writeSyntheticPNG(t, tmpPNG)
	cmd := exec.Command(tl.cjxl, tmpPNG, dest, "-d", "1", "-e", "1", "--num_threads", "1", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cjxl: %v %s", err, out)
	}
	if err := os.Remove(tmpPNG); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, testCfg(dir))
	if exists(pngPath) {
		t.Fatal("png still present")
	}
	after, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, after) {
		t.Fatal("dest was rewritten")
	}
	log := readLog(t, dir)
	if strings.Contains(log, "OK\t") {
		t.Fatalf("encoded: %q", log)
	}
}

func TestEmptyPNGLeftAlone(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), testCfg(dir)); err != nil {
		t.Fatal(err)
	}
	if !exists(p) {
		t.Fatal("empty png removed")
	}
	log := readLog(t, dir)
	if !strings.Contains(log, "emptyPngsLeftAlone=1") {
		t.Fatalf("log %q", log)
	}
}

func TestSkipRecent(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "new.png")
	writeSyntheticPNG(t, pngPath)
	cfg := testCfg(dir)
	cfg.SkipNewerThan = time.Hour
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !exists(pngPath) {
		t.Fatal("png removed")
	}
	if exists(filepath.Join(dir, "new.jxl")) {
		t.Fatal("encoded recent png")
	}
	log := readLog(t, dir)
	if !strings.Contains(log, "SKIP-RECENT\tnew.png") {
		t.Fatalf("log %q", log)
	}
}

func TestKeepOriginals(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "keep.png")
	writeSyntheticPNG(t, pngPath)
	cfg := testCfg(dir)
	cfg.KeepOriginals = true
	mustRun(t, cfg)
	if !exists(pngPath) {
		t.Fatal("png removed")
	}
	if !exists(filepath.Join(dir, "keep.jxl")) {
		t.Fatal("missing dest")
	}
}

func TestLimitCapsEncodes(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		writeSyntheticPNG(t, filepath.Join(dir, name))
	}
	cfg := testCfg(dir)
	cfg.Limit = 1
	mustRun(t, cfg)
	nPNG, nJXL := 0, 0
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".png":
			nPNG++
		case ".jxl":
			nJXL++
		}
	}
	if nJXL != 1 || nPNG != 2 {
		t.Fatalf("jxl=%d png=%d", nJXL, nPNG)
	}
}

func TestTinyDestReplaced(t *testing.T) {
	tl := requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "tiny.png")
	dest := filepath.Join(dir, "tiny.jxl")
	writeSyntheticPNG(t, pngPath)
	if err := os.WriteFile(dest, []byte("not-a-jxl"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testCfg(dir))
	if exists(pngPath) {
		t.Fatal("png remains")
	}
	if err := verifyJXL(dest, tl.jxlinfo); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentPNGs(t *testing.T) {
	tl := requireTools(t)
	dir := t.TempDir()
	names := []string{"w.png", "x.png", "y.png", "z.png", "m.png", "n.png"}
	for _, n := range names {
		writeSyntheticPNG(t, filepath.Join(dir, n))
	}
	cfg := testCfg(dir)
	cfg.Workers = 4
	mustRun(t, cfg)
	for _, n := range names {
		pngPath := filepath.Join(dir, n)
		dest := strings.TrimSuffix(pngPath, ".png") + ".jxl"
		if exists(pngPath) {
			t.Fatalf("png remains %s", n)
		}
		if err := verifyJXL(dest, tl.jxlinfo); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindowsCrossCompile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "png2jxl.exe")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/png2jxl")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
}

func TestAdoptStampFailureLeavesPNG(t *testing.T) {
	tl := requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "stay.png")
	dest := filepath.Join(dir, "stay.jxl")
	writeSyntheticPNG(t, pngPath)
	tmpPNG := filepath.Join(dir, "tmp.png")
	writeSyntheticPNG(t, tmpPNG)
	cmd := exec.Command(tl.cjxl, tmpPNG, dest, "-d", "1", "-e", "1", "--num_threads", "1", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cjxl: %v %s", err, out)
	}
	if err := os.Remove(tmpPNG); err != nil {
		t.Fatal(err)
	}
	orig := copyTimesFn
	copyTimesFn = func(from, to string) error {
		return errors.New("forced stamp failure")
	}
	t.Cleanup(func() { copyTimesFn = orig })
	err := Run(context.Background(), testCfg(dir))
	var failed FailedError
	if !errors.As(err, &failed) || failed.Count < 1 {
		t.Fatalf("err %v", err)
	}
	if !exists(pngPath) {
		t.Fatal("png was deleted")
	}
	if !exists(dest) {
		t.Fatal("dest missing")
	}
}

func TestStampFailureLeavesPNG(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "stay.png")
	writeSyntheticPNG(t, pngPath)
	orig := copyTimesFn
	copyTimesFn = func(from, to string) error {
		return errors.New("forced stamp failure")
	}
	t.Cleanup(func() { copyTimesFn = orig })
	err := Run(context.Background(), testCfg(dir))
	var failed FailedError
	if !errors.As(err, &failed) || failed.Count < 1 {
		t.Fatalf("err %v", err)
	}
	if !exists(pngPath) {
		t.Fatal("png was deleted")
	}
	if !exists(filepath.Join(dir, "stay.jxl")) {
		t.Fatal("dest missing after encode")
	}
}

func TestEmptyDirNotError(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	if err := Run(context.Background(), testCfg(dir)); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyPathUsesCwd(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := testCfg(dir)
	cfg.Path = ""
	mustRun(t, cfg)
	want, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	log := readLog(t, want)
	if !strings.Contains(log, "folder="+want) {
		t.Fatalf("log %q want folder=%s", log, want)
	}
}

func TestInventory(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticPNG(t, filepath.Join(dir, "b.png"))
	writeSyntheticPNG(t, filepath.Join(dir, "a.png"))
	writeSyntheticPNG(t, filepath.Join(dir, "C.PNG"))
	if err := os.WriteFile(filepath.Join(dir, "empty.png"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.jxl.partial"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Y.JXL.PARTIAL"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty, pngs, err := inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if empty != 1 {
		t.Fatalf("empty %d", empty)
	}
	if len(pngs) != 3 || pngs[0].name != "C.PNG" || pngs[1].name != "a.png" || pngs[2].name != "b.png" {
		t.Fatalf("%v", namesOf(pngs))
	}
	if exists(filepath.Join(dir, "x.jxl.partial")) || exists(filepath.Join(dir, "Y.JXL.PARTIAL")) {
		t.Fatal("partial remains")
	}
}

func namesOf(pngs []png) []string {
	out := make([]string, len(pngs))
	for i, p := range pngs {
		out[i] = p.name
	}
	return out
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Effort != 7 || c.Distance != 1.0 || c.Workers != 8 || c.ThreadsPerWorker != 3 || c.SkipNewerThan != 30*time.Second {
		t.Fatalf("%+v", c)
	}
}

func TestValidateRejectsEffort(t *testing.T) {
	c := DefaultConfig()
	c.Path = t.TempDir()
	c.Effort = 0
	if err := Run(context.Background(), c); err == nil {
		t.Fatal("expected error")
	}
}

func TestReplaceFileOverwritesDest(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "a.jxl")
	src := filepath.Join(dir, "a.jxl.partial")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new-partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(src, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-partial" {
		t.Fatalf("got %q", got)
	}
}

func TestStampWindowsSourceOmitsChtimes(t *testing.T) {
	b, err := os.ReadFile("stamp_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("\"os\"")) {
		t.Fatal("stamp_windows.go must not import os")
	}
	if bytes.Contains(b, []byte("Chtimes")) {
		t.Fatal("stamp_windows.go must not reference Chtimes")
	}
	if !bytes.Contains(b, []byte("GetFileTime")) || !bytes.Contains(b, []byte("SetFileTime")) {
		t.Fatal("stamp_windows.go must use GetFileTime and SetFileTime")
	}
}

func TestReplaceFileSourceHasNoDestDelete(t *testing.T) {
	b, err := os.ReadFile("convert.go")
	if err != nil {
		t.Fatal(err)
	}
	idx := bytes.Index(b, []byte("func replaceFile"))
	if idx < 0 {
		t.Fatal("replaceFile missing")
	}
	rest := b[idx:]
	end := bytes.Index(rest, []byte("\nfunc "))
	if end > 0 {
		rest = rest[:end]
	}
	if bytes.Contains(rest, []byte("Remove")) || bytes.Contains(rest, []byte("RemoveAll")) {
		t.Fatal("replaceFile must not delete dest")
	}
}
