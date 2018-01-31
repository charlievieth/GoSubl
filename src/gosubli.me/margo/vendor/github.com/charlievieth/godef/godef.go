// The following uses portions of golang.org/x/tools/cmd/guru.

// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package godef

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/ioutil"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/charlievieth/godef/cache"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/buildutil"
	"golang.org/x/tools/go/loader"
)

// A QueryPos represents the position provided as input to a query:
// a textual extent in the program's source code, the AST node it
// corresponds to, and the package to which it belongs.
// Instances are created by parseQueryPos.
type queryPos struct {
	fset       *token.FileSet
	start, end token.Pos           // source extent of query
	path       []ast.Node          // AST path from query node to root of ast.File
	exact      bool                // 2nd result of PathEnclosingInterval
	info       *loader.PackageInfo // type info for the queried package (nil for fastQueryPos)
}

// TypeString prints type T relative to the query position.
func (qpos *queryPos) typeString(T types.Type) string {
	return types.TypeString(T, types.RelativeTo(qpos.info.Pkg))
}

// ObjectString prints object obj relative to the query position.
func (qpos *queryPos) objectString(obj types.Object) string {
	return types.ObjectString(obj, types.RelativeTo(qpos.info.Pkg))
}

// SelectionString prints selection sel relative to the query position.
func (qpos *queryPos) selectionString(sel *types.Selection) string {
	return types.SelectionString(sel, types.RelativeTo(qpos.info.Pkg))
}

type Query struct {
	Mode  string         // query mode ("callers", etc)
	Pos   string         // query position
	Build *build.Context // package loading configuration

	// pointer analysis options
	Scope      []string  // main packages in (*loader.Config).FromArgs syntax
	PTALog     io.Writer // (optional) pointer-analysis log file
	Reflection bool      // model reflection soundly (currently slow).

	// Populated during Run()
	Fset   *token.FileSet
	result *definitionResult
}

func (q *Query) Output(fset *token.FileSet, res *definitionResult) {
	q.Fset = fset
	q.result = res
}

// definition reports the location of the definition of an identifier.
func definition(q *Query) error {
	// First try the simple resolution done by parser.
	// It only works for intra-file references but it is very fast.
	// (Extending this approach to all the files of the package,
	// resolved using ast.NewPackage, was not worth the effort.)
	{
		qpos, err := fastQueryPos(q.Build, q.Pos)
		if err != nil {
			return err
		}

		id, _ := qpos.path[0].(*ast.Ident)
		if id == nil {
			return fmt.Errorf("no identifier here")
		}

		// Did the parser resolve it to a local object?
		if obj := id.Obj; obj != nil && obj.Pos().IsValid() {
			q.Output(qpos.fset, &definitionResult{
				pos:   obj.Pos(),
				descr: fmt.Sprintf("%s %s", obj.Kind, obj.Name),
			})
			return nil // success
		}

		// Qualified identifier?
		if pkg := packageForQualIdent(qpos.path, id); pkg != "" {
			srcdir := filepath.Dir(qpos.fset.File(qpos.start).Name())
			tok, pos, err := findPackageMember(q.Build, qpos.fset, srcdir, pkg, id.Name)
			if err != nil {
				return err
			}
			q.Output(qpos.fset, &definitionResult{
				pos:   pos,
				descr: fmt.Sprintf("%s %s.%s", tok, pkg, id.Name),
			})
			return nil // success
		}

		// Fall back on the type checker.
	}

	// Run the type checker.
	lconf := loader.Config{Build: q.Build}
	allowErrors(&lconf)

	if _, err := importQueryPackage(q.Pos, &lconf); err != nil {
		return err
	}

	// Load/parse/type-check the program.
	lprog, err := lconf.Load()
	if err != nil {
		return err
	}

	qpos, err := parseQueryPos(lprog, q.Pos, false)
	if err != nil {
		return err
	}

	id, _ := qpos.path[0].(*ast.Ident)
	if id == nil {
		return fmt.Errorf("no identifier here")
	}

	// Look up the declaration of this identifier.
	// If id is an anonymous field declaration,
	// it is both a use of a type and a def of a field;
	// prefer the use in that case.
	obj := qpos.info.Uses[id]
	if obj == nil {
		obj = qpos.info.Defs[id]
		if obj == nil {
			// Happens for y in "switch y := x.(type)",
			// and the package declaration,
			// but I think that's all.
			return fmt.Errorf("no object for identifier")
		}
	}

	if !obj.Pos().IsValid() {
		return fmt.Errorf("%s is built in", obj.Name())
	}

	q.Output(lprog.Fset, &definitionResult{
		pos:   obj.Pos(),
		descr: qpos.objectString(obj),
	})
	return nil
}

