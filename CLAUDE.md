# Cascade プロジェクト規約

## プロジェクト概要

Cascadeは新規に設計する独自プログラミング言語。**Go言語で実装する**(Cascadeコンパイラ自身の実装言語がGo)。ソースファイルの拡張子は**`.cas`**。AMIVM上に実装する2つ目のフロントエンド言語で、1つ目の[[Seed]]がスカラー・固定長配列・制御構文・関数程度に留めたのに対し、Cascadeは**ポインタ・構造体・クロージャー・map・チャネル/goroutine・ビット演算**まで積極的に使う設計になっている。言語名・構文・キーワードはSeedを踏襲しない独自のもの。

設計の3本柱(詳細は`cascade_spec.md` 0節):

1. **手続き型+レシーバー**: `obj.method()`の形で呼べるレシーバー付き関数(クラス・継承は無い)
2. **関数型的要素**: 関数を値として扱える。クロージャーで`filter`/`map`/`reduce`を書ける
3. **パイプラインによる縦の並列化**: `source |> stage1 |> stage2 |> sink`で各段が独立goroutine相当として並行に動く

コンパイルパイプラインはSeedと同じ3工程。

```
Cascadeソース (.cas)
  ↓ (Cascadeが担当。本リポジトリのスコープ)
AMIVM-IR (.ir)
  ↓ amivm (外部CLIツール。別リポジトリ)
Goコード (.go)
  ↓ go build (Cascadeのビルドパイプラインが担当)
実行ファイル
```

**Cascadeの責務は「Cascadeソース → AMIVM-IR」の生成まで。** この3工程の境界を越えて責務を持たせない(例: CascadeがGoコードを直接生成する、AMIVM-IRの意味検証をCascade側でも二重に行う、といった実装はしない)。この原則は[[Seed]]開発時と同じ。

### 全命令使用という目標について

Cascade開発でAMIVMの全命令を使うことが目標として掲げられている。**ただし無理に全命令を使う必要はなく、言語設計・実装が自然にそうなればよい、という程度の努力目標。** 不自然な機能をこじつけてまで未使用命令を消化する必要はない。進捗は下記「命令使用ゴール」節の表で管理する。

## ドキュメント構成

| ファイル/ディレクトリ | 役割 |
|---|---|
| `cascade_spec.md` | **Cascade言語仕様。唯一の正確な仕様。** 字句規則・型・演算子・制御構文・構造体・クロージャー・パイプライン・モジュールなどを定義。実装と齟齬が出たら、まず`cascade_spec.md`の記述を疑い、仕様として確定してからコードを直すこと |
| `amivm/docs/amivm_spec.md` | AMIVM-IRの唯一の正確な仕様(下記「AMIVM-IRの書き方」節に要点を転記)。amivm本体のバージョンを上げた際は齟齬がないか確認すること |
| `seed_implementation_notes.md` | **[[Seed]]の実装で踏んだ地雷・確立したパターンのまとめ。Cascade実装前に必読。** 特にgoto/VAR巻き上げ問題(§1)は最重要。Seedが未実証だった命令(ポインタ・構造体・map・クロージャー・チャネル・select・ビット演算)をCascadeでは実際に使うことになるため、§5の「未実証命令への手がかり」は実地検証必須の仮説として扱うこと |
| `seed/` | Seedの実装済みリポジトリ(参考実装)。ディレクトリ構成・レイヤー分割(`lexer`/`parser`/`ast`/`sema`/`codegen`)・CLIの作り(`flag.NewFlagSet`によるサブコマンド)・テスト戦略の実例として参照してよい。**Cascadeリポジトリの一部ではない** |
| `amivm/` | 参照用にローカルへ置かれているamivmリポジトリのクローン。**Cascadeリポジトリの一部ではない。** amivmはCascadeから見て「外部CLIツール」であり、`go install`で`PATH`に配置して呼び出す(下記参照) |
| 本ファイル(`CLAUDE.md`) | Cascadeプロジェクトの規約・AIによる開発支援のための注意点 |

## amivmのインストール・呼び出し方

`amivm`はGo製CLIで、`go install`でインストールして`PATH`経由で呼ぶ(コピーやパス直接参照はしない)。

```sh
# amivmリポジトリをclone後
make install   # go install ./cmd/amivm — $GOBIN(未設定なら$GOPATH/bin)に配置される
```

### CLIコマンド仕様

```
amivm <IRファイルパス> [-o|--output <出力ファイルパス>] [-v|--verbose] [-i|--import <名前>=<importパス>]...
```

- `-o`/`--output`省略時の出力先は、IRファイルパスの拡張子を`.go`に置き換えたパス
- `-v`/`--verbose`を付けると元のIR・型チェックの過程・最終的な生成コード・完了メッセージを標準出力に表示する
- `-i`/`--import <名前>=<importパス>`は繰り返し指定できる。**Cascadeが独自のランタイムライブラリ(`cascadert`等、未使用なら消える)を呼びたい場合はこれを使う**
- ファイル読み込み失敗・IRパースエラー・型チェック失敗などのエラーは常に出力する
- `go build`による実行ファイル生成は行わない(別工程。Cascade側のビルドパイプラインで実行する)

## AMIVM-IRの書き方(唯一の正確な仕様)

以下は`amivm/docs/amivm_spec.md`からの要点転記。**Cascadeのコード生成部がAMIVM-IRを出力する際は、この命令セット・カテゴリ・Kind分類に厳密に従うこと。**

### 制約・前提条件

- `FUNC`はトップレベルのみに置ける(関数のネスト不可)。`STTYPE`・`CLOS`・`SEL`もネスト不可
- 配列は1次元固定長のみ。多次元配列はAMIVM-IR自体では表現しない(Cascadeの`[]T`はそもそも1次元のみなので該当しない)
- チャネル・スライス・map・構造体・クロージャーは、対応する`TYPE`系命令(`SLTYPE`/`MPTYPE`/`STTYPE`/`FNTYPE`/`CHTYPE`)で型を定義してから使う
- トークンの区切り文字は**タブ**。行頭のインデント用タブは無視。`//`で始まる行はコメントとして無視

### 識別子のプレフィックス

| 記号 | 意味 |
|---|---|
| `$` | 関数引数 |
| `&` | クロージャー引数 |
| `%` | 関数内変数名 |
| `@` | 関数外変数名(グローバル変数) |
| `^` | 型名 |
| `>` | 構造体フィールド名 |
| `!` | amivm定義関数名(`!xxx`→`<関数名>_amivm_function`、`!main`→`main`) |
| `?` | Go関数名(標準ライブラリ・Cascade独自ランタイム問わず) |
| `#` | ラベル名 |

### 命令一覧(全カテゴリ)

| 分類 | 命令 |
|---|---|
| 変数宣言 | `VAR local type1` / `GVAR global type1` |
| 代入・ポインタ・配列 | `SET` `ASET` `AGET` `PSET` `PGET` `ADDR` |
| 算術 | `ADD` `SUB` `MUL` `DIV` `MOD` |
| ビット演算 | `BAND` `BOR` `BXOR` `BCLEAR` `BNOT` |
| シフト | `SHL` `SHR` |
| 論理演算 | `AND` `OR` `NOT` |
| 比較 | `EQ` `NEQ` `LT` `LTE` `GT` `GTE` |
| 文字列連結 | `CONCAT single slice1 slice2 ...` |
| ラベル・分岐 | `LABEL label` / `GOTO label` / `IF boolean1 label` |
| 関数定義 | `FUNC defname type1 ... : type3 ...` / `RET` / `ENDFUNC` |
| 関数呼び出し | `CALL multi1 ... : callname value1 ...` / `DEFER` / `SPAWN` |
| チャネル | `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` |
| select | `SEL` `CASESEND` `CASERECV` `DEFAULT` `ENDSEL` |
| スライス | `SLTYPE` `SLMAKE` `SLICE` |
| 構造体 | `STTYPE` `FIELD` `ENDSTTYPE` `FSET` `FGET` |
| map | `MPTYPE` `MPMAKE` `MSET` `MGET` |
| クロージャー・関数型 | `FNTYPE` `CLOS` `ENDCLOS` |

各命令の生成Goコード・オペランドカテゴリ(`whole`/`integer`/`value`/`single`/`multi`等)・Kind分類は`amivm/docs/amivm_spec.md`の4〜6節を参照。**キャスト・組み込み関数(`close`/`len`/`cap`等)は専用命令を持たず`CALL`に統合されている**(Goの型変換`T(v)`は構文上`ast.CallExpr`と同一のため)。

## 命令使用ゴール(Cascade機能とAMIVM命令の対応表)

「全命令を使う」目標の進捗管理表。実装が進むにつれ状態を更新すること。

