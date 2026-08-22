# Cascade

AMIVM-IRを経由してGoソースコードへコンパイルする、Go実装のプログラミング言語です。AMIVM上の2つ目のフロントエンド言語([[Seed]](seed/)に続く)で、Seedよりも大きく踏み込んでいます: ポインタ・構造体・クロージャー・map・チャネル/goroutine・ビット演算・エラー処理、そして言語組み込みの並行パイプライン構文を持ちます。

> [English README is here](README.md)

## ステータス

Cascadeのフロントエンド(字句解析・構文解析・意味検査・AMIVM-IRコード生成)は、[`cascade_spec.md`](cascade_spec.md)に記載された言語仕様を一通り実装済みです: スカラー型とnull許容(`T?`)型、ビット演算・シフトを含む全演算子、制御構文、リスト、複数戻り値対応のユーザー定義関数、構造体/ポインタ/レシーバー関数、クロージャーと高階関数(`filter`/`map`/`reduce`)、`map<K, V>`、エラー処理(`error`型・`(T, error?)`・後置`?`)、並行パイプライン(`source`/`stage`/`sink`・`|>`・`send`・`collect`・`abort`・`merge`)、パッケージ/複数ファイル対応(`import`・`pub`)。

## パイプライン

```
Cascadeソース (.cas)
  ↓ (Cascade — 本リポジトリ)
AMIVM-IR (.ir)
  ↓ amivm (外部ツール。github.com/amisonnet8/amivm)
Goソースコード (.go)
  ↓ go build
実行ファイル
```

Cascade自身の責務はAMIVM-IRを出力するところまでです。それをGoソースへ変換するのは[amivm](https://github.com/amisonnet8/amivm)の仕事、実行ファイルにする単純な`go build`はさらに別工程で、どちらも`cascade`が呼び出す外部ツールであり、本リポジトリ自体が実装しているものではありません。

## 動作要件

- Go([`go.mod`](go.mod)記載のバージョン)
- `PATH`の通った場所にインストールされた[`amivm`](https://github.com/amisonnet8/amivm)

## インストール

```sh
go install github.com/amisonnet8/amivm/cmd/amivm@latest
go install github.com/amisonnet8/cascade/cmd/cascade@latest
```

どちらも`$GOBIN`(未設定なら`$GOPATH/bin`)に配置されるので、そのディレクトリが`PATH`に通っていることを確認してください。Cascadeのビルドは最終的に必ず素の`go build`で終わるため、Goさえインストールされていれば`cascade`が実行時に必要とするものは全て揃います(それ以外に取得すべきものはありません)。

## 使い方

```
cascade <コマンド> [フラグ] <file.cas | パッケージディレクトリ>
```

ソース引数には単一の`.cas`ファイル(そのファイル1つだけの、暗黙のパッケージとしてコンパイルされる)か、ディレクトリ(`cascade_spec.md`11.1節の通り、`import`も解決した1つのパッケージとしてコンパイルされる。[`examples/14_packages/`](examples/14_packages/)参照)のどちらかを渡せます。

| コマンド | 出力 |
|---|---|
| `build` | 実行ファイル |
| `run` | コンパイルして即座に実行(stdin/stdout/stderrをそのまま引き継ぐ) |
| `emit-ir` | AMIVM-IR |
| `emit-go` | Goソースコード(amivm経由) |
| `help` | このコマンド一覧 |

`build`・`emit-ir`・`emit-go`は以下のフラグを受け付けます。

| フラグ | 説明 |
|---|---|
| `-o <file>` | 出力ファイルパス(省略時は入力パスから導出。例: `foo.cas` → `foo`/`foo.ir`/`foo.go`。パッケージディレクトリ`foo/`の場合はカレントディレクトリに`foo`/`foo.ir`/`foo.go`) |
| `-v` | 各パイプライン段階の出力を実行しながら表示(生成されたIR、amivm自身の`-v`トレース、最終的なGoソース) |

## 例

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

並行パイプライン——Cascadeの3本柱(`cascade_spec.md` 0節参照)の1つです。

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

各ステージはそれぞれ独立したgoroutineとして動き、コンパイラが生成するチャネルで繋がっています。言語機能ごと・実装した順に並んだ実行可能なサンプルを[`examples/`](examples/)に置いています。

## 言語仕様

**唯一の正確な仕様は[`cascade_spec.md`](cascade_spec.md)です。** 本READMEを含む他のドキュメントと矛盾する場合は`cascade_spec.md`を優先してください。

## リポジトリ構成

```
cmd/cascade/          CLIエントリポイント(本READMEの`cascade`コマンド群)
internal/lexer/         字句解析
internal/parser/        構文解析 → AST
internal/ast/           AST定義
internal/pkgloader/     パッケージ/importの解決(cascade_spec.md 11節)。semaより前に動き、
                         複数ファイル・複数パッケージのプログラムを、sema/codegenが元々理解する
                         単一パッケージの形へ平坦化する。詳細はCLAUDE.md参照
internal/sema/          意味検査(型チェック・スコープ解決・null絞り込み・パイプライン型接続検査。
                         amivm側は全てgo/typesに委ねているためCascade自身がここを担う必要がある。
                         詳細はCLAUDE.md参照)
internal/codegen/       AST → AMIVM-IR
examples/                実行可能な.casサンプル(言語機能ごと・実装した順にグループ化)
cascade_spec.md         Cascade言語仕様(唯一の正確な仕様)
cascade_implementation_notes.md  AMIVM-IRを生成するフロントエンドを書く上での知見
                         (Cascade固有ではなく一般的な話に絞ってある)
CLAUDE.md                AIによる開発支援のためのプロジェクト規約
LICENSE                  MITライセンス
```

## ライセンス

[MIT](LICENSE)
