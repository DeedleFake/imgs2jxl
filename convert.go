package imgs2jxl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type needsEncode struct{ img img }

type verifiedPartial struct{ img img }

type finalJXL struct{ img img }

type stampedJXL struct{ img img }

var copyTimesFn = copyTimes

func adoptExisting(p img, t tools) (finalJXL, bool) {
	if err := verifyJXL(p.destPath(), t.jxlinfo); err != nil {
		return finalJXL{}, false
	}
	return finalJXL{img: p}, true
}

func (n needsEncode) encode(t tools, cfg Config) (verifiedPartial, error) {
	partial := n.img.partialPath()
	_ = os.Remove(partial)
	args := append([]string{n.img.path, partial}, cjxlFlags(cfg)...)
	cmd := exec.Command(t.cjxl, args...)
	out, err := cmd.CombinedOutput()
	vErr := verifyJXL(partial, t.jxlinfo)
	if err != nil || vErr != nil {
		_ = os.Remove(partial)
		return verifiedPartial{}, errors.New(failDetail(out, err, vErr))
	}
	return verifiedPartial(n), nil
}

func (v verifiedPartial) renameToFinal() (finalJXL, error) {
	if err := replaceFile(v.img.partialPath(), v.img.destPath()); err != nil {
		return finalJXL{}, err
	}
	return finalJXL(v), nil
}

func (f finalJXL) stamp() (stampedJXL, error) {
	if err := copyTimesFn(f.img.path, f.img.destPath()); err != nil {
		return stampedJXL{}, err
	}
	return stampedJXL(f), nil
}

func (s stampedJXL) removeImg() error {
	return os.Remove(s.img.path)
}

func applyStamp(f finalJXL, keepOriginals bool) error {
	s, err := f.stamp()
	if err != nil {
		return err
	}
	if keepOriginals {
		return nil
	}
	return s.removeImg()
}

func convertOne(ctx context.Context, p img, cfg Config, t tools, now time.Time) event {
	if ctx.Err() != nil {
		return nil
	}
	cur, err := observeImg(p.path)
	if err != nil {
		return failed{name: p.name, detail: err.Error()}
	}
	if cur.tooRecent(now, cfg.SkipNewerThan) {
		return skipped{name: cur.name}
	}
	if f, ok := adoptExisting(cur, t); ok {
		return finishFinal(f, cfg, true)
	}
	v, err := (needsEncode{img: cur}).encode(t, cfg)
	if err != nil {
		return failed{name: cur.name, detail: err.Error()}
	}
	f, err := v.renameToFinal()
	if err != nil {
		return failed{name: cur.name, detail: err.Error()}
	}
	return finishFinal(f, cfg, false)
}

func finishFinal(f finalJXL, cfg Config, existing bool) event {
	if err := applyStamp(f, cfg.KeepOriginals); err != nil {
		return failed{name: f.img.name, detail: err.Error()}
	}
	out := fileSize(f.img.destPath())
	if existing {
		return already{name: f.img.name, bytesIn: f.img.size, bytesOut: out}
	}
	return converted{name: f.img.name, bytesIn: f.img.size, bytesOut: out}
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func verifyJXL(path, jxlinfo string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return errors.New("missing")
	}
	if !fi.Mode().IsRegular() {
		return errors.New("missing")
	}
	if fi.Size() <= minValidJXLSize {
		return errors.New("too small")
	}
	cmd := exec.Command(jxlinfo, path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		status := exitStatus(err)
		text := oneLine(string(out))
		if text == "" {
			return fmt.Errorf("jxlinfo exit %d", status)
		}
		return fmt.Errorf("jxlinfo exit %d %s", status, text)
	}
	return nil
}

func replaceFile(src, dest string) error {
	// Do not delete dest first; that delete is the crash window the scripts accepted.
	err := os.Rename(src, dest)
	if err != nil {
		err = os.Rename(src, dest)
	}
	return err
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func failDetail(out []byte, runErr, verifyErr error) string {
	var parts []string
	status := exitStatus(runErr)
	text := oneLine(string(out))
	if runErr != nil && status < 0 {
		msg := oneLine(runErr.Error())
		if text != "" {
			msg = strings.TrimSpace(msg + " " + text)
		}
		if msg != "" {
			parts = append(parts, "cjxl "+msg)
		}
	} else {
		switch {
		case status == 0 && text == "":
		case status == 0:
			parts = append(parts, "cjxl "+text)
		case text == "":
			parts = append(parts, fmt.Sprintf("cjxl exit %d", status))
		default:
			parts = append(parts, fmt.Sprintf("cjxl exit %d %s", status, text))
		}
	}
	if verifyErr != nil {
		if msg := oneLine(verifyErr.Error()); msg != "" {
			parts = append(parts, msg)
		}
	}
	if len(parts) == 0 {
		return "encode/verify failed"
	}
	return strings.Join(parts, " | ")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}