| 命令 | 対応するCascade機能 | 状態 |
|---|---|---|
| `VAR` `GVAR` `SET` `ASET` `AGET` | `let`/`const`、`[]T`要素の読み書き | 確定 |
| `ADD` `SUB` `MUL` `DIV` `MOD` | 算術演算子(6節) | 確定 |
| `BAND` `BOR` `BXOR` `BCLEAR` `BNOT` | `&` `\|` `^` `&^` `~`(int専用ビット演算、6節) | 確定 |
| `SHL` `SHR` | `<<` `>>`(int専用、6節) | 確定 |
| `AND` `OR` `NOT` | `&&` `\|\|` `!` | 確定 |
| `EQ` `NEQ` `LT` `LTE` `GT` `GTE` | 比較演算子。`is none`/`is not none`もnull許容型の内部フラグ比較として実装できる見込み | 確定 |
| `CONCAT` | `string + string` | 確定 |
| `LABEL` `GOTO` `IF` | `if`/`elif`/`else`/`while`/`for`/`switch`/`break`/`continue`(制御構文全般) | 確定 |
| `FUNC` `RET` `ENDFUNC` `CALL` | 通常関数・レシーバー付き関数・複数戻り値(8節) | 確定 |
| `ADDR` `PGET` `PSET` | `&x`(アドレス取得)・`*p`(デリファレンス)・`*ptr = v`(2.2/4.4/6節) | 確定 |
| `SLTYPE` `SLMAKE` `ASET` `AGET` | `[]T`(可変長リスト、4.3節)。`append`は再SLMAKE+コピーで実現する見込み(Seedの配列と異なり可変長なので再確保が前提) | 確定 |
| `SLICE` | Step 9で実装。ユーザー向け構文としては今も存在しないが、`filter`組み込み関数の内部実装(結果件数が事前にわからないため入力と同じ長さで`SLMAKE`し、マッチ分だけ前方に詰めてから実際の件数へ`SLICE`で切り詰める)で自然な使い道が見つかった | 確定 |
| `STTYPE` `FIELD` `ENDSTTYPE` `FSET` `FGET` | `struct`定義・フィールドアクセス(4.1/5節) | 確定 |
| `MPTYPE` `MPMAKE` `MSET` `MGET` | `map<K, V>`(4.5節)。`m[k]`が返す`V?`は予想通り`MGET`のcomma-ok形をそのまま値+nullフラグへ流し込めた(seed_implementation_notes.md §3の予想が的中)。Step 10で実装・実地検証済み | 確定 |
| `FNTYPE` `CLOS` `ENDCLOS` | `func(...): R`型・クロージャーリテラル(8.3節)。Step 9で実装・実地検証済み | 確定 |
| `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` | `chan<T>`・`send()`・`for v in someChannel`(9節) | 確定 |
| `SPAWN` | `source`/`stage`/`sink`を`|>`で連結した際の各段の並行実行(9.2節) | 確定 |
| `SEL` `CASESEND` `CASERECV` `DEFAULT` | `merge()`のファンイン実装(9.5節)。2チャネルのどちらか先に来た方を受け取るselect相当 | 確定(実装方針は下記オープン課題参照) |
| `DEFER` | ユーザー向け構文としては存在しない。**候補**: `source`/`stage`本体の生成コード先頭で`DEFER : ?close 出力チャネル`し、関数(=ステージ)終了時に出力チャネルを自動closeして下流の`for-in`を終了させる、というコード生成側の内部利用 | **要検討**(下記オープン課題参照) |

## オープンな設計課題(実装前に方針を確定させ、確定次第この節を書き換える)

Seedと異なりCascadeは複数ファイル/パッケージ・null許容型・並行パイプラインという、Seedに無かった複雑さを持つ。以下は現時点で未確定の、最初に潰すべき設計課題。**[[Seed]]の「確定した設計判断」節(`seed/CLAUDE.md`)に倣い、決まったらここに確定内容として書き残すこと。仮説のまま放置しない。**

### 1. パイプライン(9節)の並行実行モデル

最も設計コストが高い箇所。`amivm/test_ir/11_spawn_channel_sel.ir`を実装前に必ず読むこと。

- `source`/`stage`/`sink`は各`SPAWN`されるgoroutine(`FUNC`)として生成
- 段間の`chan<T>`は`CHTYPE`+`CHMAKE`
- `for v in input`は`CHRECV`のcomma-ok形をループ条件に使い、`ok == false`(チャネルclose)でループを抜ける形になる見込み
- 各段が処理を終えたら出力チャネルをcloseして下流に伝播させる必要がある(`DEFER : ?close 出力チャネル`が候補。上記命令使用ゴール表参照)
- `collect`(9.3節): 終端チャネルを受信し続けて`[]T`に溜め込む専用のcollector goroutineを内部生成し、結果を`main`側が別チャネル経由で同期的に受け取る、という形が候補
- `abort`(9.4節): 全ステージを即座に止める必要がある。**候補**: closeすることが一度きりの安全なブロードキャストになるGoの性質を使い、専用の`chan<bool>`(またはstring)をcloseすることで中断を通知し、各ステージの`for-in`ループを`SEL`(`CASERECV input` vs `CASERECV abortChan`)に変える
- `merge`(9.5節): 2入力チャネルを`SEL`の`CASERECV`×2でファンインする専用goroutineとして実装する候補

いずれも**実装したら`amivm`→`go build`→実行まで必ず確認する**(seed_implementation_notes.md §6.1の教訓)。並行処理はロジック上正しく見えても実地検証なしでは信用しない。

### 2. `error`型(8.6節)の表現

`err.message`のようなフィールドアクセスが必要なため、Goの組み込み`error`インターフェースにマッピングするより、**`^error`という名前で`message: string`を1フィールド持つ`STTYPE`をCascadeコンパイラが自動定義する**案が有力(2.2節の説明「実体は`message: string`を1つ持つ構造体的な値」とも整合する)。後置`?`演算子(8.6節)はsemaが「戻り値の末尾が`error?`である呼び出し」を検出し、`IF`+`RET`による早期returnへ機械的に展開する(Seedのビルトイン脱糖と同じ発想)。

### 3. パッケージ/モジュール解決(11節)

Seedには存在しなかった、Cascade固有の追加コンパイル工程。`import`文の解決・循環import検出(11.5節)・パッケージ内の複数ファイル統合(11.1節)は、字句解析・構文解析より**前**、あるいはAST構築後・sema前の独立したフェーズ(パッケージローダー)として実装する必要がある。AMIVM-IRへ落とす際の識別子一意化(`パッケージ名_識別子`、11.6節)はcodegenの命名規則に組み込む。

## 確定した設計判断

実装を進める中で確定した設計判断をここに記録する。上記「オープンな設計課題」とは異なり、既に方針が決まった事項はここへ追記していく(Seedの`seed/CLAUDE.md`「確定した設計判断」節と同じ運用)。

### パーサの実装方式

**手書きの再帰下降パーサ(式は演算子優先順位法/Pratt parsing)を採用する。パーサジェネレータ(goyacc/ANTLR等)は使わない。**

理由:

- [[Seed]]も同じ方針(`internal/lexer`/`internal/parser`とも手書き)で実績があり、レイヤー分割・実装パターンをそのまま踏襲できる
- `cascade_spec.md` 6節の演算子優先順位表(前置`&`/`*`/`~`、後置`?`によるエラー伝播、多数の中置演算子)はPratt parsingと相性が良く、BNF+パーサジェネレータより素直に実装・保守できる
- 「意味検証の責任分担」節の通りCascadeコンパイラ自身がユーザーに分かりやすいエラーメッセージを出す方針であり、生成コードより手書きパーサの方がエラー箇所・メッセージを細かく制御しやすい

トレードオフ: 将来文法が大きく複雑化した場合、手書きコードの保守コストがパーサジェネレータより高くなりうるが、現状の文法規模では許容範囲と判断した。

### `main`のコンパイル方針(Step 1で確定)

Cascadeの`func main(): int`をamivmの`!main`へ直接対応させることはできない。Goの`func main()`は引数無し・戻り値無しを要求するが、Cascadeのエントリポイントは`int`を返す(12節)ため、そのまま`FUNC !main : ^int`として生成すると`go build`が`func main must have no arguments and no return values`で失敗する。**[[Seed]]の`main`/`seed_main`分離と全く同じ解決策を採る**: ユーザーの`main`は内部名`cascade_main`(`internal/sema`と`internal/codegen`の両方に同名の定数として定義。名前が一致している前提でコードが動くため、変更時は両方揃えて直すこと)としてコンパイルし、実際の`!main`は`cascade_main`を呼んで戻り値を`os.Exit()`に渡す薄いラッパーとして生成する。ユーザーが関数名として`cascade_main`を使うことはsemaがエラーにする(Seedの`seed_main`予約と同じ)。

### `T?`の実装方式(Step 2で確定)

想定通り、Seedの「値+`_isset`」ペアを**`T?`が付いた変数だけ**に適用する形で確定した(`?`の無い型はそもそも`none`を取れないため、フラグ自体を持たない)。

