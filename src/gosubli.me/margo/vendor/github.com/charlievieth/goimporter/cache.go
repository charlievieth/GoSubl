// +build !go1.5

package goimporter

import (
	"go/build"
	"os"

	"github.com/charlievieth/goimporter/internal/gcimporter"
	"github.com/charlievieth/goimporter/internal/lru"
	"github.com/charlievieth/goimporter/internal/types"
)

type Cache struct {
	c *lru.Cache
}

func NewCache(maxEntries int) *Cache {
	return &Cache{c: lru.New(maxEntries)}
}

type cacheImporter struct {
	ctxt  *build.Context
	cache *lru.Cache
	imp   types.Importer
}

func findPkg(ctxt *build.Context, path string) (filename string, err error) {
	srcDir := "."
	if build.IsLocalImport(path) {
		srcDir, err = os.Getwd()
		if err != nil {
			return
		}
	}
	filename, _ = gcimporter.FindPkg(ctxt, path, srcDir)
	return
}

func (c *cacheImporter) Import(pkgs map[string]*types.Package, path string) (pkg *types.Package, err error) {
	var fi os.FileInfo
	filename, err := findPkg(c.ctxt, path)
	if err != nil {
		return nil, err
	}
	if pc, ok := c.cache.Get(c.ctxt, path); ok {
		fi, err = os.Stat(filename)
		if err != nil {
			return
		}
		if fi.Size() == pc.Info.Size() && fi.ModTime() == pc.Info.ModTime() {
			return pc.Pkg, nil
		}
	}
	if c.imp == nil {
		c.imp = New(c.ctxt)
	}
	pkg, err = c.imp(pkgs, path)
	if err != nil {
		return
	}
	if fi == nil {
		fi, err = os.Stat(filename)
		if err != nil {
			return
		}
	}
	c.cache.Add(c.ctxt, path, &lru.Package{Pkg: pkg, Info: fi})
	return pkg, nil
}

func (c *Cache) Importer(ctxt *build.Context) types.Importer {
	m := &cacheImporter{
		ctxt:  ctxt,
		cache: c.c,
	}
	return m.Import
}