// packageForQualIdent returns the package p if id is X in a qualified
// identifier p.X; it returns "" otherwise.
//
// Precondition: id is path[0], and the parser did not resolve id to a
// local object.  For speed, packageForQualIdent assumes that p is a
// package iff it is the basename of an import path (and not, say, a
// package-level decl in another file or a predeclared identifier).
func packageForQualIdent(path []ast.Node, id *ast.Ident) string {
	if sel, ok := path[1].(*ast.SelectorExpr); ok && sel.Sel == id && ast.IsExported(id.Name) {
		if pkgid, ok := sel.X.(*ast.Ident); ok && pkgid.Obj == nil {
			f := path[len(path)-1].(*ast.File)
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if imp.Name != nil {
					if imp.Name.Name == pkgid.Name {
						return path // renaming import
					}
				} else if pathpkg.Base(path) == pkgid.Name {
					return path // ordinary import
				}
			}
		}
	}
	return ""
}

// findPackageMember returns the type and position of the declaration of
// pkg.member by loading and parsing the files of that package.
// srcdir is the directory in which the import appears.
func findPackageMember(ctxt *build.Context, fset *token.FileSet, srcdir, pkg, member string) (token.Token, token.Pos, error) {
	bp, err := ctxt.Import(pkg, srcdir, 0)
	if err != nil {
		return 0, token.NoPos, err // no files for package
	}

	type result struct {
		tok token.Token
		pos token.Pos
	}
	ch := make(chan *result, len(bp.GoFiles))
	gate := make(chan struct{}, runtime.NumCPU())
	done := make(chan struct{})

	for _, fname := range bp.GoFiles {
		go func(fname string) {
			select {
			case gate <- struct{}{}:
			case <-done:
				ch <- nil
				return
			}
			defer func() { <-gate }()

			filename := filepath.Join(bp.Dir, fname)

			// Parse the file, opening it the file via the build.Context
			// so that we observe the effects of the -modified flag.
			f, _ := buildutil.ParseFile(fset, ctxt, nil, ".", filename, parser.Mode(0))
			if f == nil {
				ch <- nil
				return
			}

			// Find a package-level decl called 'member'.
			for _, decl := range f.Decls {
				switch decl := decl.(type) {
				case *ast.GenDecl:
					for _, spec := range decl.Specs {
						switch spec := spec.(type) {
						case *ast.ValueSpec:
							// const or var
							for _, id := range spec.Names {
								if id.Name == member {
									ch <- &result{decl.Tok, id.Pos()}
									return
								}
							}
						case *ast.TypeSpec:
							if spec.Name.Name == member {
								ch <- &result{token.TYPE, spec.Name.Pos()}
								return
							}
						}
					}
				case *ast.FuncDecl:
					if decl.Recv == nil && decl.Name.Name == member {
						ch <- &result{token.FUNC, decl.Name.Pos()}
						return
					}
				}
			}
			ch <- nil
		}(fname)
	}

	for i := 0; i < len(bp.GoFiles); i++ {
		if r := <-ch; r != nil {
			close(done)
			return r.tok, r.pos, nil
		}
	}

	return 0, token.NoPos, fmt.Errorf("couldn't find declaration of %s in %q", member, pkg)
}

type definitionResult struct {
	pos   token.Pos // (nonzero) location of definition
	descr string    // description of object it denotes
}