- `internal/codegen/scope.go`の`varRef`が`ValOp`(値。常に存在)と`SetOp`(成否フラグ。`Type.Nullable`のときだけ存在。空文字列なら「フラグ無し」)を持つ
- 通常の読み取り(`genValue`の`Ident`ケース)は`SetOp`を一切見ず`ValOp`をそのまま返す。GoのゼロValueがCascadeのベース値と全スカラー型で一致するため、これで常に正しい(Seedの`_isset`と同じ性質)
- `none`の代入・初期化省略は「値をゼロ値にSET」+「フラグを`false`にSET」の2命令、それ以外の値の代入は「値をSET」+「フラグを`true`にSET」の2命令(`genInit`/`genResetToZero`)
- `MGET`/`CHRECV`のcomma-ok形を直接使う案(seed_implementation_notes.md §3)は今回のスカラー変数では不要だった(単純な`SET`2命令で十分)。map(Step10)・チャネル(Step12)の`V?`/`T?`はcomma-ok形が自然に使える可能性が高く、そちらで改めて検証する
- **`is none`/`is not none`(7節)の構文自体はStep 2では実装していない**。spec上この構文は`if`の条件式としてしか使用例が無く(7節)、絞り込み(narrowing)も`if`の分岐構造に依存するため、`if`が無い状態で先取り実装すると仕様に無い用法(独立したbool式としての使用)を勝手に決めてしまうリスクがあった。Step 5(制御構文)で`if`と同時に実装し、絞り込みはsemaがAST上でのみ扱う(絞り込み後の型をアノテーションする、codegen側は追加のIR命令不要)という方針は維持する

### `string()`組み込み変換の前倒し実装(Step 3)

演算子の計算結果(int/float/bool)を`print`で観測できるようにするため、本来ビルトイン関数一式として後回しにできるはずの`string()`(13節)だけStep 3で先取り実装した。新規AMIVM命令は不要で、`strconv.Itoa`/`strconv.FormatFloat`/`strconv.FormatBool`を`CALL`で直接呼ぶだけで済む(Goの素の`string(65)`はルーン変換になり`"A"`を返す罠があるため、`strconv`必須。seed_implementation_notes.md §3と同じ教訓)。`ast.CallExpr`に`ArgType`フィールドを追加し、semaが解決した引数の型をcodegenが再計算せずに使う(`ResolvedType`/`ResultType`と同じアノテーションパターン。Seedの同名`ArgType`を踏襲)。

**文法上の注意点として記録**: `int`/`float`/`string`/`bool`は型キーワードであると同時に、式の中では組み込み変換関数の呼び出し(`string(x)`)にもなる。これらは`KwString`等の予約語としてlexされ`Ident`にはならないため、`parsePrimary`は`Ident`とは別に型キーワードのケースを持ち、直後が`(`なら呼び出し式として`parseCallExprFrom`に渡す(そうでなければ式の位置に型キーワードが出現したという通常の構文エラー)。将来`int()`/`float()`ビルトイン(それぞれ`string`からの変換は`int?`/`float?`を返す点に注意)を実装する際もこの分岐に相乗りできる。

### ビット演算・シフト演算の実地検証結果(Step 4)

`BAND` `BOR` `BXOR` `BCLEAR` `BNOT` `SHL` `SHR`はSeedで未実証だった命令(seed_implementation_notes.md §0)。実装方針はロジック上の予想通りで、生成パターンに驚きは無かった(同notes §5.6の予想が的中): `+`/`-`/`*`と全く同じ「演算子ごとに命令を振り分け、一時変数へ結果を格納する」パターン(`genBinary`/`genUnary`)がそのまま流用でき、`BNOT`(単項)以外は全て2項命令。単項マイナスと違い`~`には対応する命令(`BNOT`)が存在するため、`SUB tmp 0 x`のような回避策は不要だった。

**唯一の非自明点は文法(命令ではなく`cascade_spec.md`側)にあった**: 6節の優先順位表は`<<`/`>>`(優先度5)を`+`/`-`(優先度4)より**低い**優先度に置いている。C/Go系言語の直感(シフトは加減算より高優先度)とは逆の並びで、`4 << 1 + 1`は`4 << (1+1) = 16`と解釈される(`(4 << 1) + 1 = 9`ではない)。手書き再帰下降パーサでは`parseShift`が`parseAdditive`を「より結合力の強い次の階層」として呼ぶ形になるため実装自体は素直だが、テストコード(`codegen_test.go`の`TestGenerate_ShiftPrecedenceLooserThanAdditive`)で明示的に固定していないと将来の変更で見逃されるリスクがあった。`amivm`→`go build`→実行(`examples/04_bitwise.cas`)でも数値レベルで検証済み。

### goto/VAR巻き上げ問題の実地検証結果、および制御構文の設計(Step 5)

**seed_implementation_notes.md §1の懸念(goto/VAR巻き上げ)は、Cascadeでも実際に起こりうる形で確認され、Step2で先取りしていた対策がそのまま機能した。** `internal/codegen/codegen.go`の`funcGen.decls`(全`VAR`をフラットにホイストし、`SET`だけを元の位置に残す仕組み)はStep2の時点で導入済みだったため、Step5で`if`/`elif`/`else`/`while`/`switch`を実装した際にコード変更は不要だった。`genBlock`(Seedと同名・同役割)が各ブロックにcodegen側スコープを与え、変数解決(シャドーイング)を扱う一方、Go側の変数配置は常にフラットなまま。単体テスト`TestGenerate_VarHoistingAcrossIfElifElse`(`elif`/`else`節内の`let`が全て関数先頭に来ることを検証)と、`examples/05_control_flow.cas`の`amivm`→`go build`→実行で二重に確認した。

制御構文のcodegenパターンは[[Seed]]の`genIfStmt`/`genWhileStmt`(`seed/internal/codegen/stmt.go`)をほぼそのまま踏襲した(条件评価直後に`IF`、無条件`GOTO`で後続をスキップ、というelif連鎖の作り方)。Seedに無かった要素は以下の2点:

- **`switch`の`break`/`continue`分離**: 7節「`switch`自体はループではない」という規定により、`break`はswitchとループの両方から抜けられるが、`continue`はswitchを素通りして囲むループにしか効かない。`funcGen`に`breakStack`(while**と**switchが積む)と`continueStack`(whileのみが積む)を別々に持たせることで実現した。単体テスト`TestGenerate_ContinueInSwitchTargetsEnclosingLoop`で、switch内の`continue`がwhileの開始ラベルへ直接ジャンプすることを確認済み
- **タグ付き/タグなしswitchの共通コード生成**: どちらも「各case値についてIF→ラベル」という同じ構造だが、タグ付きは`EQ`比較を挟み、タグなしはcase値(bool式)をそのまま`IF`条件に使う。AST(`SwitchCase.Values`)は共通のまま、codegen側の1箇所(`genSwitchStmt`)で`stmt.Tag != nil`分岐するだけで両対応できた

**`is none`/`is not none`と型絞り込み(narrowing、2.3節)をStep2からの持ち越しとして実装した。** 絞り込みは事前の想定通り**semaのみ**で完結し、codegen側の変更は一切不要だった(`is not none`の読み取りは既存の`_isset`フラグを返すだけ、`is none`はそれを`NOT`で反転するだけ)。sema側は3パターンを実装:

1. 各clause自身の本体内: 条件が`X is not none`ならそのclauseの子スコープでXを非null型にshadow
2. `else`節内: ifが単一clauseで条件が`X is none`のとき、elseの子スコープでXを非null型にshadow(条件の否定として自明)
3. if文全体の後: 単一clause・条件が`X is none`・本体が`return`/`break`/`continue`で終わる場合のみ、囲むスコープ自体にXを非null型でshadowし、以降の文に効かせる

いずれも新設した`scope.shadow`(既存宣言があっても強制上書きする、`declareLocal`とは別の内部用メソッド)で実現。3パターンとも`examples/05_control_flow.cas`で実行時に値レベルで確認済み(else節の絞り込みはnoneでない値を渡す追加確認も実施)。仕様が直接例示していない組み合わせ(elif節での絞り込み一般化、switch内での絞り込みなど)は実装していない。

### リスト(`[]T`)の型表現・`append`の実装方針(Step 6)

**`ast.Type`に`Elem *Type`を追加し、リスト型を表現した。** 既存の`Name string`(スカラー型名)と共存させ、`Elem != nil`ならリスト型・`Name`は不使用という形にした(新しい`Kind`列挙などは導入せず、既存構造体に1フィールド追加するだけで済んだ)。`SLTYPE`+`SLMAKE`+`ASET`/`AGET`はSeedの配列実装で既に実証済みの命令(seed_implementation_notes.md §0)だが、Cascade特有の可変長・`append`・`range`の実地検証は今回が初めて。

