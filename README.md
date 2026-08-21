# Cascade

A programming language, implemented in Go, that compiles to Go source via AMIVM-IR. It's the second front end built on AMIVM (after [Seed](seed/)), and goes considerably further: pointers, structs, closures, maps, channels/goroutines, bitwise operators, error handling, and a concurrent pipeline construct built into the language itself.

> [日本語版 README はこちら](README_ja.md)

## Status

Cascade's front end (lexer, parser, semantic checker, and AMIVM-IR code generator) implements the full language described in [`cascade_spec.md`](cascade_spec.md): scalars and null-checked (`T?`) types, the full operator set including bitwise/shift, control flow, lists, user-defined functions with multi-value returns, structs/pointers/receiver functions, closures and higher-order functions (`filter`/`map`/`reduce`), `map<K, V>`, error handling (`error`, `(T, error?)`, postfix `?`), concurrent pipelines (`source`/`stage`/`sink`, `|>`, `send`, `collect`, `abort`, `merge`), and packages/multi-file programs (`import`, `pub`).

## Pipeline

```
Cascade source (.cas)
  ↓ (Cascade — this repository)
AMIVM-IR (.ir)
  ↓ amivm (external tool, github.com/amisonnet8/amivm)
Go source (.go)
  ↓ go build
executable
```

Cascade's own responsibility stops at emitting AMIVM-IR. Turning that into Go source is [amivm](https://github.com/amisonnet8/amivm)'s job, and turning that into an executable is a plain `go build` — both are separate tools `cascade` shells out to, not something this repository implements itself.

## Requirements

- Go, matching the version in [`go.mod`](go.mod).
- [`amivm`](https://github.com/amisonnet8/amivm) on your `PATH`.

## Install

```sh
go install github.com/amisonnet8/amivm/cmd/amivm@latest
go install github.com/amisonnet8/cascade/cmd/cascade@latest
```

Both land in `$GOBIN` (or `$GOPATH/bin` if unset) — make sure that directory is on your `PATH`. Since every Cascade build ends in a plain `go build`, having Go installed already covers every dependency `cascade` needs at runtime; there's nothing else to fetch.

## Usage

```
cascade <command> [flags] <file.cas | package-dir>
```

The source argument is either a single `.cas` file (compiled alone, as its own implicit package) or a directory (compiled as a full package per `cascade_spec.md` §11.1, with `import`s resolved — see [`examples/14_packages/`](examples/14_packages/)).

| Command | Output |
|---|---|
| `build` | a native executable |
| `run` | compiles and immediately runs, streaming its stdin/stdout/stderr |
| `emit-ir` | the AMIVM-IR |
| `emit-go` | the Go source (via amivm) |
| `help` | this command list |

`build`, `emit-ir`, and `emit-go` accept:

| Flag | Description |
|---|---|
| `-o <file>` | output file path (default: derived from the input path, e.g. `foo.cas` → `foo`/`foo.ir`/`foo.go`, or a package directory `foo/` → `foo`/`foo.ir`/`foo.go` in the current directory) |
| `-v` | show each pipeline stage's output as it runs (the generated IR, amivm's own `-v` trace, the final Go source) |

## Example

```cascade
func main(): int {
    print("Hello, Cascade!")
    return 0
}
```

```sh
$ cascade run hello.cas
Hello, Cascade!
```

A concurrent pipeline — one of Cascade's three design pillars (see `cascade_spec.md` §0):

```cascade
source numbers(output: chan<int>) {
    for n in range(1, 7) {
        send(output, n)
    }
}

stage double(input: chan<int>, output: chan<int>) {
    for n in input {
        send(output, n * 2)
    }
}

sink printAll(input: chan<int>) {
    for n in input {
        print(string(n))
    }
}

func main(): int {
    numbers |> double |> printAll
    return 0
}
```

Each stage runs as its own goroutine, connected by channels the compiler generates for you. More runnable examples — one per language feature, in the order they were implemented — live in [`examples/`](examples/).

## Language

**The only authoritative specification is [`cascade_spec.md`](cascade_spec.md).** If any other document (including this README) disagrees with it, `cascade_spec.md` wins.

## Repository layout

```
cmd/cascade/        CLI entry point (this README's `cascade` commands)
internal/lexer/       tokenizing
internal/parser/      parsing → AST
internal/ast/         AST definitions
internal/pkgloader/   package/import resolution (cascade_spec.md §11) — runs before sema,
                       flattening a multi-file/multi-package program into the single-package
                       shape sema/codegen already understand; see CLAUDE.md
internal/sema/        semantic analysis (type checking, scope resolution, null narrowing,
                       pipeline type-checking — amivm delegates all of this to go/types, so
                       Cascade has to catch it itself; see CLAUDE.md)
internal/codegen/     AST → AMIVM-IR
examples/              runnable .cas sample programs, one group per language feature
cascade_spec.md       the Cascade language specification (the only authoritative one)
CLAUDE.md              project conventions for AI-assisted development
LICENSE                MIT license
```

## License

[MIT](LICENSE)
