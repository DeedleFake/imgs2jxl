package imgs2jxl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type img struct {
	path  string
	name  string
	size  int64
	mtime time.Time
}

func (p img) destPath() string {
	return strings.TrimSuffix(p.path, filepath.Ext(p.path)) + ".jxl"
}

func (p img) partialPath() string {
	return p.destPath() + ".partial"
}

func (p img) tooRecent(now time.Time, d time.Duration) bool {
	if d <= 0 {
		return false
	}
	return now.Sub(p.mtime) < d
}

func observeImg(path string) (img, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return img{}, err
	}
	if !fi.Mode().IsRegular() {
		return img{}, fmt.Errorf("not a regular file: %s", path)
	}
	if fi.Size() <= 0 {
		return img{}, fmt.Errorf("empty image: %s", path)
	}
	return img{
		path:  path,
		name:  filepath.Base(path),
		size:  fi.Size(),
		mtime: fi.ModTime(),
	}, nil
}

func inventory(dir string) (empty int, imgs []img, err error) {
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
			p, obsErr := observeImg(path)
			if obsErr != nil {
				continue
			}
			imgs = append(imgs, p)
		}
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].name < imgs[j].name })
	return empty, imgs, nil
}