// importQueryPackage finds the package P containing the
// query position and tells conf to import it.
// It returns the package's path.
func importQueryPackage(pos string, conf *loader.Config) (string, error) {
	fqpos, err := fastQueryPos(conf.Build, pos)
	if err != nil {
		return "", err // bad query
	}
	filename := fqpos.fset.File(fqpos.start).Name()

	_, importPath, err := guessImportPath(filename, conf.Build)
	if err != nil {
		// Can't find GOPATH dir.
		// Treat the query file as its own package.
		importPath = "command-line-arguments"
		conf.CreateFromFilenames(importPath, filename)
	} else {
		// Check that it's possible to load the queried package.
		// (e.g. guru tests contain different 'package' decls in same dir.)
		// Keep consistent with logic in loader/util.go!
		cfg2 := *conf.Build
		cfg2.CgoEnabled = false
		bp, err := cfg2.Import(importPath, "", 0)
		if err != nil {
			return "", err // no files for package
		}

		switch pkgContainsFile(bp, filename) {
		case 'T':
			conf.ImportWithTests(importPath)
		case 'X':
			conf.ImportWithTests(importPath)
			importPath += "_test" // for TypeCheckFuncBodies
		case 'G':
			conf.Import(importPath)
		default:
			// This happens for ad-hoc packages like
			// $GOROOT/src/net/http/triv.go.
			return "", fmt.Errorf("package %q doesn't contain file %s",
				importPath, filename)
		}
	}

	conf.TypeCheckFuncBodies = func(p string) bool { return p == importPath }

	return importPath, nil
}

type PathError struct {
	Dir     string
	SrcDirs []string
}

func (p *PathError) Error() string {
	return fmt.Sprintf("directory %s is not beneath any of these GOROOT/GOPATH directories: %s",
		p.Dir, strings.Join(p.SrcDirs, ", "))
}

// guessImportPath finds the package containing filename, and returns
// its source directory (an element of $GOPATH) and its import path
// relative to it.
//
// TODO(adonovan): what about _test.go files that are not part of the
// package?
//
func guessImportPath(filename string, buildContext *build.Context) (srcdir, importPath string, err error) {
	absFile, err := filepath.Abs(filename)
	if err != nil {
		return "", "", fmt.Errorf("can't form absolute path of %s: %v", filename, err)
	}

	absFileDir := filepath.Dir(absFile)
	resolvedAbsFileDir, err := filepath.EvalSymlinks(absFileDir)
	if err != nil {
		return "", "", fmt.Errorf("can't evaluate symlinks of %s: %v", absFileDir, err)
	}

	segmentedAbsFileDir := segments(resolvedAbsFileDir)
	// Find the innermost directory in $GOPATH that encloses filename.
	minD := 1024
	for _, gopathDir := range buildContext.SrcDirs() {
		absDir, err := filepath.Abs(gopathDir)
		if err != nil {
			continue // e.g. non-existent dir on $GOPATH
		}
		resolvedAbsDir, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			continue // e.g. non-existent dir on $GOPATH
		}

		d := prefixLen(segments(resolvedAbsDir), segmentedAbsFileDir)
		// If there are multiple matches,
		// prefer the innermost enclosing directory
		// (smallest d).
		if d >= 0 && d < minD {
			minD = d
			srcdir = gopathDir
			importPath = strings.Join(segmentedAbsFileDir[len(segmentedAbsFileDir)-minD:], string(os.PathSeparator))
		}
	}
	if srcdir == "" {
		return "", "", &PathError{Dir: filepath.Dir(absFile), SrcDirs: buildContext.SrcDirs()}
	}
	if importPath == "" {
		// This happens for e.g. $GOPATH/src/a.go, but
		// "" is not a valid path for (*go/build).Import.
		return "", "", fmt.Errorf("cannot load package in root of source directory %s", srcdir)
	}
	return srcdir, importPath, nil
}

func segments(path string) []string {
	return strings.Split(path, string(os.PathSeparator))
}

// prefixLen returns the length of the remainder of y if x is a prefix
// of y, a negative number otherwise.
func prefixLen(x, y []string) int {
	d := len(y) - len(x)
	if d >= 0 {
		for i := range x {
			if y[i] != x[i] {
				return -1 // not a prefix
			}
		}
	}
	return d
}

