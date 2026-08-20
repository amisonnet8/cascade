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
| `SLICE` | 対応するCascade構文が現時点で無い(添字範囲切り出し`xs[a:b]`のような構文は仕様に無い)。`append`/`range`/`collect`の内部実装で自然に使える箇所が無いか実装時に探る | **要検討** |
| `STTYPE` `FIELD` `ENDSTTYPE` `FSET` `FGET` | `struct`定義・フィールドアクセス(4.1/5節) | 確定 |
| `MPTYPE` `MPMAKE` `MSET` `MGET` | `map<K, V>`(4.5節)。`m[k]`が返す`V?`は`MGET`のcomma-ok形をそのまま値+nullフラグへ流し込める見込み(seed_implementation_notes.md §3参照) | 確定 |
| `FNTYPE` `CLOS` `ENDCLOS` | `func(...): R`型・クロージャーリテラル(8.3節) | 確定 |
| `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` | `chan<T>`・`send()`・`for v in someChannel`(9節) | 確定 |
| `SPAWN` | `source`/`stage`/`sink`を`|>`で連結した際の各段の並行実行(9.2節) | 確定 |
| `SEL` `CASESEND` `CASERECV` `DEFAULT` | `merge()`のファンイン実装(9.5節)。2チャネルのどちらか先に来た方を受け取るselect相当 | 確定(実装方針は下記オープン課題参照) |
| `DEFER` | ユーザー向け構文としては存在しない。**候補**: `source`/`stage`本体の生成コード先頭で`DEFER : ?close 出力チャネル`し、関数(=ステージ)終了時に出力チャネルを自動closeして下流の`for-in`を終了させる、というコード生成側の内部利用 | **要検討**(下記オープン課題参照) |

## オープンな設計課題(実装前に方針を確定させ、確定次第この節を書き換える)

Seedと異なりCascadeは複数ファイル/パッケージ・null許容型・並行パイプラインという、Seedに無かった複雑さを持つ。以下は現時点で未確定の、最初に潰すべき設計課題。**[[Seed]]の「確定した設計判断」節(`seed/CLAUDE.md`)に倣い、決まったらここに確定内容として書き残すこと。仮説のまま放置しない。**

### 1. レシーバー付き関数(8.2節)のコンパイル方針

Cascadeの構造体はGoの`struct`に素直に対応するが、レシーバー関数(`func (p: Point) magnitude(): float`)をGoのメソッドとして生成する必要は無いはず。**候補**: レシーバーを第一引数とする普通の`FUNC`(`!Point_magnitude`のような命名)として生成し、`obj.method(args)`という呼び出し構文を単なる`CALL !Point_magnitude obj args...`に脱糖する。`FNTYPE`+`FGET`(test_ir `16_method_call.ir`)によるメソッド値の動的取得は、`*os.File`のような**外部Goパッケージの型のメソッド**を呼ぶときにのみ必要になる想定(Cascade組み込み関数のGoランタイム実装で使う可能性はある)。値レシーバー/ポインタレシーバー間の自動アドレス取得・デリファレンス(8.2節)はsema/codegenが担う。

### 2. パイプライン(9節)の並行実行モデル

最も設計コストが高い箇所。`amivm/test_ir/11_spawn_channel_sel.ir`を実装前に必ず読むこと。

- `source`/`stage`/`sink`は各`SPAWN`されるgoroutine(`FUNC`)として生成
- 段間の`chan<T>`は`CHTYPE`+`CHMAKE`
- `for v in input`は`CHRECV`のcomma-ok形をループ条件に使い、`ok == false`(チャネルclose)でループを抜ける形になる見込み
- 各段が処理を終えたら出力チャネルをcloseして下流に伝播させる必要がある(`DEFER : ?close 出力チャネル`が候補。上記命令使用ゴール表参照)
- `collect`(9.3節): 終端チャネルを受信し続けて`[]T`に溜め込む専用のcollector goroutineを内部生成し、結果を`main`側が別チャネル経由で同期的に受け取る、という形が候補
- `abort`(9.4節): 全ステージを即座に止める必要がある。**候補**: closeすることが一度きりの安全なブロードキャストになるGoの性質を使い、専用の`chan<bool>`(またはstring)をcloseすることで中断を通知し、各ステージの`for-in`ループを`SEL`(`CASERECV input` vs `CASERECV abortChan`)に変える
- `merge`(9.5節): 2入力チャネルを`SEL`の`CASERECV`×2でファンインする専用goroutineとして実装する候補

いずれも**実装したら`amivm`→`go build`→実行まで必ず確認する**(seed_implementation_notes.md §6.1の教訓)。並行処理はロジック上正しく見えても実地検証なしでは信用しない。

### 3. `error`型(8.6節)の表現

`err.message`のようなフィールドアクセスが必要なため、Goの組み込み`error`インターフェースにマッピングするより、**`^error`という名前で`message: string`を1フィールド持つ`STTYPE`をCascadeコンパイラが自動定義する**案が有力(2.2節の説明「実体は`message: string`を1つ持つ構造体的な値」とも整合する)。後置`?`演算子(8.6節)はsemaが「戻り値の末尾が`error?`である呼び出し」を検出し、`IF`+`RET`による早期returnへ機械的に展開する(Seedのビルトイン脱糖と同じ発想)。

