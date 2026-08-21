package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/amisonnet8/cascade/internal/codegen"
	"github.com/amisonnet8/cascade/internal/pkgloader"
	"github.com/amisonnet8/cascade/internal/sema"
)

// compileToIR runs Cascade's own share of the pipeline — pkgloader.Load
// (package/import resolution, cascade_spec.md §11), sema.Check,
// codegen.Generate — and returns the resulting AMIVM-IR text. Per
// CLAUDE.md, this is the full extent of what Cascade itself is responsible
// for; everything past this point hands off to an external tool (amivm,
// then go build). srcPath may name a single .cas file (compiled alone, as
// its own implicit package — see pkgloader.Load's doc) or a directory
// (compiled as a full package per §11.1, with imports resolved).
func compileToIR(srcPath string) (string, error) {
	file, err := pkgloader.Load(srcPath)
	if err != nil {
		return "", fmt.Errorf("package error: %w", err)
	}

	if err := sema.Check(file); err != nil {
		return "", fmt.Errorf("semantic error: %w", err)
	}

	ir, err := codegen.Generate(file)
	if err != nil {
		return "", fmt.Errorf("codegen error: %w", err)
	}
	return ir, nil
}

// compileToGo compiles srcPath to IR and runs it through amivm, returning
// the generated Go source and the scratch module directory it was written
// into. The caller owns workDir and must remove it once done —
// compileToBinary keeps it around to run `go build` in the same module.
//
// amivm requires its output directory to be a Go module (its cross-package
// type-checking is module-aware), so the Go file is generated inside a
// scratch module rather than as a bare file.
//
// verbose prints the IR before invoking amivm, passes -v through to amivm
// (showing its own type-checking trace and the final Go source), and
// echoes amivm's output.
func compileToGo(srcPath string, verbose bool) (goSrc, workDir string, err error) {
	ir, err := compileToIR(srcPath)
	if err != nil {
		return "", "", err
	}
	if verbose {
		fmt.Println("=== AMIVM-IR ===")
		fmt.Print(ir)
	}

	workDir, err = os.MkdirTemp("", "cascade-build-*")
	if err != nil {
		return "", "", err
	}
	cleanup := func() { os.RemoveAll(workDir) }

	modPath := filepath.Join(workDir, "go.mod")
	if err := os.WriteFile(modPath, []byte("module cascadebuild\n\ngo 1.26\n"), 0o644); err != nil {
		cleanup()
		return "", "", err
	}
	irPath := filepath.Join(workDir, "main.ir")
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		cleanup()
		return "", "", err
	}

	goPath := filepath.Join(workDir, "main.go")
	amivmArgs := []string{irPath, "-o", goPath}
	if verbose {
		amivmArgs = append(amivmArgs, "-v")
	}
	out, runErr := exec.Command("amivm", amivmArgs...).CombinedOutput()
	if verbose && len(out) > 0 {
		os.Stdout.Write(out)
	}
	if runErr != nil {
		cleanup()
		if verbose {
			return "", "", fmt.Errorf("amivm failed")
		}
		return "", "", fmt.Errorf("amivm:\n%s", out)
	}

	goBytes, err := os.ReadFile(goPath)
	if err != nil {
		cleanup()
		return "", "", err
	}
	return string(goBytes), workDir, nil
}

// compileToBinary runs the full Cascade → AMIVM-IR → Go → binary pipeline
// and writes the resulting executable to outPath.
func compileToBinary(srcPath, outPath string, verbose bool) error {
	_, workDir, err := compileToGo(srcPath, verbose)
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return err
	}
	buildCmd := exec.Command("go", "build", "-o", absOut, ".")
	buildCmd.Dir = workDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build:\n%s", out)
	}
	return nil
}