**構文上の非自明な決定: `?`は常に最も外側の型に結合する。** `[]int?`は`([]int)?`(nullableなリスト)であって`[](int?)`(nullableなintのリスト)ではない、と決定した。根拠は9.3節の`collect`が返す`[]string?`という具体例(パイプライン全体が`none`になりうる、要素がnullになりうるわけではない)。パーサは`parseTypeBase`(`[]`プレフィックスの再帰的な処理のみ、`?`は見ない)と`parseType`(`parseTypeBase`の後に一度だけ`?`をチェック)を分離することで実現した。この結果、現在の文法では「要素自体がnullableなリスト」(`[](T?)`)は表現できない——仕様例に出てこないため、今のところ問題にしていない。

**`append`はGoの生の`append()`を使わず、常に`SLMAKE`で新しい配列を確保し要素をコピーする方式にした。** Goの`append`は容量に余裕があれば元の裏付け配列に書き込むことがあり、同じ裏付け配列を共有する複数のリストが独立に`append`されると、互いに観測されない形で上書きし合う可能性がある(元のリストの「見える範囲」自体は破壊されないため大抵は無害だが、常に安全とは言い切れない)。CLAUDE.mdの「命令使用ゴール」表に元々書いていた「Seedの配列と異なり可変長なので再確保が前提」という想定通りの実装にし、`append`のたびに`?len`→`SLMAKE`→コピーループ(`AGET`/`ASET`)→末尾へ新要素、という完全に独立した新しいリストを返す形にした。`examples/06_lists.cas`で`len(元のリスト)`が`append`後も変わらないことを実地確認済み。

**副産物のバグ修正**: リスト型の比較ロジックを書く過程で、`checkAssignable`が値の`Nullable`を全くチェックしていなかったことに気づいた(`let a: int? = 5; let b: int = a`が絞り込み無しで通ってしまっていた)。`vt.Nullable && !target.Nullable`のときエラーにするよう修正し、回帰テストを追加した(`TestCheck_ListErrors/nullable_value_cannot_narrow_implicitly_into_a_non-nullable_target`)。あわせて型の同値判定を`typeShapeEqual`(Nullableを無視し、リストは要素型を再帰比較)に統一し、`[]int == []string`のような異なる要素型同士の比較がすり抜けていた別のバグも合わせて塞いだ。

### 関数(通常関数・複数戻り値)の設計(Step 7)

**sema側に`checker`構造体(`sigs map[string]funcSig`)を新設し、それまで裸の関数群だった`internal/sema`をメソッド群へ書き換えた。** これはseed_implementation_notes.md §7が予告していた「プログラム全体で共有する情報(前方参照可能な関数シグネチャ表など)が必要になった時点で小さな構造体を導入する」というタイミングそのもの。`Check`は最初に全関数のシグネチャを1回のパスで集め、その後に各関数本体を検査するため、宣言順序に関係なく前方参照・相互再帰・自己再帰が動く(`examples/07_functions.cas`の`factorial`で自己再帰を実地確認)。スコープ(`*scope`)はブロックごとに変わる情報なので、Seedの助言通り構造体のフィールドにはせず引数のまま渡し続けている。codegen側も同じ理由で独立した`funcSig`テーブルを自前に持つ(sema側には依存しない。seed_implementation_notes.md §2の「semaとcodegenは互いに依存しない」を踏襲)。

**nullable引数はCALL境界を越える際に値+`_isset`フラグの2オペランドへ展開する。** 関数引数は1つの宣言型につき1つの値しか渡せないため、`T?`パラメータの「値がセットされているか」フラグは呼び出し側が明示的にもう1つのオペランドとして渡す以外に方法が無い(Seedの「パラメータは常に既に代入済みなのでローカルで`true`にSETするだけでよい」という簡略化はCascadeでは通用しない——Cascadeのnullable引数は`none`を正当な値として本当に受け取れる必要があるため)。`FUNC`の仮引数リストで`T?`が2スロット(値+`^bool`)に展開され(`genFuncDecl`)、呼び出し側も同じ形で2オペランドを渡す(`genCallArgs`)。`examples/07_functions.cas`の`greet(name: string?)`を`greet(none)`/`greet("Cascade")`の両方で呼び、生成IRが`!greet "" false`/`!greet "Cascade" true`になることを確認済み。

**nullable戻り値(`func f(): T?`)は今回実装せず、semaで明示的に拒否することにした。** 戻り値がCALL/RET境界を越えてnullableフラグを運ぶには、引数と対称的な「RET/CALLの結果オペランドも2つに展開する」機構が必要になるが、Step7の仕様例(`add`/`log`/`divmod`)はどれもnullable戻り値を使わない。一方Step11の`(T, error?)`規約はまさにこの機構を必要とするため、生半可に今作って後で仕様に合わせて手直しするより、Step11で腰を据えて設計する方が良いと判断した。`checkFuncSig`が`fn.Results`に`Nullable`な型があれば明示的にエラーにする(曖昧な未定義動作にせず、はっきり「未対応」と伝える)。

**副産物のバグ修正(2件目)**: 関数引数のnullable展開を実装する過程で、`genInit`(`let`宣言・単純代入の値設定)がnullable変数からnullable変数への代入で`_isset`フラグを常に`true`に固定してしまっていたバグに気づいた(`let x: int? = none; let y: int? = x`のとき、`y`が`x`の実際のnone状態を無視して常に「値あり」になっていた——Step2から存在していた欠陥)。関数引数と全く同じ「値+issetの2オペランド」を必要としていたため、共通ヘルパー`genNullableOperands`を新設し(`none`→ゼロ値+false、既存のnullable変数→そのままValOp/SetOpを転送、その他の非nullable値→値+true)、`genInit`と`genCallArgs`の両方から使うようにした。`TestGenerate_NullablePropagationThroughAssignment`で回帰テストを追加し、実地でも確認済み(`y is none`が正しく`true`になる)。

**既知の未対応事項として記録**: 関数本体の全パスがreturnで終わっているかの検証(Goの`missing return`相当)は実装していない。この解析はGo自身のアルゴリズムも決して単純ではなく、Step7の本題(複数戻り値・関数呼び出し)から外れるため見送った。該当するコードを書いた場合、Cascade自身の分かりやすいエラーではなく、amivmの`go/types`経由の`missing return`エラーとして表面化する。将来必要になれば`internal/sema`に制御到達性解析を追加する。

### 構造体・ポインタ・レシーバー関数の設計(Step 8)

**レシーバー関数は、レシーバーを第一引数とする普通の`FUNC`として`StructName_Method`(例: `!Point_scale`)という名前で生成する。** オープンな設計課題の課題1で候補として挙げていた方針そのままで確定した。`obj.method(args)`は`ast.CallExpr`に`Receiver`フィールドを追加する形で表現し(パラレルな`MethodCallExpr`型は作らない)、sema側は`checker.methods map[string]map[string]methodSig`(構造体名→メソッド名→シグネチャ)でレシーバーごとに独立した名前空間を持たせた——これにより、あるstructのメソッドと通常関数、あるいは別のstructの同名メソッドが自由に同じ名前を使える。codegen側も`funcSig`の入れ子mapとして同じ形の独立テーブルを持つ(sema・codegen間の非依存を維持)。`FNTYPE`+`FGET`によるメソッド値の動的取得(test_ir `16_method_call.ir`)は今回使わなかった——Cascade組み込みのレシーバー関数はコンパイル時に静的解決できるため不要と判明した。外部Goパッケージ型のメソッド呼び出しが必要になった時点で改めて検討する。

**値レシーバー/ポインタレシーバー間の自動アドレス取得・デリファレンスは、sema側で完全に解決してからcodegenに渡す。** `resolveMethodCall`がレシーバー式の実際の型(値かポインタか)とメソッドの宣言レシーバー型を比較し、食い違っていれば`ast.CallExpr`に`RecvNeedsAddr`/`RecvNeedsDeref`フラグを立てる。codegenはこのフラグを見て呼び出し直前に`ADDR`/`PGET`を1回挟むだけで、型解決ロジックそのものはcodegen側に一切持たせていない(「意味検証の責任分担」の原則通り)。

**ポインタのnull許容性はGoネイティブの`nil`で表現し、他の`T?`型が使う「値+`_isset`フラグ」ペアは一切使わない。** `ast.Type.Ptr`が設定された型は常に`Nullable: true`(構文上`?`を書けるか否かに関わらず)だが、`needsIssetSlot(t) = t.Nullable && t.Ptr == nil`という新設ヘルパーで判定を分岐させ、ポインタには`_isset`変数もCALL境界での2スロット展開も一切生成しない。`is none`/`is not none`もポインタに対しては`_isset`フラグの読み取りではなく、`nil`との`EQ`/`NEQ`直接比較にcodegen側で分岐させる(`genNullCheck`)。値レシーバー/ポインタレシーバーどちらでも`p.x`というフィールドアクセス構文が同じに書けるのと同様、この分岐が必要なのはnull判定の1箇所だけで、`FSET`/`FGET`によるフィールド読み書き自体はGo自身が値/ポインタを透過的に扱ってくれるため**codegen側に特別な分岐は一切不要**だった(想定通りの単純化)。