// pkgContainsFile reports whether file was among the packages Go
// files, Test files, eXternal test files, or not found.
func pkgContainsFile(bp *build.Package, filename string) byte {
	for i, files := range [][]string{bp.GoFiles, bp.TestGoFiles, bp.XTestGoFiles} {
		for _, file := range files {
			if sameFile(filepath.Join(bp.Dir, file), filename) {
				return "GTX"[i]
			}
		}
	}
	return 0 // not found
}

// ParseQueryPos parses the source query position pos and returns the
// AST node of the loaded program lprog that it identifies.
// If needExact, it must identify a single AST subtree;
// this is appropriate for queries that allow fairly arbitrary syntax,
// e.g. "describe".
//
func parseQueryPos(lprog *loader.Program, pos string, needExact bool) (*queryPos, error) {
	filename, startOffset, endOffset, err := parsePos(pos)
	if err != nil {
		return nil, err
	}

	// Find the named file among those in the loaded program.
	var file *token.File
	lprog.Fset.Iterate(func(f *token.File) bool {
		if sameFile(filename, f.Name()) {
			file = f
			return false // done
		}
		return true // continue
	})
	if file == nil {
		return nil, fmt.Errorf("file %s not found in loaded program", filename)
	}

	start, end, err := fileOffsetToPos(file, startOffset, endOffset)
	if err != nil {
		return nil, err
	}
	info, path, exact := lprog.PathEnclosingInterval(start, end)
	if path == nil {
		return nil, fmt.Errorf("no syntax here")
	}
	if needExact && !exact {
		return nil, fmt.Errorf("ambiguous selection within %s", astutil.NodeDescription(path[0]))
	}
	return &queryPos{lprog.Fset, start, end, path, exact, info}, nil
}

// parseOctothorpDecimal returns the numeric value if s matches "#%d",
// otherwise -1.
func parseOctothorpDecimal(s string) int {
	if s != "" && s[0] == '#' {
		if s, err := strconv.ParseInt(s[1:], 10, 32); err == nil {
			return int(s)
		}
	}
	return -1
}

// parsePos parses a string of the form "file:pos" or
// file:start,end" where pos, start, end match #%d and represent byte
// offsets, and returns its components.
//
// (Numbers without a '#' prefix are reserved for future use,
// e.g. to indicate line/column positions.)
//
func parsePos(pos string) (filename string, startOffset, endOffset int, err error) {
	if pos == "" {
		err = fmt.Errorf("no source position specified")
		return
	}

	colon := strings.LastIndex(pos, ":")
	if colon < 0 {
		err = fmt.Errorf("bad position syntax %q", pos)
		return
	}
	filename, offset := pos[:colon], pos[colon+1:]
	startOffset = -1
	endOffset = -1
	if comma := strings.Index(offset, ","); comma < 0 {
		// e.g. "foo.go:#123"
		startOffset = parseOctothorpDecimal(offset)
		endOffset = startOffset
	} else {
		// e.g. "foo.go:#123,#456"
		startOffset = parseOctothorpDecimal(offset[:comma])
		endOffset = parseOctothorpDecimal(offset[comma+1:])
	}
	if startOffset < 0 || endOffset < 0 {
		err = fmt.Errorf("invalid offset %q in query position", offset)
		return
	}
	return
}

// fileOffsetToPos translates the specified file-relative byte offsets
// into token.Pos form.  It returns an error if the file was not found
// or the offsets were out of bounds.
//
func fileOffsetToPos(file *token.File, startOffset, endOffset int) (start, end token.Pos, err error) {
	// Range check [start..end], inclusive of both end-points.

	if 0 <= startOffset && startOffset <= file.Size() {
		start = file.Pos(int(startOffset))
	} else {
		err = fmt.Errorf("start position is beyond end of file")
		return
	}

	if 0 <= endOffset && endOffset <= file.Size() {
		end = file.Pos(int(endOffset))
	} else {
		err = fmt.Errorf("end position is beyond end of file")
		return
	}

	return
}

