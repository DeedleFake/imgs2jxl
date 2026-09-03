package png2jxl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type png struct {
	path  string
	name  string
	size  int64
	mtime time.Time
}

func (p png) destPath() string {
	return strings.TrimSuffix(p.path, filepath.Ext(p.path)) + ".jxl"
}

func (p png) partialPath() string {
	return p.destPath() + ".partial"
}

func (p png) tooRecent(now time.Time, d time.Duration) bool {
	if d <= 0 {
		return false
	}
	return now.Sub(p.mtime) < d
}

func observePNG(path string) (png, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return png{}, err
	}
	if !fi.Mode().IsRegular() {
		return png{}, fmt.Errorf("not a regular file: %s", path)
	}
	if fi.Size() <= 0 {
		return png{}, fmt.Errorf("empty png: %s", path)
	}
	return png{
		path:  path,
		name:  filepath.Base(path),
		size:  fi.Size(),
		mtime: fi.ModTime(),
	}, nil
}

func inventory(dir string) (empty int, pngs []png, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil, err
	}
	for _, ent := range ents {
		name := ent.Name()
		path := filepath.Join(dir, name)
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".jxl.partial"):
			_ = os.Remove(path)
		case strings.HasSuffix(lower, ".png"):
			fi, statErr := os.Stat(path)
			if statErr != nil || !fi.Mode().IsRegular() {
				continue
			}
			if fi.Size() == 0 {
				empty++
				continue
			}
			p, obsErr := observePNG(path)
			if obsErr != nil {
				continue
			}
			pngs = append(pngs, p)
		}
	}
	sort.Slice(pngs, func(i, j int) bool { return pngs[i].name < pngs[j].name })
	return empty, pngs, nil
}