**構造体リテラルは宣言されている全フィールドを明示する形のみ許可し、部分初期化(Goのゼロ値埋め)は今回サポートしない。** 一方`let p: Point`(初期化式なし)は§4.2の「宣言のみ。ゼロ値で初期化される」規則を構造体にも適用し、フィールドを1つずつ`FSET`でゼロ値に設定する`genStructZeroReset`を新設した——AMIVM-IRには「ゼロ構造体リテラル」に相当するSETトークンが無いため。ネストした構造体フィールド(例: `struct Line { start: Point, end: Point }`の`start`)についても、**再帰的なゼロリセットを実装できた**(当初CLAUDE.mdの実装ステップ計画では「ネストは対象外」を想定していたが、実装時に「ネストしたフィールドを一旦フレッシュな一時変数へ`FGET`→再帰的にゼロリセット→`FSET`で書き戻す」という手順がGoの構造体が値型であることを利用して素直に組めると分かり、想定より広くカバーできた)。ただしnull許容な非ポインタ構造体フィールド(`x: int?`のような、structのフィールドに`_isset`ペアが必要になるケース)はsemaで明示的に未対応エラーにしている——仕様例に出てこず、構造体1つのフィールドに2つのGoフィールドを対応させる仕組み(`STTYPE`の`FIELD`宣言を2行に増やす必要がある)が必要になり、Step7のnullable戻り値と同じ理由で先送りにした。

**amivmの`ADDR`/`PGET`/`PSET`/`FSET`のオペランドカテゴリが「裸の変数のみ」("variable"/"single"カテゴリ、`$N`/`&N`/`%xxx`/`@xxx`)である制約を実装時に発見し、`&x`(アドレス取得)が有効な対象をGoより狭く、**単純な識別子のみ**に制限した。** 仕様の優先順位表(6節)は`&`をGoと同様に一般的なアドレス取得可能式(変数・構造体フィールド・リスト要素)に使えるかのように読めるが、`amivm/docs/amivm_spec.md`の`ADDR single variable`の`variable`カテゴリは`$N`/`&N`/`%xxx_123`/`@xxx_123`のみを許し、`p.x`のようなフィールドパスや`xs[0]`のような添字式を直接ADDRの対象にはできない(Goの`&p.x`自体はGoとしては合法だが、amivm-IRレベルでそれを表現する手段が無い)。そのためsemaの`isAddressable`は`*ast.Ident`のみを真とする(実装当初は`FieldExpr`/`IndexExpr`も含める設計だったが、コード生成時にこの制約に気づいて修正した)。ポインタレシーバーメソッドの自動アドレス取得も同じ制約を受け、値変数がプレーンな識別子である場合のみ有効(`pt.scale(...)`は可、`someFunc().scale(...)`や`xs[0].scale(...)`は「アドレスを取れない」エラーになる)。一方`PGET`/`PSET`の対象オペランドも同じ「裸の変数」制約を受けるが、こちらは常に自動的に満たされる——`genValue`はどんな式も最終的に既存変数か新規一時変数のどちらかのトークンを返す設計になっているため、コード生成側で特別な配慮は不要だった。

**副産物のバグ修正(3件目)**: 実装・検証の過程で2つの既存バグを発見・修正した。(1) `typeGiven`(型注釈が明示されているかを判定するヘルパー、Step 6由来)が`t.Ptr`をチェックしておらず、`let q: *Point = none`のような宣言が「型注釈なし、`none`から推論しようとして失敗」という誤ったエラーになっていた——`t.Elem != nil`と同様に`t.Ptr != nil`もチェックするよう修正。(2) `narrowedVarInfo`(if文の条件からの型絞り込み、Step 5由来)が、絞り込み後の型を`ast.Type{Name: orig.Type.Name, Nullable: false}`という形で再構築する際、ポインタ型は`Name`が空文字列で`Ptr`フィールドに情報を持つことを考慮しておらず、`if p is not none { ... }`(pはポインタ型)で絞り込みが働くと`Ptr`が失われた空の`ast.Type{}`になり後続の型チェックが「型が空文字列」という意味不明なエラーで壊れていた——修正として、ポインタ型はそもそも絞り込みの対象から除外した(ポインタは絞り込んでも`*T`のまま変わらず、絞り込みによる型変化自体が意味を持たないため、除外が正しい設計判断でもある)。両方とも`examples/08_structs.cas`の実地検証中に発見し、`TestGenerate_PointerNullCheckUsesEQNilNotIssetFlag`等の回帰テストを追加した。

`examples/08_structs.cas`(`Point`/`Line`構造体、値・ポインタレシーバー双方のメソッド、フィールド読み書き、ポインタのアドレス取得・デリファレンス・null判定、ネストした構造体のゼロ初期化)で`amivm`→`go build`→実行まで確認済み。

### クロージャー・高階関数の設計(Step 9)

**関数値(`func(...): R`型)のnull許容性は、ポインタと全く同じ方針でGoネイティブの`nil`で表現する。** `ast.Type.Func`が設定された型は常に`Nullable: true`(構文上`?`を書けるか否かに関わらず、2.2節の「ゼロ値`none`」規定通り)で、`needsIssetSlot`(Step 8で新設したヘルパー)の除外条件に`t.Func == nil`を追加するだけで、`_isset`フラグ・CALL境界の2スロット展開のどちらも一切不要になった。`is none`/`is not none`も同様にポインタと同じ「`nil`との`EQ`/`NEQ`直接比較」codegenパスに乗る。型絞り込み(`narrowedVarInfo`)からもポインタと同じ理由(絞り込んでも`func(...): R`という型自体は変わらない)で除外した。

**クロージャーは周囲のスコープを「実際に共有する子スコープ」としてコンパイルすることで、捕捉(キャプチャ)専用の機構を一切実装せずに済んだ。** `CLOS`本体をコンパイルするcodegen側の`funcGen`は、`decls`/`b`/`seq`/`labelSeq`/`breakStack`/`continueStack`は完全に独立した(クロージャー自身のGo関数リテラルとしての)新規インスタンスにする一方、`scope`だけはクロージャーリテラルが出現した時点の外側スコープ`newScope(g.scope)`にした。これにより、クロージャー本体が外側の変数を参照すると`scope.lookup`が外側スコープまで辿って**同じ`%xxx`/`$N`トークン**を返し、Goのクロージャーが変数を値コピーせず参照するのと同じ効果が、コピーや専用命令なしに得られる。sema側も対称的で、`checkClosureLit`が`newScope(sc)`(親を持つ子スコープ)でボディを検査するだけで、代入・複合代入・インクリメントを含めて既存のスコープ解決ロジックがそのまま「捕捉した変数の読み書き」を正しく扱った。`makeCounter`(捕捉した`count`をクロージャー内で`+= 1`し続ける、`amivm/test_ir/15_closure.ir`の`%count`パターンそのもの)を実地検証し、複数回の呼び出しで正しく1,2,3…と増加することを確認済み。

**`break`/`continue`とnull許容戻り値は、クロージャー境界でリセットする(Goの`func`リテラルの境界と同じ扱い)。** `checkClosureLit`は`checkStmtsIn`を`loopDepth=0, breakDepth=0`で呼ぶため、クロージャー内の`break`はクロージャーの外のループへは決して届かない(Goの`func`リテラル内の`break`が外側のループに影響しないのと同じ)。戻り値のnull許容性チェックも通常の関数と同じ`needsRetIssetExpansion`(下記参照)を使う。

**amivmはCLOSのネストを禁止している(`amivmはFUNC本体内にのみ出現し、ネスト不可`)ため、クロージャーリテラルの中にさらにクロージャーリテラルを書くことは今回サポートしない。** `checker.closureDepth`という単純なカウンタ(クロージャー本体を検査している間だけインクリメント)でこれを検出し、ネストが見つかったら明示的な「amivmのCLOSはネストできない」というCascadeレベルのエラーを出す(amivmの`IRパースエラー`という分かりにくい形で表面化させない)。カリー化などネストしたクロージャーリテラルが必要になる書き方は、現状の言語機能では表現できない既知の制限として記録する。

**呼び出し可能な対象(callable)の解決順序を「メソッド → クロージャー変数 → 組み込み関数 → 通常関数」の優先順位に統一した。** これにより、10節の変数シャドーイング規則が「呼び出し可能」という名前空間にも自然に拡張され、ローカルのクロージャー変数が同名の組み込み関数(例えば独自の`filter`という名前のクロージャーを定義した場合)や同名のグローバル関数を隠せる。この解決ロジックは`resolveClosureCall`という1つのヘルパーに集約し、`checkCallExprValue`(値として使う呼び出し)・`checkExprStmt`(文としての呼び出し)・`callSig`(複数戻り値の`let`)の3箇所全てから呼ばれる。