### 4. パッケージ/モジュール解決(11節)

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

## 意味検証の責任分担(重要)

型の整合性・未定義識別子・関数シグネチャの不一致・メソッドの存在チェックなどは、**amivm側では検証せず`go/types`に全面的に委ねている。** amivmが保証するのは「構文的に妥当なGoコードを出力すること」だけ。

これはつまり、**Cascadeの意味検査(型チェック・スコープ解決・null許容型の絞り込み・パイプラインの型接続検査など)はCascadeコンパイラ自身が担う必要がある** ということ。AMIVM-IR生成前の段階で、Cascade言語仕様(`cascade_spec.md`)に基づく検査を完了させておくこと。IRを間違って生成した場合のエラーは、amivmの`go/types`型チェック失敗という形で(生成されたGoコードのエラーとして)返ってくるため非常にわかりにくい。**Cascadeコンパイラ自身が、ユーザーに分かりやすいエラーメッセージをamivm呼び出し前に出せるようにすること**が望ましい。

なお、amivmは生成コード中の未使用変数を自己修復する仕組み(`_ = 変数名`の自動挿入)を内部に持っている。**これはamivm側の責務であり、Cascadeのcodegenが一時変数の使用漏れを気にする必要は無い。**

## 独自のGoランタイムを呼ぶ

`amivm`は`?pkg.Func`+`CALL`の仕組みで任意のGo関数を呼べる。Cascadeのビルトイン関数(`sqrt`/`args`等)のうち、単純な演算命令に対応しないものは、Cascade自身が用意するGoランタイムパッケージ(例: `cascadert`。Seedの`seedrt`に相当)の関数として実装し、生成したIRから`CALL`で呼び出す設計になる見込み。配布方法(`go:embed`でスクラッチビルド用ディレクトリへコピー→`-i`で解決)もSeedの`seedrt`と同じ方式を踏襲してよい(`seed/CLAUDE.md`「`seedrt`の配布方法」参照)。

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
  cascadert/                    Cascadeランタイム(sqrt/args等、CALLで呼ばれるGo実装)。
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
| 5 | 制御構文 | `if/elif/else`、`while`、`for-in`(range限定の単純ケース)、`switch`(タグ付き/タグなし)、`break/continue` | `LABEL` `GOTO` `IF` | goto/VAR巻き上げ問題(seed_implementation_notes.md §1)の再検証 |
| 6 | リスト(`[]T`) | リテラル・`append`・添字読み書き・`for x in xs`・`range`/`len`組み込み | `SLTYPE` `SLMAKE` `ASET` `AGET`(`SLICE`の使い所も探る) | — |
| 7 | 関数(通常関数・複数戻り値) | `func`定義、複数戻り値(8.1/8.5節)、`divmod`的サンプル | `FUNC` `RET` `CALL`の本格利用 | — |
| 8 | 構造体・ポインタ・レシーバー関数 | `struct`定義・フィールドアクセス、`&x`/`*p`、値/ポインタレシーバーの自動変換 | `STTYPE` `FIELD` `ENDSTTYPE` `FSET` `FGET` `ADDR` `PGET` `PSET` | 課題1(レシーバー関数のコンパイル方針)の確定 |
| 9 | クロージャー・高階関数 | クロージャーリテラル(8.3節)、`filter`/`map`/`reduce`(8.4節)、参照捕捉の実地検証 | `FNTYPE` `CLOS` `ENDCLOS` | — |
| 10 | map(`map<K, V>`) | リテラル・`m[k]`(`V?`化)・`m[k]=v`・`delete` | `MPTYPE` `MPMAKE` `MSET` `MGET` | — |
| 11 | エラー処理 | `error`型・`(T, error?)`規約・後置`?`のsema展開(8.6節) | (新規命令なし。`STTYPE`+`IF`+`RET`の組み合わせ) | 課題3(`error`型の表現)の確定 |
| 12 | パイプライン基礎 | `source`/`stage`/`sink`、`chan<T>`、`send`、`for v in channel`、`\|>`連結(9.1/9.2節) | `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` `SPAWN` | 課題2(並行実行モデル)の一次決定。`amivm/test_ir/11_spawn_channel_sel.ir`必読 |
| 13 | パイプライン拡張(collect/abort/merge) | `collect`(9.3節)・`abort`(9.4節)・`merge`(9.5節) | `SEL` `CASESEND` `CASERECV` `DEFAULT` `ENDSEL` `DEFER` | 課題2の最終確定 |
| 14 | パッケージ/複数ファイル | ディレクトリ=パッケージの統合(11.1節)、`import`/`pub`(11.2/11.3節)、循環import検出(11.5節)、識別子一意化(11.6節) | (新規命令なし。codegenの命名規則) | 課題4(パッケージ/モジュール解決)の確定 |
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
