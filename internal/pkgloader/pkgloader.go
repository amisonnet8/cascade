// Package pkgloader resolves Cascade packages and imports across multiple
// .cas files/directories per cascade_spec.md §11, before sema runs.
//
// Load is the sole entry point: given a root path (a directory, per
// §11.1's "パッケージ=ディレクトリ", or a single .cas file — see its own
// doc for why both are supported), it discovers every .cas file in that
// directory and merges them into one package (§11.1), recursively
// resolves and loads every package reached via `import` (§11.2, detecting
// import cycles per §11.5 and rejecting a non-root `main`, §11.4),
// rewrites every qualified reference (`qualifier.Name`, in a type, a
// struct literal, or a function call — see rewrite.go) into the target
// package's own already-uniqued flat name, validating `pub` (§11.3) as it
// goes, and finally hands back one fully flat, single-package-shaped
// *ast.File — pooling the root package's own declarations with every
// (already renamed) imported package's — for sema.Check and
// codegen.Generate to consume exactly as they did before Step 14 existed.
// Neither package needs any awareness of packages/imports/pub at all;
// this is the "AST構築後・sema前の独立したフェーズ" CLAUDE.md's own repo
// plan anticipated for this step.
package pkgloader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/amisonnet8/cascade/internal/ast"
	"github.com/amisonnet8/cascade/internal/parser"
)

// loadedPackage is one already-fully-processed package: its own merged,
// self-renamed (if non-root), qualifier-resolved *ast.File, plus the set
// of its own flat (already-prefixed) names that are `pub` — consulted by
// any OTHER package that imports this one (see rewrite.go's
// resolveQualifiedName).
type loadedPackage struct {
	Name     string // directory basename — this package's own identifier prefix (cascade_spec.md §11.6); meaningless for the root, which is never prefixed
	Dir      string // absolute directory path — the cycle-detection/cache key
	File     *ast.File
	PubNames map[string]bool // flat (already-prefixed) name -> true, for every pub struct/func/top-level-let/const this package declares
}

// loader carries the state shared across one whole Load() call: which
// directories are currently on the recursion stack (cycle detection,
// §11.5) and which have already been fully loaded (so a diamond-shaped
// import graph — two different packages both importing a third — loads
// that third package exactly once, matching Go's own import
// deduplication), plus the order packages finished loading in (used to
// merge deterministically).
type loader struct {
	visiting map[string]bool
	loaded   map[string]*loadedPackage
	order    []*loadedPackage
}

// Load resolves root (a directory, or a single .cas file) into one fully
// merged, package-resolved *ast.File.
//
// A single-file root is supported alongside directory roots as a
// deliberate CLI convenience, not a literal reading of cascade_spec.md
// §11.1 (which only ever describes a package as "同じディレクトリに置
// かれた.casファイル群" with no carve-out for compiling just one of
// several files in a shared directory): passing a specific .cas file
// compiles *only* that file, as if it were the sole file in its own
// implicit single-file package, ignoring any other .cas files that
// happen to sit alongside it in the same directory. This keeps every
// existing single-file example (examples/01_hello.cas, etc. — 13 of
// them, all independent, unrelated programs sharing one flat examples/
// directory) invocable exactly as before, while directory roots gain
// full, spec-faithful multi-file/import support for genuine packages
// (see examples/14_packages/ for one). See CLAUDE.md's "確定した設計判
// 断" for the full reasoning.
func Load(root string) (*ast.File, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, err
	}

	var dir, onlyFile string
	if info.IsDir() {
		dir = absRoot
	} else {
		dir = filepath.Dir(absRoot)
		onlyFile = absRoot
	}

	ld := &loader{visiting: map[string]bool{}, loaded: map[string]*loadedPackage{}}
	if _, err := ld.loadPackage(dir, onlyFile, true); err != nil {
		return nil, err
	}

	merged := &ast.File{}
	for _, pkg := range ld.order {
		merged.Structs = append(merged.Structs, pkg.File.Structs...)
		merged.Funcs = append(merged.Funcs, pkg.File.Funcs...)
		merged.Stages = append(merged.Stages, pkg.File.Stages...)
		merged.Lets = append(merged.Lets, pkg.File.Lets...)
	}
	return merged, nil
}