**`filter`/`map`/`reduce`は`amivm`の`SLICE`命令の初めての実用例になった。** `map`/`reduce`は結果の要素数が入力と同じ(`map`)か単一のアキュムレータ(`reduce`)なので通常の`SLMAKE`+ループで素直に書けたが、`filter`は一致する要素数が実行時までわからない。当初は「一致件数を数えるループ→正確なサイズで`SLMAKE`→再度ループして詰める」という2パス方式を検討したが、**述語(predicate)クロージャーが副作用を持ちうる**(捕捉した変数を書き換える、`print`を呼ぶ等)ため、2パスだと同じ要素に対して述語が2回評価されてしまい、副作用がある場合に不正な結果になるという設計上の欠陥に気づいた。最終的に「入力と同じ長さで`SLMAKE`(最悪ケース分を確保)→1パスで走査し、一致した要素だけを先頭から詰めていく→実際の一致件数で`SLICE`により末尾を切り詰める」という1パス方式を採用し、述語の評価回数を要素数と正確に一致させた。CLAUDE.mdの命令使用ゴール表が元々`SLICE`について「`append`/`range`/`collect`の内部実装で自然に使える箇所を探る」としていた通りの結果になった。

**関数値を渡す手段はクロージャーリテラル(またはクロージャー型の既存変数)に限定し、トップレベルの通常関数を名前で直接値として渡すことは今回サポートしない。** amivmの値オペランドカテゴリ(`value1`/`value2`)は`$N`/`&N`/`%xxx`/`@xxx`/リテラル/`nil`のみを許し、`!funcName`(関数名トークン)は含まれない——`SET %f !someFunc`のような代入はamivmのIRとして表現できない。そのため`filter(numbers, isEven)`のように既存のトップレベル関数を名前で渡す書き方は、`isEven`が変数スコープに存在しないため「undefined name」相当のエラーになる(意図的な制限であり、バグではない)。将来必要になれば、トップレベル関数を暗黙にクロージャーへラップするsemaの脱糖処理を追加することで対応できる見込み。

**関数呼び出しの`callname`オペランドカテゴリが「`%xxx`/`@xxx`/`!xxx`/`?xxx`のみ」で、関数引数(`$N`)やクロージャー引数(`&N`)を直接呼び出し対象にはできないという制約を、実際に`amivm`へIRを通すまで気づかなかった。** `amivm/test_ir/15_closure.ir`の例は常にローカル変数(`%adder`)に代入されたクロージャーを呼び出しており、関数の引数として受け取ったクロージャーを直接呼ぶケースを示していなかったため、設計段階の文書調査だけでは見落とした。`applyTwice(f: func(int): int, x: int): int { return f(f(x)) }`のようなプログラムを実際に`amivm`へ通したところ、「呼び出し対象にこの形式は使えません: $1」というパースエラーで発覚した。修正として、クロージャー値を呼び出す全ての箇所(`genClosureCallValue`/`genClosureCallStmt`、および`filter`/`map`/`reduce`の関数引数)で、呼び出し対象のトークンが`%`または`@`で始まっていない場合は、まず新しい一時変数へ`SET`でコピーしてから呼び出す`closureCallTarget`ヘルパーに統一した。**この一連の経緯は「ロジック上正しそうに見えても実地検証なしでは信用しない」(seed_implementation_notes.md §6.1)を改めて裏付けるものだった**——設計段階で見つけた唯一の実例(test_ir)を鵜呑みにせず、自分たちのユースケース(関数を引数で受け取って呼ぶ)で実際に検証したことで発見できたバグ。

**副産物のバグ修正(4件目、Step 8由来の既存バグ2件を追加で発見)**: Step 9の実装・検証中に、ポインタ絡みで2件の既存バグを新たに発見・修正した。(1) `typeShapeEqual`(2つの型が構造的に同じかを判定するヘルパー、Step 3由来)が`Elem`(リスト要素型)しか再帰比較しておらず、`Ptr`・`Func`を全く見ていなかった——2つの異なるポインタ型(例: `*Point`と`*int`)がどちらも`Name`が空文字列になるため、常に「型が同じ」と誤判定されていた。関数型の導入で同じ問題が顕在化する前に、`Ptr`・`Func`も`Elem`と同様に再帰比較するよう修正した。(2) `typeString`(エラーメッセージ用に型を文字列化するヘルパー)が同じ理由で`Ptr`を全く考慮しておらず、ポインタ型が関わるエラーメッセージが型情報の全く無い`"?"`という表示になっていた(`let x: string = p`という誤りが`"cannot assign ? to non-nullable string"`という不可解なメッセージになることを実地確認)——`Ptr`/`Func`を明示的に処理し、`*T`・`func(T1, T2): R`という読める形式で表示するよう修正した。(3) `checkFuncSig`のnullable戻り値禁止チェック(Step 7由来)が`r.Nullable`を無条件に見ており、ポインタ・関数型のように「常にnullableだが値+`_isset`展開が不要な型」まで一律で禁止していた——`makeAdder(base: int): func(int): int`のような、Step 9の核心となる書き方そのものが「nullable return types are not supported yet」で弾かれてしまっていた(Step 8でもポインタを返す関数を1つも例示していなかったため見逃されていた)。`needsRetIssetExpansion`という新しいヘルパー(`needsIssetSlot`と同じ考え方をRET/CALL境界に適用したもの)を導入し、`checkFuncSig`と`checkClosureLit`の両方をこちらに切り替えた。(4) `binaryResultType`(二項演算子の型チェック、Step 3由来)の「nullableオペランドは全面禁止、絞り込んでから使うこと」という一律チェックが、ポインタ・関数型に対しては絞り込み不可能な指示を出す誤ったエラーメッセージになっていた上、Goでは合法な**ポインタの`==`/`!=`比較(アドレス比較)まで巻き込んで禁止してしまっていた**——関数型の`==`比較を禁止する専用ガードを追加している最中に気づいた。ポインタの`==`/`!=`は特別に許可し、関数型の`==`/`!=`(Goでも`nil`以外との比較は不可)は明確なエラーメッセージで禁止し、それ以外の演算子でのポインタ・関数型の使用も「絞り込め」ではなく「この演算子はポインタ/関数値をサポートしない」という正しいメッセージへ修正した。全て回帰テスト(`TestCheck_ValidPointerEqualityComparison`等)を追加済み。

`examples/09_closures.cas`(`makeAdder`/`makeCounter`によるクロージャー生成・捕捉変数の書き換え、`applyTwice`による関数値の引数渡し、`filter`/`map`/`reduce`の3組み込み関数)で`amivm`→`go build`→実行まで確認済み。

### map(`map<K, V>`)の設計(Step 10)

**`map<K, V>`自体のnull許容性はリストと同じ方針にした(ポインタ・関数型とは異なる)。** 2.2節でmapのゼロ値は`{}`(実体を持つ空map)と明記されており、ポインタ・関数型のような「常に`none`」ではない。そのため`ast.Type.Map`が設定されていても`Nullable`は強制せず、`map<K, V>?`と明示的に書かれたときだけ、リストや他の通常の型と全く同じ`_isset`フラグ機構に乗る(`needsIssetSlot`の除外条件にMapは追加していない)。

**非nullableなmapの「宣言のみ」(`let empty: map<string, int>`)は、`SET nil`ではなく`MPMAKE`で実体のある空mapを確保する。** Goのnil mapは読み取り(`len`・`m[k]`・range)は安全だが**書き込みはpanicする**——リストの場合はnilスライスへの`append`が安全なため`zeroValueLiteral`の`nil`を使い回せたが、mapは同じ理由が成立しない。そこで`genResetToZero`にmap専用の分岐を追加し、非nullable・かつ初期化式が無い場合は`MPMAKE`を発行するようにした。一方nullableなmap(`map<K, V>?`、宣言のみだと`none`)は`nil`のままで問題ない——安全性の根拠は、「mapへの書き込みが許されるのは絞り込み(`is not none`)で非nullable化した後だけ」というsemaの既存規則(`checkAssignStmt`の`stmt.Index`分岐が`info.Type.Nullable`を見て弾く、Step 6由来)と、「`isset`が`true`になる代入経路(マップリテラルの`genMapLiteralInit`、または既存の妥当な値の伝播)は必ず実体のある`MPMAKE`済みmapを伴う」という帰納的な不変条件の組み合わせによって、`isset=true`なのに中身が`nil`という状態が構造的に発生し得ないことによる。

**`m[k]`(`V?`を返す添字読み取り)と`xs[i]`(リストの添字読み取り、`T`を返す)は、`ast.IndexExpr`という同じASTノードを共有しつつ、`ResultType.Nullable`だけで判別する設計にした。** 新しい判別用フィールドは追加していない——リストの要素は仕様上決して独立してnullableになれない(Step 6で確定済み)ため`ResultType.Nullable`は常に`false`、mapの読み取りは`MGET`のcomma-ok由来のnullabilityにより常に`true`になることが構造的に保証されているため、この1フィールドだけで安全に分岐できる。codegen側の`genIndexRead`はこれを見て`AGET`(リスト)か値のみの`MGET`(map、isset非対応の文脈用)を選び、`func.go`の`genNullableOperands`は同じ`ResultType.Nullable`を見て、nullable文脈(`let v: int? = m[k]`等)では`MGET`のcomma-ok形(値+okの2オペランド)を1回の命令で両方取得する専用パスに乗せる。

