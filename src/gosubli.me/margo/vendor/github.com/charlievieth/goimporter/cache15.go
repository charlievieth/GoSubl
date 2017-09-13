// +build go1.5

package goimporter

import (
	"go/build"
	"go/types"
	"os"

	"github.com/charlievieth/goimporter/internal/gcimporter15"
	"github.com/charlievieth/goimporter/internal/lru"
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
	gc    types.Importer
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

func (c *cacheImporter) Import(path string) (pkg *types.Package, err error) {
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
	if c.gc == nil {
		c.gc = New(c.ctxt)
	}
	pkg, err = c.gc.Import(path)
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
	return &cacheImporter{
		ctxt:  ctxt,
		cache: c.c,
	}
}
