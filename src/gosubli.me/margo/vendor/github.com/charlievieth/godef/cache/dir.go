package cache

import (
	"os"
	"sync"
	"time"

	"github.com/charlievieth/godef/lru"
)

type fileInfo struct {
	name  string
	isDir bool
}

func (f fileInfo) Name() string       { return f.name }
func (f fileInfo) IsDir() bool        { return f.isDir }
func (f fileInfo) Size() int64        { panic("cache: fileInfo.Size() not implemented") }
func (f fileInfo) Mode() os.FileMode  { panic("cache: fileInfo.Mode() not implemented") }
func (f fileInfo) ModTime() time.Time { panic("cache: fileInfo.ModTime() not implemented") }
func (f fileInfo) Sys() interface{}   { panic("cache: fileInfo.Sys() not implemented") }

type dirEntry struct {
	ents    []os.FileInfo
	modTime time.Time
}

type Dir struct {
	sync.Mutex
	maxSize int
	cache   lru.Cache
}

func NewDir(maxSize int) *Dir {
	return &Dir{maxSize: maxSize}
}

func (d *Dir) maxEntries(_ *lru.Cache) bool {
	return d.maxSize > 0 && d.cache.Len() > d.maxSize
}

func (d *Dir) lazyInit() {
	if d.maxSize > 0 && d.cache.MaxEntries == nil {
		d.cache.MaxEntries = d.maxEntries
	}
}

func (d *Dir) get(path string) (*dirEntry, bool) {
	d.Lock()
	d.lazyInit()
	var e *dirEntry
	v, ok := d.cache.Get(path)
	if ok {
		e = v.(*dirEntry)
	}
	d.Unlock()
	return e, ok
}

func (d *Dir) remove(path string) {
	d.Lock()
	d.cache.Remove(path)
	d.Unlock()
}

func (d *Dir) readDir(path string) ([]os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	fis, err := readdir(f)
	f.Close()
	if err != nil {
		return nil, err
	}

	modTime := fi.ModTime()
	d.Lock()
	d.lazyInit()
	if v, ok := d.cache.Get(path); ok {
		if e := v.(*dirEntry); e.modTime.After(modTime) {
			d.Unlock()
			return e.ents, nil
		}
	}
	d.cache.Add(path, &dirEntry{
		ents:    fis,
		modTime: modTime,
	})
	d.Unlock()

	return fis, nil
}

func (d *Dir) ReadDir(path string) ([]os.FileInfo, error) {
	if e, ok := d.get(path); ok {
		fi, err := os.Stat(path)
		if e.modTime.Equal(fi.ModTime()) {
			return e.ents, nil
		}
		d.remove(path)
		if err != nil {
			return nil, err
		}
	}
	return d.readDir(path)
}