**map value型(`V`)は独立してnullableにできないという制約をsemaで明示的に禁止した(`validateType`)。** `m[k]`が返す`V?`自体が既に「キーが存在するか」という1段のnullabilityを表現しているため、`V`自身もnullable(`map<string, int?>`)だと二重のnullabilityが必要になり、`MGET`のcomma-ok形(値+okの2オペランドのみ)では素直に表現できない。仕様例にも二重nullableの用例が無いため、Step 8の「構造体フィールドの独立nullable禁止」と同じ理由・同じ判断で先送りにした。mapのキー型(`K`)についても、4.5節が明記する「スカラー型(int/float/string/bool)のみ」という制約を`validateMapKeyType`で検証している。

**AMIVM-IRにはmapを走査する命令が存在しない(`RANGE`相当が無い)ため、`for k, v in m`の実装だけ他の3命令(`MPTYPE`/`MPMAKE`/`MSET`/`MGET`)と全く異なるアプローチが必要だった。** 単独のキー・値へのアクセス(`MGET`/`MSET`)はできても、mapの全エントリを列挙する手段がAMIVM-IR自体には無いため、Cascade側のコード生成だけでは`for k, v in m`を組み立てられない。この設計判断はユーザーに確認を取り、**今回`cascadert`ランタイムを新設して解決する方針**を選んだ(先送りにする選択肢もあったが、初回導入のコストを払ってでも今回のStep 10で完結させることにした)。`cascadert.Keys[K comparable, V any](m map[K]V) []K`(Go 1.18+のジェネリクス)を1つ実装し、`for k, v in m`は「`cascadert.Keys`でキー一覧を`[]K`として取得→既存のリストfor-inと全く同じカウントループでキーを走査→各キーごとに`MGET`で値を取得」という、リストfor-inの上に薄く乗せる形にした。ジェネリック関数が`MPTYPE`の生成する名前付きmap型(`type MapType1 map[string]int`のような)からも正しく型推論できることは、実装に入る前に独立した最小限のGoプログラムで検証済み(`go run`で`Keys(namedMapValue)`が動くことを確認してから本実装に着手した)。

**`cascadert`ランタイムの配布はSeedの`seedrt`と完全に同じ方式を踏襲した。** `cascadert/embed.go`が`//go:embed *.go`で自身のソースを埋め込み、`cmd/cascade/build.go`の`writeCascadert`(`seed/cmd/seed/build.go`の`writeSeedrt`をほぼそのまま移植)がビルド時にスクラッチモジュール配下の`cascadert/`へコピーし、`amivm`に`-i cascadert=cascadebuild/cascadert`を渡す。`-i`は`cascadert`を実際に使わないプログラムに対しても無条件に渡してよい(未使用のimportマッピングはamivmが自動的に除去する、既にSeedで確認済みの挙動)。

**`delete(m, key)`はGoの組み込み`delete`をそのまま`CALL : ?delete`で呼ぶだけで実現でき、専用命令もcascadertも不要だった。** `len`が`?len`でGoの組み込み`len`を呼べるのと全く同じパターンで、`delete`もGoのuniverse-scopeビルトインなので`?xxx`+`CALL`の仕組みでそのまま届く。

`examples/10_maps.cas`(map リテラル・`m[k]`のnull許容読み取り・`m[k]=v`・`delete`・非nullable/nullable両方の空map宣言・`for k, v in m`の2変数走査)で`amivm`→`go build`→実行まで確認済み。

## 意味検証の責任分担(重要)

型の整合性・未定義識別子・関数シグネチャの不一致・メソッドの存在チェックなどは、**amivm側では検証せず`go/types`に全面的に委ねている。** amivmが保証するのは「構文的に妥当なGoコードを出力すること」だけ。

これはつまり、**Cascadeの意味検査(型チェック・スコープ解決・null許容型の絞り込み・パイプラインの型接続検査など)はCascadeコンパイラ自身が担う必要がある** ということ。AMIVM-IR生成前の段階で、Cascade言語仕様(`cascade_spec.md`)に基づく検査を完了させておくこと。IRを間違って生成した場合のエラーは、amivmの`go/types`型チェック失敗という形で(生成されたGoコードのエラーとして)返ってくるため非常にわかりにくい。**Cascadeコンパイラ自身が、ユーザーに分かりやすいエラーメッセージをamivm呼び出し前に出せるようにすること**が望ましい。

なお、amivmは生成コード中の未使用変数を自己修復する仕組み(`_ = 変数名`の自動挿入)を内部に持っている。**これはamivm側の責務であり、Cascadeのcodegenが一時変数の使用漏れを気にする必要は無い。**

## 独自のGoランタイムを呼ぶ

`amivm`は`?pkg.Func`+`CALL`の仕組みで任意のGo関数を呼べる。Cascadeのビルトイン関数(`sqrt`/`args`等)のうち、単純な演算命令に対応しないものは、Cascade自身が用意するGoランタイムパッケージ`cascadert`(Seedの`seedrt`に相当)の関数として実装し、生成したIRから`CALL`で呼び出す。Step 10(map)で`for k, v in m`の実装のために初めて必要になり、Seedの`seedrt`と全く同じ方式で導入済み: `cascadert/embed.go`が自身の`.go`ファイルを`go:embed`で埋め込み、`cmd/cascade/build.go`の`writeCascadert`(`seed/cmd/seed/build.go`の`writeSeedrt`を忠実に踏襲)が`cascade build`実行時にスクラッチビルド用ディレクトリ配下の`cascadert/`へコピーし、`amivm`の`-i cascadert=cascadebuild/cascadert`で解決する(`-i`は未使用でも無害なので毎回無条件に渡してよい)。現在の`cascadert`の中身は`Keys[K comparable, V any](m map[K]V) []K`(Go 1.18+のジェネリクスを使い、`MPTYPE`が生成するどんな名前付きmap型に対しても1つの実装で動く。named map型からの型推論が実際に効くことは実装前に独立した最小Goプログラムで検証済み)のみ。新しいビルトインが必要になるたびにこのパッケージへ関数を追加していく。

## 過去に踏まれた地雷(Seedからの申し送り。Cascadeでも起こりうる)

詳細は`seed_implementation_notes.md`を参照。特に重要なものを抜粋。

1. **goto/VAR巻き上げ問題(最重要)**: `IF`/`GOTO`が生成するGoコードは1関数内のフラットな命令列であり、`goto`は「まだスコープに入っていない変数宣言」を飛び越えられない(Goのルール)。対象言語の関数・クロージャー・**ステージ関数**ごとに、使う`VAR`を全て先頭に巻き上げ、初期化は元の位置に`SET`だけ残すこと。`CLOS`本体も独立した1関数スコープなので同様の巻き上げが必要
2. **スコープはGo側で完全にフラット**: シャドーイングを許す言語仕様(10節)に対して、codegenは内部で一意な変数名を採番すること
3. **`CALL`はキャストにも使われる**: `string(intVal)`のような数値→文字列変換はGoの素の型変換だと`"A"`のようなルーン変換になる罠がある(`strconv`を使うこと)
4. **`CALL`の結果省略は本当に省略する**: 空文字列オペランドではなく`CALL : callname ...`のように空にする
5. **参照渡し/値渡しはGo表現に従う**: スライス・ポインタ・map・チャネル・関数値はコピーせずそのままローカル変数として使い回す。スカラーはコピーしてよい
6. **`fmt.Sprintf`で動的にIR行を組み立てる際、オペランド中の`%`をフォーマット文字列に直接連結しない**(`%s`引数として渡す)
7. **`STTYPE`/`CLOS`/`SEL`はネスト不可**。関数内ローカルな構造体定義を許す言語仕様なら、全てトップレベルへ引き上げる必要がある(Cascadeの`struct`は元々トップレベル宣言のみなので該当しない見込みだが、クロージャー内で新たな構造体を作る発展的な構文を将来追加する場合は要注意)

## リポジトリ構成(予定・未実装)

**`/workspaces/cascade`(このディレクトリ自体)がCascadeのホーム。** Seed(`seed/`)は自分自身が独立リポジトリのルートで、`amivm/`をその内部に参照用クローンとして置く構成だったが、Cascadeでは`cascade_spec.md`・`seed_implementation_notes.md`・`seed/`・`amivm/`が既にこの階層に揃っているため、Cascade本体の実装(`go.mod`以下)もこの直下に追加していく。実装が進むにつれ実態に合わせて更新すること。

