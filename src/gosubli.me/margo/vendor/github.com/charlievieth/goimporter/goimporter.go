// +build !go1.5

// Package importer provides access to export data importers.
package goimporter

import (
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"runtime"

	"github.com/charlievieth/goimporter/internal/gccgoimporter"
	"github.com/charlievieth/goimporter/internal/gcimporter"
	"github.com/charlievieth/goimporter/internal/types"
)

// DefaultContext, returns a copy of the default build.Context with updated
// GOROOT and GOPATH fields.
func DefaultContext() *build.Context {
	ctxt := build.Default
	ctxt.GOROOT = runtime.GOROOT()
	ctxt.GOPATH = os.Getenv("GOPATH")
	return &ctxt
}

// For returns an Importer for the given compiler.  Supported compilers are
// "gc", and "gccgo"
func For(compiler string) types.Importer {
	switch compiler {
	case "gc":
		return New(DefaultContext())
	case "gccgo":
		var inst gccgoimporter.GccgoInstallation
		if err := inst.InitFromDriver("gccgo"); err != nil {
			return nil
		}
		return inst.GetImporter(nil, nil)
	}
	// compiler not supported
	return nil
}

// Default returns an Importer for the compiler that built the running binary.
func Default() types.Importer {
	return For(runtime.Compiler)
}

// A gcimporter implements the types.Importer interface for the gc compiler.
//
// The importer auto-installs packages missing object files, and uses it's own
// build.Context to look up package paths, unlike Importer returned by the
// go/importer package, which uses build.Default.
type gcimports struct {
	ctxt *build.Context
}

// Returns a new types.Importer that uses the provided build.Context to
// determine package paths.  The returned types.Importer will auto-install
// any packages missing object files.
func New(ctxt *build.Context) types.Importer {
	m := &gcimports{ctxt: ctxt}
	return m.Import
}

// Import imports a gc-generated package given its import path.  Packages
// missing object files are installed and re-imported.
func (m *gcimports) Import(pkgs map[string]*types.Package, path string) (pkg *types.Package, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case error:
				err = fmt.Errorf("importer: internal error: %q", e.Error())
			case string:
				err = fmt.Errorf("importer: internal error: %q", e)
			default:
				err = fmt.Errorf("importer: internal error: %+v", e)
			}
		}
	}()

	pkg, err = gcimporter.Import(m.ctxt, pkgs, path)

	// Attempt to install missing packages.
	if IsNotFound(err) {
		bp, _ := m.ctxt.Import(path, ".", build.FindOnly|build.AllowBinary)
		if bp.PkgObj == "" {
			return
		}
		// Looks like the package was installed since we last checked.
		if fi, e := os.Stat(bp.PkgObj); e == nil && !fi.IsDir() {
			return gcimporter.Import(m.ctxt, pkgs, path)
		}
		// Install package and attempt to import it again.
		if e := exec.Command("go", "install", bp.ImportPath).Run(); e == nil {
			return gcimporter.Import(m.ctxt, pkgs, path)
		}
	}

	return
}

// IsNotFound returns if err is a gcimporter.NotFoundError.
func IsNotFound(err error) bool {
	return gcimporter.IsNotFound(err)
}