// loadPackage loads, merges, self-renames (if non-root), and
// qualifier-resolves the package rooted at dir (restricted to onlyFile
// alone when it's non-empty — see Load's doc). isRoot controls both
// whether `main` is allowed (§11.4) and whether this package's own
// declarations get a name prefix at all (§11.6 — "ルートパッケージの識
// 別子には接頭辞を付けない").
func (ld *loader) loadPackage(dir, onlyFile string, isRoot bool) (*loadedPackage, error) {
	if pkg, ok := ld.loaded[dir]; ok {
		return pkg, nil
	}
	if ld.visiting[dir] {
		return nil, fmt.Errorf("circular import: package %q (or one it imports) imports itself, directly or indirectly (cascade_spec.md §11.5)", filepath.Base(dir))
	}
	ld.visiting[dir] = true
	defer delete(ld.visiting, dir)

	file, err := loadPackageFiles(dir, onlyFile)
	if err != nil {
		return nil, err
	}

	name := filepath.Base(dir)

	if !isRoot {
		for _, fn := range file.Funcs {
			if fn.Receiver == nil && fn.Name == "main" {
				return nil, fmt.Errorf("package %q: 'main' can only be declared in the root package (cascade_spec.md §11.4)", name)
			}
		}
	}

	// Imports are resolved (and, transitively, THEIR OWN imports) before
	// this package's own body is rewritten, so that by the time we get to
	// qualifier resolution below, every imported package's PubNames table
	// is already finalized.
	quals := map[string]*loadedPackage{}
	for _, imp := range file.Imports {
		targetDir, err := filepath.Abs(filepath.Join(dir, imp.Path))
		if err != nil {
			return nil, err
		}
		target, err := ld.loadPackage(targetDir, "", false)
		if err != nil {
			return nil, err
		}
		if existing, dup := quals[imp.Qualifier]; dup && existing.Dir != target.Dir {
			return nil, fmt.Errorf("line %d: import qualifier %q is already used for a different package in this package", imp.Line, imp.Qualifier)
		}
		quals[imp.Qualifier] = target
	}
	file.Imports = nil // fully resolved; sema/codegen never need it

	var renames map[string]string
	pubBareNames := map[string]bool{}
	if !isRoot {
		renames = collectRenames(file, name)
		collectPubBareNames(file, pubBareNames)
		applySelfRename(file, renames)
	}

	rw := &rewriter{renames: renames, quals: quals}
	if err := rw.rewriteFile(file); err != nil {
		return nil, err
	}

	pubNames := map[string]bool{}
	for bare := range pubBareNames {
		pubNames[renames[bare]] = true
	}

	pkg := &loadedPackage{Name: name, Dir: dir, File: file, PubNames: pubNames}
	ld.loaded[dir] = pkg
	ld.order = append(ld.order, pkg)
	return pkg, nil
}

// loadPackageFiles parses and merges every .cas file in dir (or just
// onlyFile, if given — see Load's doc) into one *ast.File, in
// deterministic (sorted-path) order.
func loadPackageFiles(dir, onlyFile string) (*ast.File, error) {
	var paths []string
	if onlyFile != "" {
		paths = []string{onlyFile}
	} else {
		matches, err := filepath.Glob(filepath.Join(dir, "*.cas"))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no .cas files found in %q", dir)
		}
		sort.Strings(matches)
		paths = matches
	}

	merged := &ast.File{}
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f, err := parser.Parse(string(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		merged.Imports = append(merged.Imports, f.Imports...)
		merged.Structs = append(merged.Structs, f.Structs...)
		merged.Funcs = append(merged.Funcs, f.Funcs...)
		merged.Stages = append(merged.Stages, f.Stages...)
		merged.Lets = append(merged.Lets, f.Lets...)
	}
	return merged, nil
}

// collectRenames builds this (non-root) package's own bare-name ->
// prefixed-name table (cascade_spec.md §11.6) for every top-level
// declaration that needs one: struct types, plain functions (a
// receiver function's own Name is left alone — see rewrite.go's doc for
// why methods don't need this), source/stage/sink names, and top-level
// let/const names.
func collectRenames(file *ast.File, prefix string) map[string]string {
	renames := map[string]string{}
	for _, sd := range file.Structs {
		renames[sd.Name] = prefix + "_" + sd.Name
	}
	for _, fn := range file.Funcs {
		if fn.Receiver == nil {
			renames[fn.Name] = prefix + "_" + fn.Name
		}
	}
	for _, sd := range file.Stages {
		renames[sd.Name] = prefix + "_" + sd.Name
	}
	for _, ld := range file.Lets {
		renames[ld.Name] = prefix + "_" + ld.Name
	}
	return renames
}

// collectPubBareNames records the ORIGINAL (pre-rename) bare name of
// every top-level declaration marked `pub` (cascade_spec.md §11.3) —
// called before applySelfRename mutates those same declarations' own
// Name fields, so it must run first.
func collectPubBareNames(file *ast.File, out map[string]bool) {
	for _, sd := range file.Structs {
		if sd.Pub {
			out[sd.Name] = true
		}
	}
	for _, fn := range file.Funcs {
		if fn.Receiver == nil && fn.Pub {
			out[fn.Name] = true
		}
	}
	for _, sd := range file.Stages {
		if sd.Pub {
			out[sd.Name] = true
		}
	}
	for _, ld := range file.Lets {
		if ld.Pub {
			out[ld.Name] = true
		}
	}
}

// applySelfRename renames every top-level declaration's own Name field
// in place, using the bare -> prefixed table collectRenames built. This
// only touches each declaration's identity; every REFERENCE to it
// (elsewhere in this same package's own files) is handled separately by
// rewriteFile, using the identical renames table.
func applySelfRename(file *ast.File, renames map[string]string) {
	for _, sd := range file.Structs {
		sd.Name = renames[sd.Name]
	}
	for _, fn := range file.Funcs {
		if fn.Receiver == nil {
			fn.Name = renames[fn.Name]
		}
	}
	for _, sd := range file.Stages {
		sd.Name = renames[sd.Name]
	}
	for _, ld := range file.Lets {
		ld.Name = renames[ld.Name]
	}
}