```
/workspaces/cascade/            Cascadeのホーム(このリポジトリのルート)
  cascade_spec.md               Cascade言語仕様(唯一の正)
  seed_implementation_notes.md  Seed実装時の知見
  CLAUDE.md                     本ファイル
  seed/                         Seedの参考実装(参照用。Cascade本体の一部ではない)
  amivm/                        amivmのローカルクローン(参照用。Cascade本体の一部ではない)
  README.md / README_ja.md      導入ドキュメント(実装が固まってから作成)
  go.mod                        module github.com/amisonnet8/cascade (想定)
  Makefile                      build/install/test/fmt/vet/tidy/clean タスク
  cmd/cascade/
    main.go                     CLIエントリポイント(build/run/emit-ir/emit-go/help のディスパッチ、想定)
    build.go                    compileToIR → compileToGo → compileToBinary の3段パイプライン
  internal/lexer/               字句解析
  internal/parser/              構文解析 → AST
  internal/ast/                 AST定義
  internal/pkgloader/           パッケージ/importの解決(11節。Seedには無かった新規レイヤー)
  internal/sema/                意味検査(型チェック・スコープ解決・null絞り込み・パイプライン型接続検査)
  internal/codegen/             AST → AMIVM-IR生成
  cascadert/                    Cascadeランタイム(Step 10で導入済み。現状は map<K, V> の
                                 for-in走査に使うKeysのみ。sqrt/args等、単純な演算命令に
                                 対応しないビルトインを今後ここに追加していく想定)。
                                 生成されたGoコードからimportされるため internal/ 配下には置けない
  examples/                     サンプルCascadeプログラム(`.cas`。実装した構文ごとに追加)
```

## 実装ステップ計画(15ステップ)

Seedは7〜8ステップ(git履歴上は「Step1: hello-worldパイプライン疎通」〜「Step8: CLI/配布」)で実装した。Cascadeはポインタ・構造体・クロージャー・map・エラー処理・パイプライン・パッケージという、Seedに無かった複雑さを持つため、15ステップに分割する。各ステップは「機能単位の縦切り+都度amivm→go build→実行で実地検証」というSeedの開発プロセス(seed_implementation_notes.md §6.1)を踏襲すること。

| # | ステップ | 主な内容 | 実証する命令 | 解決するオープン課題 |
|---|---|---|---|---|
| 1 ✅ | ブートストラップ | lexer/parser/ast/codegen最小構成。`func main(): int { print("Hello, Cascade!") return 0 }`をamivm→go build→実行まで通す | `FUNC` `RET` `ENDFUNC` `CALL`(`print`) | `main`/`cascade_main`ブリッジ方針を新規に確定(上記「確定した設計判断」参照) |
| 2 ✅ | 変数・スカラー型・null許容型 | `let`/`const`、`int`/`float`/`string`/`bool`、`T?`の値+成否フラグペア(`is none`/`is not none`自体は`if`が無いと使い道が無いためStep5に先送り) | `VAR` `SET` | `T?`の実装方式を確定(下記「確定した設計判断」参照) |
| 3 ✅ | 演算子(算術・比較・論理・文字列) | `+ - * / %`、`== != < <= > >=`、`&& \|\| !`、`string`連結、優先順位表(6節)の実地検証。観測用に組み込み変換`string()`(13節)も先取り実装 | `ADD` `SUB` `MUL` `DIV` `MOD` `EQ` `NEQ` `LT` `LTE` `GT` `GTE` `AND` `OR` `NOT` `CONCAT` | — |
| 4 ✅ | ビット演算・シフト演算 | `&` `\|` `^` `&^` `~`、`<<` `>>`(int専用、semaで型制約を検査)。優先順位表(シフトが`+`/`-`より低優先度という非直感的な並び)も実地検証 | `BAND` `BOR` `BXOR` `BCLEAR` `BNOT` `SHL` `SHR` | — |
| 5 ✅ | 制御構文 | `if/elif/else`、`while`、`switch`(タグ付き/タグなし)、`break/continue`、`is none`/`is not none`と型絞り込み(Step2から持ち越し)。`+=`等の複合代入・`++`/`--`も前倒し実装。`for-in`は`[]T`(Step6)に依存するためStep6へ移動 | `LABEL` `GOTO` `IF` | goto/VAR巻き上げ問題(seed_implementation_notes.md §1)の再検証(下記「確定した設計判断」参照) |
| 6 ✅ | リスト(`[]T`) | リテラル・`append`・添字読み書き・`for x in xs`・`range`/`len`組み込み | `SLTYPE` `SLMAKE` `ASET` `AGET`(`SLICE`の使い所も探る) | — |
| 7 ✅ | 関数(通常関数・複数戻り値) | `func`定義、複数戻り値(8.1/8.5節)、`divmod`的サンプル | `FUNC` `RET` `CALL`の本格利用 | nullable戻り値は未対応と確定(下記「確定した設計判断」参照) |
| 8 ✅ | 構造体・ポインタ・レシーバー関数 | `struct`定義・フィールドアクセス、`&x`/`*p`、値/ポインタレシーバーの自動変換 | `STTYPE` `FIELD` `ENDSTTYPE` `FSET` `FGET` `ADDR` `PGET` `PSET` | レシーバー関数のコンパイル方針を確定(下記「確定した設計判断」参照。旧「オープンな設計課題」課題1) |
| 9 ✅ | クロージャー・高階関数 | クロージャーリテラル(8.3節)、`filter`/`map`/`reduce`(8.4節)、参照捕捉の実地検証 | `FNTYPE` `CLOS` `ENDCLOS` | — |
| 10 ✅ | map(`map<K, V>`) | リテラル・`m[k]`(`V?`化)・`m[k]=v`・`delete`・`for k, v in m` | `MPTYPE` `MPMAKE` `MSET` `MGET` | `cascadert`ランタイムの初回導入(下記「確定した設計判断」参照) |
| 11 | エラー処理 | `error`型・`(T, error?)`規約・後置`?`のsema展開(8.6節) | (新規命令なし。`STTYPE`+`IF`+`RET`の組み合わせ) | 課題2(`error`型の表現)の確定 |
| 12 | パイプライン基礎 | `source`/`stage`/`sink`、`chan<T>`、`send`、`for v in channel`、`\|>`連結(9.1/9.2節) | `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` `SPAWN` | 課題1(並行実行モデル)の一次決定。`amivm/test_ir/11_spawn_channel_sel.ir`必読 |
| 13 | パイプライン拡張(collect/abort/merge) | `collect`(9.3節)・`abort`(9.4節)・`merge`(9.5節) | `SEL` `CASESEND` `CASERECV` `DEFAULT` `ENDSEL` `DEFER` | 課題1の最終確定 |
| 14 | パッケージ/複数ファイル | ディレクトリ=パッケージの統合(11.1節)、`import`/`pub`(11.2/11.3節)、循環import検出(11.5節)、識別子一意化(11.6節) | (新規命令なし。codegenの命名規則) | 課題3(パッケージ/モジュール解決)の確定 |
| 15 | CLI・配布 | `cascade build/run/emit-ir/emit-go/help`、`cascadert`の`go:embed`配布、README作成 | — | — |

特にStep4(ビット演算)・Step8(ポインタ・構造体)・Step9(クロージャー)・Step10(map)・Step12/13(チャネル・SPAWN・SEL)はSeedで未実証だった命令なので、「ロジック上正しそうに見える」だけで次のステップへ進まないこと。上記「オープンな設計課題」の5項目は、対応するステップ着手時に方針を確定し、その節を書き換える(仮説のまま放置しない)。

## 開発の進め方

1. `cascade_spec.md`を正として実装する。仕様に曖昧な点や矛盾を見つけたら、まず仕様側を疑い、確定させてからコードを直す
2. 新しい命令カテゴリ・構文を実装したら、実際に`amivm`(`PATH`にインストール済みのもの)にかけて`go build`まで通し、動作確認する。特にポインタ・構造体・map・クロージャー・チャネル/SPAWN/SELは**Seedで未実証だった命令**なので、「ロジック上正しそうに見える」だけで済ませない(seed_implementation_notes.md §6.1参照)
3. Cascadeの意味検査(型チェック・null絞り込み・パイプライン型接続等)は、amivmに渡す前にCascade側で完了させる。amivmの`go/types`エラーをユーザー向けエラーとしてそのまま出さない
4. 新しい構文・ビルトイン関数を実装したら、対応するサンプルCascadeプログラムを`examples/`に追加し、生成されたIR・Goコード・実行結果まで確認する
5. 上記「オープンな設計課題」の各項目は、着手時に方針を確定させ、確定内容をこの節(または実装コード側のdocコメント)に書き残す。仮説のまま放置しない
6. 新しい命令を使うたびに「命令使用ゴール」節の表を更新し、全命令の使用状況を追跡する
7. amivm本体の仕様が更新された場合(`amivm/docs/amivm_spec.md`を再確認)、本ファイルの「AMIVM-IRの書き方」節が古くなっていないか照合し、必要なら更新する
