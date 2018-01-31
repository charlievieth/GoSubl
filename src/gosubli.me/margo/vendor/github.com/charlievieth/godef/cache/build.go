package cache

// TODO - maybe

/*
import (
	"go/build"
	"os"
	"sort"
	"time"

	"github.com/charlievieth/pkg/fs"
)

// CEV: terrible name
type fileStat struct {
	name    string
	size    int64
	modTime time.Time
}

func newFileStatList(dir string, names []string) ([]fileStat, error) {
	sort.Strings(names)
	fss := make([]fileStat, len(names))
	for i, name := range names {
		fi, err := fs.Stat(dir + string(os.PathSeparator) + name)
		if err != nil {
			return nil, err
		}
		fss[i] = fileStat{
			name:    name,
			size:    fi.Size(),
			modTime: fi.ModTime(),
		}
	}
	return fss, nil
}

type Package struct {
	// Source files
	GoFiles        map[string]fileStat // .go source files (excluding CgoFiles, TestGoFiles, XTestGoFiles)
	CgoFiles       map[string]fileStat // .go source files that import "C"
	InvalidGoFiles map[string]fileStat // .go source files with detected problems (parse error, wrong package name, and so on)
	CFiles         map[string]fileStat // .c source files
	CXXFiles       map[string]fileStat // .cc, .cpp and .cxx source files
	MFiles         map[string]fileStat // .m (Objective-C) source files
	HFiles         map[string]fileStat // .h, .hh, .hpp and .hxx source files
	FFiles         map[string]fileStat // .f, .F, .for and .f90 Fortran source files
	SFiles         map[string]fileStat // .s source files
	SwigFiles      map[string]fileStat // .swig files
	SwigCXXFiles   map[string]fileStat // .swigcxx files
	SysoFiles      map[string]fileStat // .syso system object files to add to archive

	// Test information
	// TestGoFiles  []fileStat // _test.go files in package
	// XTestGoFiles []fileStat // _test.go files outside package
}

func (p *Package) compareFileStat(stat map[string]fileStat, names []string) bool {
	if len(stat) != len(names) {
		return false
	}
	// fast check against names
	for _, s := range names {
		if _, ok := stat[s]; !ok {
			return false
		}
	}
	for _, s := range names {
		// fi, err := fs.Stat(s)
		if _, ok := stat[s]; !ok {
			return false
		}
	}
	return false
}

func (p *Package) Same(pkg *build.Package) bool {
	if len(p.GoFiles) != len(pkg.GoFiles) ||
		len(p.CgoFiles) != len(pkg.CgoFiles) ||
		len(p.InvalidGoFiles) != len(pkg.InvalidGoFiles) ||
		len(p.CFiles) != len(pkg.CFiles) ||
		len(p.CXXFiles) != len(pkg.CXXFiles) ||
		len(p.MFiles) != len(pkg.MFiles) ||
		len(p.HFiles) != len(pkg.HFiles) ||
		len(p.FFiles) != len(pkg.FFiles) ||
		len(p.SFiles) != len(pkg.SFiles) ||
		len(p.SwigFiles) != len(pkg.SwigFiles) ||
		len(p.SwigCXXFiles) != len(pkg.SwigCXXFiles) ||
		len(p.SysoFiles) != len(pkg.SysoFiles) {
		return false
	}

	return false
}
*/
