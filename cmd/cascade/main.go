// Command cascade is the Cascade language toolchain: it compiles .cas
// source through AMIVM-IR (via the external amivm CLI) and go build into a
// native executable.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		printUsage(os.Stderr)
		return fmt.Errorf("no command given")
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "build":
		return runBuild(rest)
	case "run":
		return runRun(rest)
	case "emit-ir":
		return runEmitIR(rest)
	case "emit-go":
		return runEmitGo(rest)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Cascade is a compiler for the Cascade programming language.

Usage:

	cascade <command> [flags] <file.cas | package-dir>

The source argument is either a single .cas file (compiled alone, as its
own implicit package) or a directory (compiled as a full package per
cascade_spec.md §11.1, with its own imports resolved).

Commands:

	build      compile to a native executable
	run        compile and immediately run, streaming its stdin/stdout/stderr
	emit-ir    compile to AMIVM-IR
	emit-go    compile to Go source (via amivm)
	help       show this help message

Flags (build, emit-ir, emit-go):

	-o <file>  output file path (default: derived from the input path)
	-v         show each pipeline stage's output as it runs
`)
}

// outputFlags are the -o/-v flags shared by build, emit-ir, and emit-go.
func outputFlags(name string) (fs *flag.FlagSet, out *string, verbose *bool) {
	fs = flag.NewFlagSet(name, flag.ContinueOnError)
	out = fs.String("o", "", "output file path")
	verbose = fs.Bool("v", false, "show each pipeline stage's output as it runs")
	return fs, out, verbose
}

// parseOneSrcArg parses fs against args and returns the single required
// <file.cas | package-dir> positional argument.
func parseOneSrcArg(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 1 {
		return "", fmt.Errorf("usage: cascade %s [-o file] [-v] <file.cas | package-dir>", fs.Name())
	}
	return fs.Arg(0), nil
}

func runBuild(args []string) error {
	fs, out, verbose := outputFlags("build")
	srcPath, err := parseOneSrcArg(fs, args)
	if err != nil {
		return err
	}
	outPath := *out
	if outPath == "" {
		outPath = defaultOutPath(srcPath, "")
	}
	if err := compileToBinary(srcPath, outPath, *verbose); err != nil {
		return err
	}
	fmt.Println(outPath)
	return nil
}

func runRun(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: cascade run <file.cas | package-dir>")
	}
	srcPath := args[0]

	tmp, err := os.MkdirTemp("", "cascade-run-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	binPath := filepath.Join(tmp, "a.out")
	if err := compileToBinary(srcPath, binPath, false); err != nil {
		return err
	}

	runCmd := exec.Command(binPath)
	runCmd.Stdin = os.Stdin
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	if err := runCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func runEmitIR(args []string) error {
	fs, out, verbose := outputFlags("emit-ir")
	srcPath, err := parseOneSrcArg(fs, args)
	if err != nil {
		return err
	}

	ir, err := compileToIR(srcPath)
	if err != nil {
		return err
	}
	if *verbose {
		fmt.Println("=== AMIVM-IR ===")
		fmt.Print(ir)
	}

	outPath := *out
	if outPath == "" {
		outPath = defaultOutPath(srcPath, ".ir")
	}
	if err := os.WriteFile(outPath, []byte(ir), 0o644); err != nil {
		return err
	}
	fmt.Println(outPath)
	return nil
}

func runEmitGo(args []string) error {
	fs, out, verbose := outputFlags("emit-go")
	srcPath, err := parseOneSrcArg(fs, args)
	if err != nil {
		return err
	}

	goSrc, workDir, err := compileToGo(srcPath, *verbose)
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	outPath := *out
	if outPath == "" {
		outPath = defaultOutPath(srcPath, ".go")
	}
	if err := os.WriteFile(outPath, []byte(goSrc), 0o644); err != nil {
		return err
	}
	fmt.Println(outPath)
	return nil
}

// defaultOutPath derives an output path from srcPath (a single .cas file
// or a package directory — see pkgloader.Load's doc) and ext (e.g.
// ".ir", ".go", or "" for a build's bare executable name).
//
// A directory srcPath is reduced to just its own base name, placed in
// the current directory — mirroring `go build ./mypackage`, which
// produces a `mypackage` executable in the caller's own directory, not
// inside ./mypackage itself. Doing anything else here is actively
// dangerous: passing srcPath itself straight through (as if it were a
// file to strip ".cas" from) leaves the output path identical to the
// already-existing source directory, and `go build -o <existing-dir>`
// resolves that by writing the binary *inside* it — silently dropping a
// build artifact into the user's own source tree. A real bug caught by
// running `cascade build` against examples/14_packages during Step 15's
// own verification (see CLAUDE.md's "確定した設計判断").
func defaultOutPath(srcPath, ext string) string {
	if info, err := os.Stat(srcPath); err == nil && info.IsDir() {
		return filepath.Base(filepath.Clean(srcPath)) + ext
	}
	return strings.TrimSuffix(srcPath, ".cas") + ext
}