// fastQueryPos parses the position string and returns a queryPos.
// It parses only a single file and does not run the type checker.
func fastQueryPos(ctxt *build.Context, pos string) (*queryPos, error) {
	filename, startOffset, endOffset, err := parsePos(pos)
	if err != nil {
		return nil, err
	}

	// Parse the file, opening it the file via the build.Context
	// so that we observe the effects of the -modified flag.
	fset := token.NewFileSet()
	cwd, _ := os.Getwd()
	f, err := buildutil.ParseFile(fset, ctxt, nil, cwd, filename, parser.Mode(0))
	// ParseFile usually returns a partial file along with an error.
	// Only fail if there is no file.
	if f == nil {
		return nil, err
	}
	if !f.Pos().IsValid() {
		return nil, fmt.Errorf("%s is not a Go source file", filename)
	}

	start, end, err := fileOffsetToPos(fset.File(f.Pos()), startOffset, endOffset)
	if err != nil {
		return nil, err
	}

	path, exact := astutil.PathEnclosingInterval(f, start, end)
	if path == nil {
		return nil, fmt.Errorf("no syntax here")
	}

	return &queryPos{fset, start, end, path, exact, nil}, nil
}

// ---------- Utilities ----------

// allowErrors causes type errors to be silently ignored.
// (Not suitable if SSA construction follows.)
func allowErrors(lconf *loader.Config) {
	ctxt := *lconf.Build // copy
	ctxt.CgoEnabled = false
	lconf.Build = &ctxt
	lconf.AllowErrors = true
	// AllErrors makes the parser always return an AST instead of
	// bailing out after 10 errors and returning an empty ast.File.
	lconf.ParserMode = parser.AllErrors
	lconf.TypeChecker.Error = func(err error) {}
}

// sameFile returns true if x and y have the same basename and denote
// the same file.
//
func sameFile(x, y string) bool {
	if filepath.Base(x) == filepath.Base(y) { // (optimisation)
		if xi, err := os.Stat(x); err == nil {
			if yi, err := os.Stat(y); err == nil {
				return os.SameFile(xi, yi)
			}
		}
	}
	return false
}

var (
	fileCache = cache.NewFile(128 * 1024 * 1024) // 128MB
	dirCache  = cache.NewDir(4096)
)

// useModifiedFiles augments the provided build.Context by the
// mapping from file names to alternative contents.
func useModifiedFiles(orig *build.Context, modified map[string][]byte) *build.Context {
	rc := func(data []byte) (io.ReadCloser, error) {
		return ioutil.NopCloser(bytes.NewBuffer(data)), nil
	}
	copy := *orig // make a copy
	ctxt := &copy
	ctxt.OpenFile = func(path string) (io.ReadCloser, error) {
		// Fast path: names match exactly.
		if content, ok := modified[path]; ok {
			return rc(content)
		}
		return fileCache.OpenFile(path)
	}
	ctxt.ReadDir = dirCache.ReadDir
	return ctxt
}

func useModifiedFile(orig *build.Context, modified string, content []byte) *build.Context {
	copy := *orig // make a copy
	ctxt := &copy
	base := filepath.Base(modified)
	info, _ := os.Stat(modified)

	ctxt.OpenFile = func(path string) (io.ReadCloser, error) {
		// Fast path: name matches exactly.
		if path == modified {
			return ioutil.NopCloser(bytes.NewReader(content)), nil
		}
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info != nil && filepath.Base(path) == base {
			if os.SameFile(info, fi) {
				return ioutil.NopCloser(bytes.NewReader(content)), nil
			}
		}
		return fileCache.OpenFileStat(path, fi)
	}

	// WARN
	ctxt.ReadDir = dirCache.ReadDir

	return ctxt
	return nil
}

/*
func useModifiedFile(orig *build.Context, modified string, content []byte) *build.Context {
	copy := *orig // make a copy
	ctxt := &copy
	base := filepath.Base(modified)
	info, _ := os.Stat(modified)

	ctxt.OpenFile = func(path string) (io.ReadCloser, error) {
		// Fast path: name matches exactly.
		if path == modified {
			return ioutil.NopCloser(bytes.NewReader(content)), nil
		}
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info != nil && filepath.Base(path) == base {
			if os.SameFile(info, fi) {
				return ioutil.NopCloser(bytes.NewReader(content)), nil
			}
		}
		return fileCache.OpenFileStat(path, fi)
	}

	// WARN
	ctxt.ReadDir = dirCache.ReadDir

	return ctxt
	return nil
}
*/
