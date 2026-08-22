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
| `cascade_implementation_notes.md` | **Cascade自身の実装で踏んだ地雷・確立したパターンのまとめ。AMIVM-IRを生成するフロントエンドを書くときの一般的な知見に絞ってある。** 新しいAMIVM命令カテゴリを実装する前・amivm本体の破壊的変更に追従する前に一読すること。各言語の実装知見はその言語自身のリポジトリで管理する方針のため、[[Seed]]側の知見(goto/VAR巻き上げ問題など)はこのファイルには含まれない——必要なら`github.com/amisonnet8/seed`リポジトリ自身の実装知見を参照すること |
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
- `-i`/`--import <名前>=<importパス>`は繰り返し指定できる。**Cascadeが独自のランタイムライブラリ(未使用なら消える)を呼びたい場合はこれを使う**(下記「独自のGoランタイムを呼ぶ」参照。現状Cascadeはこの機構を使っていない)
- ファイル読み込み失敗・IRパースエラー・型チェック失敗などのエラーは常に出力する
- `go build`による実行ファイル生成は行わない(別工程。Cascade側のビルドパイプラインで実行する)

## AMIVM-IRの書き方(唯一の正確な仕様)

以下は`amivm/docs/amivm_spec.md`からの要点転記。**Cascadeのコード生成部がAMIVM-IRを出力する際は、この命令セット・カテゴリ・Kind分類に厳密に従うこと。**

### 制約・前提条件

- `FUNC`はトップレベルのみに置ける(関数のネスト不可)。`STTYPE`もネスト不可。**`IF`・`LOOP`・`CLOS`・`SEL`はいずれもネストできる**(互いの本体の中に`IF`/`LOOP`/`CLOS`/`SEL`を任意の組み合わせ・任意の深さで書ける。`CLOS`のネスト深さ`L`は`FUNC`直下を1として数え、クロージャー引数`&L-N`の階層番号に対応する)
- 配列は1次元固定長のみ。多次元配列はAMIVM-IR自体では表現しない(Cascadeの`[]T`はそもそも1次元のみなので該当しない)
- チャネル・スライス・map・構造体・クロージャーは、対応する`TYPE`系命令(`SLTYPE`/`MPTYPE`/`STTYPE`/`FNTYPE`/`CHTYPE`)で型を定義してから使う
- トークンの区切り文字は**タブ**。行頭のインデント用タブは無視。`//`で始まる行はコメントとして無視

### 識別子のプレフィックス

| 記号 | 意味 |
|---|---|
| `$` | 関数引数 |
| `&` | クロージャー引数(`&N`は自分がいる`CLOS`階層のN番目、`&L-N`で階層`L`を明示指定できる) |
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
| ラベル・GOTO | `LABEL label` / `GOTO label` |
| 条件分岐 | `IF boolean1` / `ELIF boolean1` / `ELSE` / `ENDIF`(ブロック形。Goの`if`/`else if`/`else`に対応) |
| ループ | `LOOP` / `BREAK` / `CONTINUE` / `ENDLOOP`(Goの無限`for {}`。条件付きループは`LOOP`の中で`IF`+`BREAK`を組み合わせて表現する) |
| 型アサーション | `ASSERT multi1 (multi2) variable type1`(Goの`v.(T)`。`any`型相当をCascadeが持たないため対象外——下記「命令使用ゴール」参照) |
| 関数定義 | `FUNC defname type1 ... : type3 ...` / `RET` / `ENDFUNC` |
| 関数呼び出し | `CALL multi1 ... : callname value1 ...` / `DEFER` / `SPAWN` |
| チャネル | `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` |
| select | `SEL` `CASESEND` `CASERECV` `DEFAULT` `ENDSEL`(`CASESEND`/`CASERECV`/`DEFAULT`はブロック形。`label`は取らず、次のケースか`ENDSEL`までが本体) |
| スライス | `SLTYPE` `SLMAKE` `SLICE` |
| 構造体 | `STTYPE` `FIELD` `ENDSTTYPE` `FSET` `FGET` |
| map | `MPTYPE` `MPMAKE` `MSET` `MGET` `MPKEYS` |
| クロージャー・関数型 | `FNTYPE` `CLOS` `ENDCLOS` |

各命令の生成Goコード・オペランドカテゴリ(`whole`/`integer`/`value`/`single`/`multi`等)・Kind分類は`amivm/docs/amivm_spec.md`の4〜6節を参照。**キャスト・組み込み関数(`close`/`len`/`cap`等)は専用命令を持たず`CALL`に統合されている**(Goの型変換`T(v)`は構文上`ast.CallExpr`と同一のため)。型アサーション(`v.(T)`)だけは構文が異なる別ASTノード(Goの`ast.TypeAssertExpr`)になるため`CALL`に含めず`ASSERT`という専用命令になっている。

## 命令使用ゴール(Cascade機能とAMIVM命令の対応表)

「全命令を使う」目標の進捗管理表。実装が進むにつれ状態を更新すること。

| 命令 | 対応するCascade機能 | 状態 |
|---|---|---|
| `VAR` `GVAR` `SET` `ASET` `AGET` | `let`/`const`(ローカル)、`[]T`要素の読み書き。`GVAR`はトップレベル`let`/`const`(11.3節)で初めて実際に使用(Step 14) | 確定 |
| `ADD` `SUB` `MUL` `DIV` `MOD` | 算術演算子(6節) | 確定 |
| `BAND` `BOR` `BXOR` `BCLEAR` `BNOT` | `&` `\|` `^` `&^` `~`(int専用ビット演算、6節) | 確定 |
| `SHL` `SHR` | `<<` `>>`(int専用、6節) | 確定 |
| `AND` `OR` `NOT` | `&&` `\|\|` `!` | 確定 |
| `EQ` `NEQ` `LT` `LTE` `GT` `GTE` | 比較演算子。`is none`/`is not none`もnull許容型の内部フラグ比較として実装できる見込み | 確定 |
| `CONCAT` | `string + string` | 確定 |
| `IF` `ELIF` `ELSE` `ENDIF` | `if`/`elif`/`else`(7節)、`switch`の内部的な分岐実装(タグ付き/タグなし問わず、対応するネイティブ命令がamivmに無いため`switch`はIF/ELSEのネストへ desugar する。7節)。`ELIF`トークン自体は使わず`ELSE`の中に次の`IF`をネストして自前で組み立てる(条件式が複数命令を要する場合に対応するため。下記「確定した設計判断」参照) | 確定 |
| `LOOP` `BREAK` `CONTINUE` `ENDLOOP` | `while`(7節)、`for`(list/map/channel、いずれも7節)。`break`/`continue`はループでは基本ネイティブの`BREAK`/`CONTINUE`だが、`switch`の`break`と添字カウント式`for`(list/map)の`continue`だけ`LABEL`/`GOTO`を使う(下記「確定した設計判断」参照) | 確定 |
| `LABEL` `GOTO` | `switch`の`break`(ネイティブの`BREAK`で抜けられる構造がswitchには無いため)、list/map for-inの`continue`(添字カウンタの更新が本体の後にあるため、ループ先頭に戻るネイティブ`CONTINUE`だけでは足りない) | 確定 |
| `FUNC` `RET` `ENDFUNC` `CALL` | 通常関数・レシーバー付き関数・複数戻り値(8節) | 確定 |
| `ADDR` `PGET` `PSET` | `&x`(アドレス取得。単純な変数だけでなく、amivmのADDRの`point`オペランド追加後は`&p.x`/`&xs[0]`のような単一階層のフィールド/添字アクセスも含む)・`*p`(デリファレンス)・`*ptr = v`(2.2/4.4/6節) | 確定 |
| `SLTYPE` `SLMAKE` `ASET` `AGET` | `[]T`(可変長リスト、4.3節)。`append`は再SLMAKE+コピーで実現する見込み(Seedの配列と異なり可変長なので再確保が前提) | 確定 |
| `SLICE` | Step 9で実装。ユーザー向け構文としては今も存在しないが、`filter`組み込み関数の内部実装(結果件数が事前にわからないため入力と同じ長さで`SLMAKE`し、マッチ分だけ前方に詰めてから実際の件数へ`SLICE`で切り詰める)で自然な使い道が見つかった | 確定 |
| `STTYPE` `FIELD` `ENDSTTYPE` `FSET` `FGET` | `struct`定義・フィールドアクセス(4.1/5節) | 確定 |
| `MPTYPE` `MPMAKE` `MSET` `MGET` `MPKEYS` | `map<K, V>`(4.5節)。`m[k]`が返す`V?`は予想通り`MGET`のcomma-ok形をそのまま値+nullフラグへ流し込めた(seed_implementation_notes.md §3の予想が的中)。Step 10で実装・実地検証済み。`for k, v in m`のキー列挙はamivmに`MPKEYS`が追加されるまで`cascadert.Keys`という自前ランタイム経由で回避していたが、`MPKEYS`追加後は`cascadert`パッケージごと削除しネイティブ命令に置き換えた(下記「確定した設計判断」参照) | 確定 |
| `FNTYPE` `CLOS` `ENDCLOS` | `func(...): R`型・クロージャーリテラル(8.3節)。Step 9で実装・実地検証済み。amivmが`CLOS`のネストに対応した後は、クロージャーリテラルのネスト(カリー化)にも対応した(下記「確定した設計判断」参照) | 確定 |
| `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` | `chan<T>`・`send()`・`for v in someChannel`(9節) | 確定 |
| `SPAWN` | `source`/`stage`/`sink`を`|>`で連結した際の各段の並行実行(9.2節) | 確定 |
| `SEL` `CASESEND` `CASERECV` `DEFAULT` | `merge()`のファンイン実装(9.5節。2チャネルのどちらか先に来た方を受け取るselect相当)に加え、`abort()`(9.4節)の中断通知・各ステージの`for-in`/`send()`の中断監視でも使用。Step 13で実装・実地検証済み(下記「確定した設計判断」参照) | 確定 |
| `DEFER` | ユーザー向け構文としては存在しない。`source`/`stage`のFUNC本体先頭で`DEFER ?close $N`(出力チャネル)を無条件に発行し、関数(=ステージ)がどの経路で終了しても出力チャネルを自動closeして下流の`for-in`を終了させる、というコード生成側の内部利用。Step 12で実装・実地検証済み(下記「確定した設計判断」参照) | 確定 |
| `ASSERT` | 対応するCascade機能なし。Goの型アサーション`v.(T)`(`any`型の値から具体的な型を取り出す命令)に対応するが、Cascadeの型システムには`any`相当の型が無く、`ASSERT`を必要とする局面がそもそも発生しない。「無理に全命令を使う必要はない」という本プロジェクトの努力目標(冒頭「全命令使用という目標について」参照)に照らして、これは意図的に対象外とする | 対象外 |

## 確定した設計判断

実装を進める中で確定した設計判断をここに記録する(Seedの`seed/CLAUDE.md`「確定した設計判断」節と同じ運用)。当初「オープンな設計課題」として列挙していた項目(パイプラインの並行実行モデル・`error`型の表現・レシーバー関数のコンパイル方針・パッケージ/モジュール解決)はStep 14までに全て確定し、その節自体は役目を終えたため削除した——各項目の確定内容は以下に残っている。

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

**(Step 9時点)amivmはCLOSのネストを禁止している(`amivmはFUNC本体内にのみ出現し、ネスト不可`)ため、クロージャーリテラルの中にさらにクロージャーリテラルを書くことは当時サポートしなかった。** `checker.closureDepth`という単純なカウンタ(クロージャー本体を検査している間だけインクリメント)でこれを検出し、ネストが見つかったら明示的な「amivmのCLOSはネストできない」というCascadeレベルのエラーを出していた(amivmの`IRパースエラー`という分かりにくい形で表面化させない)。**この制限は後日amivm本体が`CLOS`のネストに対応したことで解消され、Cascadeもカリー化(クロージャーリテラルのネスト)に対応した——詳細は下記「AMIVM命令改修・第3弾」参照。**

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

### エラー処理(`error`型・`(T, error?)`・後置`?`)の設計(Step 11)

**`error`型は、オープンな設計課題で候補に挙げていた通り、`message: string`を1フィールド持つ`STTYPE`をCascadeコンパイラが自動登録する形で確定した。** `internal/sema`と`internal/codegen`それぞれに独立した`errorStructDecl`(パッケージ間非依存の原則を維持するため、あえて共有せず2箇所に同じ定義を持つ)を持たせ、`checker`/`funcGen`双方の構造体テーブルへ他のユーザー定義structと同列に事前登録する。`error`はStep 1からlexerの予約語(`KwError`)だったため、ユーザーが同名のstructを定義しようとしても構文的に不可能で、衝突の心配なしにこの方式が成立した。結果として`err.message`のようなフィールドアクセスは、既存の`FGET`/構造体アクセス経路をそのまま通るだけで、`error`型専用のcodegen分岐は`error(msg)`コンストラクタ(`FSET`で新規`^error`一時変数にmessageを設定するだけ)以外に一切不要だった。

**後置`?`は、仕様例が示す`(T, error?)`ちょうど2値・かつTが非nullable、という形にスコープを絞って確定した。** 一般化した「結果列の末尾がerror?でありさえすればよい」という設計も検討したが、`cascade_spec.md` 8.6節の例は全て「成功値1つ+error?」のみで、Tがnullableなケースや3値以上のケースを示していない。`isErrorResultShape(results) = len(results)==2 && results[1].Name=="error" && results[1].Nullable`という厳密な判定にし、それ以外の形の呼び出しに`?`を使おうとするとsemaが明確なエラーメッセージで拒否する(Step 7/8で確立した「仕様に無い組み合わせは先送りし、曖昧な動作にしない」方針を踏襲)。`checker.currentFuncResults`(関数/クロージャー本体を検査中、その関数自身の宣言済み戻り値型を保持するフィールド。`closureDepth`と同じパターンでクロージャー境界ごとに save/restore する)を使い、「`?`を使えるのは、それを含む関数/クロージャー自身も`(T, error?)`形を返す場合のみ」という制約も同時に検査する(そうでなければ早期returnする値の型が合わなくなるため)。

**Step 7で先送りにしていたnullable戻り値の制限を、Step 11で撤廃し完全に一般化した。** `(T, error?)`規約は「nullableな戻り値をCALL/RET境界越しに運ぶ」という、Step 7時点で意図的に実装を避けていた機構そのものを必要としたため、`checkFuncSig`/`checkClosureLit`から`needsRetIssetExpansion`の拒否チェックを削除し(このヘルパー自体も削除。`needsIssetSlot`と全く同じ判定だったため冗長になった)、`genFuncDecl`/`genClosureLit`の結果型リスト構築・`genReturnStmt`のRET生成を、既存のnullable引数(Step 7)と全く対称な「`needsIssetSlot`なら値+`^bool`の2オペランド」という展開に書き換えた。これはerror型に限定した特殊機構ではなく、`func f(): string?`のような任意のnullable戻り値が今回から一般に使えるようになったということでもある(回帰テスト`TestCheck_ValidNullableReturnType`で確認)。

**呼び出し結果の扱いを`resolveCall`/`genResultTemps`/`emitCallWithResults`という3つの共有ヘルパーに統一し、`genUserFuncCallValue`/`genMethodCallValue`/`genClosureCallValue`/`genMultiLetDecl`/`genNullableOperands`/後置`?`(`genErrorProp`)の6箇所全てから使うようにした。** `resolveCall`はStep 9の`resolveClosureCall`(sema側)に対応するcodegen側の「メソッド/クロージャー変数/通常関数のどれであっても、呼び出し対象名・シグネチャ・引数オペランド列を同じ形で返す」統一ヘルパー。`genResultTemps`は結果型リストから(nullableなら2つずつ)一時変数を確保し、`emitCallWithResults`が両者を組み合わせて`CALL`を発行する。この統一が無いと、6箇所それぞれで「結果が2個になりうる」ロジックを個別に重複実装する必要があり、抜け漏れのリスクが高かった。副産物として`genMultiLetDecl`(複数戻り値の`let`)がクロージャー変数からの呼び出しに未対応だった既存の抜けも同時に塞がれた。

**副産物のバグ修正(5件目)**: いずれも`amivm`→`go build`→実行という実地検証で初めて発覚し、Cascade自身の生成やIRの見た目だけでは気づけなかった。(1) `genNullableOperands`の`none`分岐が構造体非対応の`zeroValueLiteral`を直接呼んでおり、`return 0, none`(`error?`のゼロ値)で`codegen: unknown type "error"`エラーになっていた——`genZeroValueToken`という新ヘルパー(struct型なら`genStructZeroReset`経由、それ以外は従来の`zeroValueLiteral`)を新設し、そちらを呼ぶよう修正した。(2) **`FNTYPE`のdeftype生成(`list.go`の`typeToIR`、`t.Func != nil`分岐)が、nullableなパラメータ・結果の値+`_isset`展開を全く行っておらず、`FUNC`/`CLOS`本体が実際に生成する引数・結果の個数と食い違っていた。** 例えば`func(int): (int, error?)`型のクロージャーは、`FNTYPE`側では`^int : ^int ^error`(2結果)と宣言される一方、`CLOS`本体は`^int : ^int ^error ^bool`(3結果)を実際に返しており、この構造的な不一致はCascade側の生成・IRの目視では発見できず、amivm自身の`go/types`型チェック(`assignment mismatch: 3 variables but ... returns 2 values`)として初めて表面化した。`typeToIR`の`t.Func`分岐を`genFuncDecl`/`genClosureLit`と全く同じ`needsIssetSlot`駆動の展開ロジックに書き換えて修正した。この経緯は「実装したら実地検証まで必ず確認する」(seed_implementation_notes.md §6.1)の重要性を改めて裏付けるものだった——今回のバグはCascade側のどの単体テストが通っていても発見できず、amivmへ通した生成Goコードのコンパイルエラーとしてしか現れなかった。

`examples/11_errors.cas`(`divide`/`loadAndDouble`/`saveResult`/`process`/`mustBePositive`という値の伝播・式位置の`?`・文位置の`?`(戻り値破棄)・単一`error?`結果・クロージャーの`(T, error?)`という組み合わせ)で`amivm`→`go build`→実行まで確認済み。

### パイプライン基礎(source/stage/sink・`|>`・send・チャネルfor-in)の設計(Step 12)

**並行実行モデルは、オープン課題1で候補に挙げていた通りの形で確定した: `source`/`stage`/`sink`はそれぞれ独立した(通常の関数と同じ名前空間を共有する)トップレベル`FUNC`として生成し、`|>`で連結された際に先頭の`source`と間の`stage`群を`SPAWN`、末尾の`sink`だけを普通の`CALL`で同期的に呼び出す。** これにより、パイプライン文(`numbers |> double |> toString |> printAll`)全体はGoの「プロデューサをgoroutineとして起動し、最終コンシューマは呼び出し元のgoroutineで直接走らせる」という定番パターンに帰着し、`sink`の`CALL`が返るまで文全体が自然にブロックする(sinkの`for v in input`ループは、上流の全段が終了して出力チャネルをcloseし終えるまで戻らないため)。段間のチャネルは各接続ごとに新しく`CHMAKE`(バッファサイズ`0`、素朴な非バッファチャネルで正しさ上は十分——正しさはバッファ有無に依存せず、スループットのみに影響するため今回は最小の`0`を選んだ)。

**「各段が処理を終えたら出力チャネルをcloseして下流に伝播させる」という命令使用ゴール表の`DEFER`候補が、想定通りそのまま実装できた。** `source`/`stage`のFUNC本体は、パラメータの巻き上げ(VAR hoisting)が終わった直後・ユーザー本体の最初の文より前に、無条件で`DEFER ?close $N`(`source`なら`$1`、`stage`なら`$2`——どちらも「自分の出力チャネル」)を発行する。Goの`defer`はどの経路で関数を抜けても(正常終了・早期`return`いずれでも)実行されるため、ユーザーが一切意識することなく「自分の仕事が終わったら出力チャネルを閉じる」が保証される。これが`for v in input`(下流のチャネルfor-in)が終了できる唯一の手がかりになる。`sink`は出力チャネルを持たないため、この`DEFER`は一切発行しない。

**`for v in input`(チャネルの走査)は`CHRECV`のcomma-ok形をループ条件とループ本体の値取得の両方に使う、リスト/mapのfor-inとは全く異なる形になった。** リストfor-in(Step 6)・mapのfor-in(Step 10)はどちらも「インデックスカウンタ+`AGET`/`MGET`」という明示的なカウントループだったが、チャネルには「残り件数」という概念がそもそも無い——`CHRECV`自体が「値を受信できたか(`ok`)」を返すため、`ok`が`false`になった時点(=チャネルがcloseされ、かつバッファが空になった時点)がそのままループ終了条件になる。結果として`continue`は(リスト/map形のような専用の「インクリメント直前」ラベルを経由せず)`while`ループの`continue`と全く同じ形で、次の`CHRECV`を発行する先頭ラベルへ直接ジャンプするだけで済んだ——インデックス変数もその増分ステップも存在しないため。`ast.ForInStmt`に新設した`IsChannel bool`(sema.Checkが設定)がこの3つ目の形をcodegen側で選び分ける——`ValueVarName`(map形の判別に使う既存フィールド)だけでは「単一変数だが実はチャネル」というリスト形との違いを表現できないため、`IndexExpr.ResultType.Nullable`(map読み取りとリスト読み取りを1フィールドで判別する、Step 10の既存パターン)のような転用ができず、素直に専用の bool フィールドを追加した。

**`chan<T>`型はパイプライン専用の型として、`sema.validateType`が一般の型出現位置(構造体フィールド・通常関数の引数/戻り値・`let`宣言等)では明示的に拒否し、`source`/`stage`/`sink`のパラメータ位置(`validateChanParamType`)でのみ許可する形にした。** 2.2節の「`chan<T>`...(単独では宣言しない)」という注記を、素朴に「初期化式が無い宣言(ゼロ値が必要な文脈)だけ禁止する」ではなく、より踏み込んで「チャネル型そのものがパイプライン以外の場所に一切出現できない」という制約として解釈した——現時点でチャネル型を返す唯一の手段(`merge`)も、代入先(通常の値としての利用)もStep 13以前には存在しないため、今使われない自由度を先取りして持たせるより、実際に必要になった時点(`merge`実装時)で`let x: chan<int> = merge(...)`のような「初期化式ありの`let`」だけを個別に許可する方が、CLAUDE.mdの一貫した方針(仕様に無い組み合わせは先送りし、曖昧な動作にしない)に沿うと判断した。コード生成側(`typeToIR`)は`chan<T>`のTがスカラー・構造体はもちろんリスト・map・関数型など任意の妥当な型でも動くよう、`useFuncType`/`useMapType`と全く同じ「形状で重複排除するレジストリ+合成した`^ChanTypeN`という名前」という設計にした(`useChan`)——リストの`^intlist`のような要素名由来の命名ができない(要素型がスカラーとは限らないため)ことを除けば、既存の2つの命令使用パターンをそのまま踏襲しただけで済んだ。

**`send`は仕様上`chan/collect/error`等と同じくlexerの予約語(`KwSend`)であるため、`print`のような「識別子ベースの組み込み関数」の脱糖経路(`parseIdentStmt`)を素通りできず、`parseStmt`に専用の分岐(`case lexer.KwSend`)を追加する必要があった。** 実装時にこの点(spec上の「予約語一覧」にsendが載っている事実)を見落としかけたが、`reservedBuiltinNames`のコメントが「`send`/`chan`/`collect`/`error`はキーワードなのでエントリ不要」と既にStep 11時点で書き残していたため(将来の実装者への申し送りとして機能した)、発見・対応は早かった。パース後は`ast.CallExpr{Callee: "send", ...}`という通常の呼び出し式として扱われ(`p.parseCallExprFrom`に合成トークンを渡すだけ)、sema/codegenの`checkExprStmt`/`genExprStmt`は`print`/`delete`と同列の`case "send":`分岐が1つ増えるだけで済んだ——構文的な入口だけが特殊で、それ以降のパイプライン全体は既存の「組み込み関数はCallee名で分岐する」という仕組みにそのまま乗った。

`examples/12_pipelines.cas`(`numbers`/`double`/`toString`/`printAll`という4段パイプライン——`source`→`stage`→`stage`→`sink`——を`|>`で連結、`range`で生成した`[]int`を`send`で流し込み、`SPAWN`された3段と同期呼び出しされる`sink`が実際に協調動作することを実行時に確認)で`amivm`→`go build`→実行まで確認済み。

### パイプライン拡張(collect/abort/merge)の設計、パイプラインの並行実行モデルの最終確定(Step 13)

**`abort`(9.4節)の中断通知は、CLAUDE.mdの元の候補(`close`一発)ではなく、`close`を「一度だけ」安全に呼ぶための専用ガードチャネルを追加する2チャネル構成に変更して確定した。** 素朴に「`chan<bool>`を`close`するだけ」という候補は、**複数のステージが同時に`abort()`を呼ぶと`close of closed channel`でGoランタイムがパニックする**という欠陥を設計段階で発見した(実装前のレビューで気づいた——「実装したら実地検証」の一歩手前で、想定される並行アクセスパターンを机上で洗い出した結果)。解決策として、`abort`用のブロードキャストチャネル(`abortChan`、一度closeされたら二度と送信されない。`for-in`/`send`の`SEL`はこれを`CASERECV`で監視するだけで、closeが再現なく何度でも「即座に受信可能」であるというGoの性質をそのまま使う)とは別に、**「closeする権利を1つだけ配る」ための専用ガードチャネル(`guardChan`、バッファ1)を追加**した。`abort()`は`guardChan`への非ブロッキング送信(`SEL`+`CASESEND`+`DEFAULT`)を試み、勝った(送信できた)ゴルーチンだけが実際に`abortChan`を`close`する——`guardChan`自体は一切受信されないため、バッファが1つ埋まった時点で以降の送信は全て`DEFAULT`に落ちて安全に無視される。この設計により、`source`/`stage`/`sink`は全て**2つ**の隠しパラメータ(`abortChan`・`guardChan`)を無条件に追加で受け取る形になった(ユーザーの書くCascadeシグネチャには一切現れない、`DEFER ?close`と同じ「コード生成側だけの内部機構」)。

**`for-in`(チャネル走査)と`send()`は、ステージ本体の中でだけ`SEL`ベースの中断監視版にコンパイルされる。** `funcGen`に`abortChanOp`/`guardChanOp`という2つの新フィールドを追加し、`genStageDecl`だけがこれをセットする(通常の`func`本体では空文字列のまま)。`genForInChannelStmt`/`genSendCall`はこのフィールドが空かどうかで2つの生成パスに分岐する: ステージ内では`SEL`(`CASERECV`/`CASESEND`いずれかの本来の操作 vs `abortChan`の`CASERECV`)に包み、中断を検知したら**即座に`RET`**する。ステージ外(例えば`merge()`の結果チャネルを`main`から直に読む場合)では中断機構自体が存在しないため、Step 12そのままの素朴な`CHRECV`/`CHSEND`にフォールバックする。**`send()`側も中断監視が必要**だという点は設計段階で見落としかけた——`CHMAKE`のバッファサイズが`0`(Step 12の選択)である以上、下流が中断で先に終了した場合、上流が`send()`でブロックしたまま永遠に取り残される(デッドロック)ため、受信側だけでなく送信側にも同じ中断監視が要ることに気づいた。実際に100件生成する`source`に対し6件目で`abort`する`stage`を実地検証し、`source`側が正しく`send()`のSELで中断を検知して即座に終了することを確認した(該当箇所が未検証のまま出荷されていたら本番でハングするクラスのバグだった)。

**`abort(message)`は、メッセージの送り先が仕様に一切定義されていないため、`fmt.Println`で標準出力へ`"abort: " + message`として出力する、という実装判断を下した。** 9.4節は"エラー時の終了"としか説明しておらず、メッセージがどこへ届くべきかについて例も規定も無い。何も出力しない案(黙って握りつぶす)はデバッグ時に情報が失われるため却下し、`print`と同じ`?fmt.Println`経路に素直に乗せることにした——新しい出力先(`os.Stderr`等)を導入すると、`?os.Stderr`をCALLの通常の`value`オペランドとして渡す手段が無い(amivm-IRの`value`カテゴリは`?xxx`形式を含まない——`?xxx`は`callname`/`DEFER`/`SPAWN`の呼び出し対象専用)という制約にも実装中に気づき、標準出力への出力を選んだことでこの問題自体を回避できた。**`abort()`はメッセージ出力に続けて無条件に`RET`する**(9.4節の`validate`の例——`if n < 0 { abort(...) }`の直後に`send(output, n)`が続く——が、`abort`を呼んだらそのステージの残りの処理を一切実行してはならないことを暗に要求しているため、Cascade側の構文には現れない暗黙の早期returnをコード生成が挿入する)。

**`collect`(9.3節)は、CLAUDE.mdの候補通り「専用のcollector goroutineを内部生成し、結果を別チャネル経由で同期的に受け取る」という設計で確定した。** `|>`チェーンの終端が`collect`の場合、それまでの`source`/`stage`群を通常の`|>`文と全く同じ手順で`SPAWN`した上で、コード生成が動的に合成する`!__collect_N`という専用`FUNC`(ユーザーの書いたCascadeソースには一切対応物が無い、`typeRegistry`に新設した`collectorFuncs`という蓄積機構経由でトップレベルFUNC群の末尾にまとめて出力される)を最終段の出力チャネルへ`SPAWN`する。このcollectorは受信した値をGo生ネイティブの`append`(Cascade標準の`append`組み込みとは違い、常に再確保する設計にはしていない——このアキュムレータは他のどこからも参照されない完全にプライベートな一時変数なので、エイリアシングの心配が構造的に発生しない)で溜め込み、入力チャネルがcloseされたら結果チャネルへ`CHSEND`して`RET`する。**`abort`されると`none`になる(9.3節の規定)という挙動は、追加のフラグや専用命令を一切使わず、既存の`DEFER ?close`とcomma-ok受信の組み合わせだけで自然に得られた**——collectorが`abort`検知で(結果を送らずに)`RET`すると、`DEFER`していた結果チャネルのcloseだけが起こり、呼び出し元の`CHRECV`のcomma-ok結果が`ok=false`になる。これは既存のnullable機構(値+issetペア)にそのまま合流するため、`genNullableOperands`に`*ast.PipelineExpr`用の分岐を1つ追加するだけで、`[]T?`という戻り値の型が持つ「成功/中断」の二値性を新しい概念を一切導入せずに表現できた。

**`merge`(9.5節)は、仕様の`merge(channelA, channelB)`という最小限の例だけでは「channelA/channelBはどこから来るのか」が構築不能という問題に設計段階で突き当たり、判断を下して確定した。** Cascadeには`CHMAKE`/`SPAWN`をユーザーが直接書く手段が無く(`|>`が内部で使うだけの隠し機構)、`chan<T>`型の値を得る唯一の方法は`source`/`stage`/`sink`のパラメータ経由か`merge`自身の戻り値だけである。この制約下で「既存の2つのチャネル値を受け取って合流させる」という一般的な関数として実装するには、呼び出し元がまず何らかの方法で2つの独立したチャネルを用意できる必要があるが、Step 12の`|>`文法(`source`から始まり`sink`/`collect`で終わる単一の直線的な連結のみ)ではその手段が無い。**この不整合を解決するため、`merge`の2引数は「チャネルの値」ではなく「宣言済みの`source`の名前」として扱う設計に決めた**——`merge(sourceA, sourceB)`は、`sourceA`・`sourceB`という名前をsemaが(通常のスコープ変数としてではなく)`c.stages`テーブルから直接解決し(`checkCallExprValue`の専用分岐。`sourceA`はそもそも通常の変数として存在しないため、既存の`exprType`に一切通さない)、codegenは両方を自前でSPAWNしてから、2入力を1出力へ束ねる専用のファンインgoroutine(collectorと同様、`typeRegistry`に新設した`mergeFuncs`蓄積機構経由でトップレベルFUNC群として出力される`!__merge_N`)へ接続する。ファンインgoroutineは**「片方のチャネルがcloseされたら、そのチャネル変数自体をGoの`nil`にセットし、以降`SEL`のその節を実質的に無効化する」という定番のnilチャネルイディオムで実装した**(`EQ chA nil`で判定し、両方nilになったら終了)——`SEL`の節自体を動的に増減させる手段がAMIVM-IRには無いため、この「nilにして事実上無効化する」手法が唯一の自然な実装方法だった。`merge`自身のSPAWNする2つの`source`は、この呼び出し1回に閉じた独立の`abortChan`/`guardChan`ペアを持つ(呼び出し元が既にステージの中にいて既存の中断機構を持っていたとしても、それを再利用せず常に新規生成する)——**これは意図的なスコープ限定であり、「`merge`された片方の`source`で`abort()`を呼んでも、もう片方や外側のパイプラインには伝播しない」という制約を残したままにしている**(仕様例にこの組み合わせが無いため、Step 7以来の「仕様に無い組み合わせは先送りし、曖昧な動作にしない」方針に沿って、正しく動くが範囲の狭い実装にとどめた)。

**副産物のバグ修正(6件目)**: 実装・検証の過程で2つの既存バグを発見・修正した。(1) `narrowedVarInfo`(if文の条件からの型絞り込み、Step 5由来。Step 8でポインタについて一度修正済み)が、絞り込み後の型を`ast.Type{Name: orig.Type.Name, Nullable: false}`という形で再構築する際、**リスト型・map型は`Name`が空文字列で`Elem`/`Map`フィールドに情報を持つことを全く考慮していなかった**——Step 8で発見したポインタ版と全く同じ根本原因のバグが、リスト・mapについては未発見のまま残っていた。仕様§15の完成サンプル(`collect`の結果`[]string?`を`is none`で絞り込んで`map()`/`for-in`に渡す)を自前の検証例として実装しようとして初めて発覚した——Step 2〜12のどのテストも「nullableなリスト/mapを絞り込んで使う」パターンを一度も検証していなかったため、6ステップ以上気づかれずに残っていたことになる。修正は「`Name`だけを個別にコピーする」のをやめ、「型全体をコピーしてから`Nullable`だけ`false`に上書きする」(`narrowed.Type = orig.Type; narrowed.Type.Nullable = false`)という、Elem/Map/Chan問わず正しく動く一般的な形に直した。(2) `typeGiven`(型注釈が明示されているかを判定するヘルパー、Step 6由来)が`t.Chan`をチェックしておらず、`let x: chan<int> = merge(...)`のような宣言が「型注釈なし、初期化式から推論しようとして`merge(...)`の特別扱いに一切到達しないまま`undefined name`」という誤ったエラーになっていた——`t.Elem`/`t.Ptr`/`t.Map`と同様に`t.Chan != nil`もチェックするよう修正した。いずれも該当する新規サンプル(後述)の実地検証中に発見し、`TestCheck_NullableListNarrowingPreservesElemType`等の回帰テストを追加した。

`examples/13_pipeline_extensions.cas`(センサー読み取りの構造体パイプライン(`readings`→`validOnly`→`format`→`collect`、null許容リストの絞り込みを経て出力)・2つの`source`を`merge`でファンインして合計を求める例・負の値を検出して`abort`し`collect`が`none`を返すことを確認する例、の3パターンを1ファイルに統合)で`amivm`→`go build`→実行まで確認済み。加えて、仕様15節の完成サンプルプログラム自体も個別に実地検証し、`collect`・絞り込み・`map()`・`for-in`の組み合わせが仕様の記述通りに動作することを確認した。

### パッケージ/複数ファイル(11節)の設計(Step 14)

**パッケージ解決は「AST構築後・sema前の独立したフェーズ」というCLAUDE.md当初の方針通り、`internal/pkgloader`が単独で担い、sema・codegenはパッケージという概念を一切知らないまま(Step 1〜13と全く同じ、単一パッケージのフラットな`*ast.File`を受け取るだけ)にする設計で確定した。** `pkgloader.Load(root)`が唯一のエントリポイントで、(1) `root`配下(ディレクトリなら全`.cas`ファイル、単一ファイルならそれ1つだけ——後述)をパースして1パッケージにマージ、(2) 各ファイル先頭の`import`を再帰的に解決(循環検出付き)、(3) ルート以外の各パッケージについて、自パッケージの全トップレベル宣言(struct/関数/stage/トップレベルlet)の名前とその参照箇所全てに`パッケージ名_`のプレフィックスを機械的に付け替え(自己リネーム)、(4) インポート元ファイルの`qualifier.Name`という参照を、インポート先の(既にプレフィックス済みの)flatな名前へ書き換え(`pub`検証込み)、(5) 最終的に全パッケージの(既に完全にリネーム済みの)宣言を1つの`*ast.File`へプールする。この結果、sema.Check/codegen.Generateに渡される時点では、複数ファイル・複数パッケージだったという事実は跡形もなく消えており、両パッケージへの変更は一切不要だった。

**CLIの`build`/`run`/`emit-ir`/`emit-go`は、単一`.cas`ファイルとパッケージディレクトリの両方を`srcPath`として受け付ける形で確定した——仕様11.1節の字義通りの実装(パッケージ=ディレクトリ、そのディレクトリの全`.cas`ファイルを問答無用でマージする)だけに絞ると、これまで13ステップ分書き溜めた`examples/*.cas`(同じ`examples/`ディレクトリに互いに無関係な13個のプログラムが同居している)が、Step14以降は「1つの巨大な、`main`重複だらけの壊れたパッケージ」として扱われてしまう。この互換性問題を避けるため、`pkgloader.Load`は引数がファイルなら「そのファイル1つだけの、暗黙の単一ファイルパッケージ」として扱い(同じディレクトリにある他の`.cas`ファイルは無視する——これは仕様11.1節の字義とは厳密には異なる、CLIの利便性のための意図的な単純化と位置づけている)、引数がディレクトリなら仕様通り全`.cas`ファイルをマージする、という2モードを両方サポートする設計にした。既存13サンプルは全て`examples/NN_name.cas`という単一ファイルのまま(引数もそのファイルパス)動き続け、Step14の新規サンプルだけ`examples/14_packages/`というディレクトリ引数の形にした。

**非ルートパッケージの識別子リネームは、宣言名だけでなく「同じパッケージ内でその名前を参照している箇所全て」も機械的に書き換える設計にした——CLAUDE.mdが当初想定していた「識別子一意化はcodegenの命名規則に組み込む」という案(sema/codegenへ渡すASTの識別子自体は素のまま、AMIVM名を計算する時だけプレフィックスを付ける)は、実装を検討する過程で「sema/codegenの記号表(`c.structs`・`c.sigs`・`c.stages`)がパッケージという概念を全く持たない、1つのフラットな名前空間である」という既存設計と根本的に衝突することが分かり、不採用にした。** 2つの異なるパッケージが同名の構造体(例: どちらも`Vector`)を独立に持てるという要件を満たすには、sema/codegen自身に「どのパッケージの`Vector`か」を区別させる仕組みが必要になり、それは事実上「semaをパッケージ対応にする」という、この方式が本来避けたかった変更そのものになってしまう。代わりに、pkgloaderが**構文木レベルで**名前を書き換えることで、sema/codegenに渡る時点で全ての名前が既にグローバルに一意になっている(=既存の「1つのフラットな名前空間」という前提を実際に満たす)状態を作った。メソッド名自体(レシーバー関数の`Name`)はリネーム対象から意図的に除外した——メソッドは既存のcodegen規約(`StructName_Method`)によってレシーバーの構造体名経由で既に一意になっており、レシーバーの型(`Receiver.Type`)さえ正しくリネームされれば、メソッド名は素のままで正しく動作するため。

**既知の未対応事項として記録**: レシーバー関数(メソッド)単位の`pub`は構文としてはパースするが、実効的には検証していない。仕様11.3節は「`pub`が無い宣言はそのパッケージ内だけで使える」としており、あるメソッドが`pub`かどうかは本来「そのメソッドの呼び出し元がどのパッケージにいるか」だけでなく「レシーバーの静的型がどのパッケージのものか」も分からないと判定できないが、レシーバー式の型はsemaの型推論結果であってpkgloaderの構文木だけからは分からない(逆にsemaは、そもそもパッケージという概念を持たない)。関数・構造体・トップレベルlet/constの`pub`(修飾子経由の参照でしか到達できないため、pkgloaderの構文的な書き換えパスだけで検証が完結する)とは違い、メソッドの`pub`検証には型推論とパッケージ情報の両方が要るため、Step14の設計(sema/codegenは完全にパッケージ非対応のまま)の範囲では実現できないと判断し、先送りにした。同様に`|>`チェーンでの他パッケージのsource/stage/sink参照(`ast.PipelineStageRef`)も未対応(自パッケージ内の名前のみ)——いずれも仕様に具体例が無い組み合わせであり、必要になった時点で改めて設計する。

**関数呼び出し・トップレベルlet参照の自己リネームは、ローカル変数によるシャドーイングを正しく扱わない、という既知の限定スコープを受け入れて確定した。** 型名・構造体リテラル名・stage名の参照(`ast.Type.Name`・`ast.StructLit.TypeName`・`ast.PipelineStageRef.Name`)は、Cascadeにローカルスコープの「型」やstage名の概念が存在しないため、構文的な文字列一致だけで安全にリネームできる。しかし関数呼び出し(`CallExpr.Callee`)とトップレベルlet参照(`ast.Ident`)は、同名のクロージャー変数やローカル変数によって**shadowされる可能性がある名前空間を共有している**(Step 9の`resolveClosureCall`が正にこの優先順位——ローカルのクロージャー変数がビルトインや同名のトップレベル関数を隠せる——を実装している)。pkgloaderは構文木だけを見る(sema相当のスコープ解決を一切行わない)ため、「このローカル変数はたまたま同じパッケージの某トップレベル関数と同じ名前を持つが、意図的にshadowしている」というケースを区別できず、常にリネームしてしまう。仕様例にこの組み合わせ(同一パッケージ内でトップレベル宣言と同名のローカル変数を意図的に作る)が一切登場しないため、Step 7以来の「仕様に無い組み合わせは先送りし、曖昧な動作にしない」方針に沿って、この限定スコープを明示的に受け入れることにした(仕様に登場すれば、pkgloaderをsema並みのスコープ解決対応にする、または全く別の設計に切り替える必要がある)。

**`修飾子.識別子`という参照の構文解析は、既存の文法をほぼ流用し、パーサー自身がambiguousな判断を一切行わない形で確定した。** 型位置(`mathutil.Vector`)は`parseTypeBase`のIdent分岐に「直後が`.`なら、もう1つIdentを読んで`"qualifier.TypeName"`という(ドットを含む、通常の型名には出現しえない)文字列をそのまま`Type.Name`に格納する」という数行の拡張だけで済んだ。構造体リテラル(`mathutil.Vector{...}`)は`parseSelectorSuffix`(`x.y`を消費する既存の後置ループ)に「`x`が裸のIdentで、かつ直後が`{`なら、`"x.y"`という名前で構造体リテラルとしてパースする」という分岐を1つ足すだけで済んだ。関数呼び出し(`mathutil.Clamp(...)`)は**パーサーを一切変更していない**——既存のメソッド呼び出し構文(`obj.method(...)`が`CallExpr{Receiver: obj, Callee: "method"}`になる)がそのまま`CallExpr{Receiver: Ident{mathutil}, Callee: "Clamp"}`という形で受け止めてくれるため、パーサーの時点では「これはメソッド呼び出しか、パッケージ修飾子付き関数呼び出しか」を判定する必要が全く無く、判定はpkgloaderの書き換えパス(`Receiver`が既知の`import`修飾子と一致する裸のIdentかどうかを見るだけ)に完全に先送りできた。

**副産物のバグ修正(7件目)**: 実装・検証の過程で2つの実バグを発見・修正した。(1) pkgloaderの書き換えパス初版が、`FieldExpr`(`p.x`のような通常のフィールドアクセス)を「`X.Field`という1つのドット区切り文字列」として組み立て直し、型名(`"qualifier.TypeName"`)と全く同じ「ドットがあれば必ず修飾子として解決を試み、解決できなければエラー」というロジックに通してしまっていた——`examples/08_structs.cas`(`p.x`)・`examples/11_errors.cas`(`err.message`)・`examples/13_pipeline_extensions.cas`(`r.value`)という、importを一切使わない既存サンプル群を回帰テストとして流した際に、`undeclared import qualifier "p"`のような誤ったエラーで**全て**壊れて初めて発覚した。根本原因は、型名のドットは**パーサーが構文的に確定させた、常に一意な意味を持つマーカー**である一方、`FieldExpr`のドットは`X`(既に独立したAST上のフィールド)と`Field`を**呼び出し側が事後的に文字列連結しただけ**のもので、「通常のフィールドアクセス」と「パッケージ修飾子経由の参照」が構文的に区別できないという性質の違いを見落としていたこと。修正として、`FieldExpr`・`CallExpr`のレシーバー位置だけは「そのIdentが実際にこのファイルの`import`修飾子と一致するか」を`r.quals`に対して直接問い合わせる専用ヘルパー(`qualifierTarget`)に切り替え、一致しない場合は(エラーにせず)通常のフィールドアクセス/メソッド呼び出しとして素通りさせるようにした。既存13サンプル全てを実際に流すまで発見できなかった、という点で「実装したら実地検証まで確認する」という本プロジェクトの教訓を改めて裏付けた。(2) `typeGiven`(型注釈が明示されているかを判定するヘルパー、Step 6由来・Step13で`t.Chan`を追加済み)は今回のトップレベルlet実装では問題を起こさなかったが、念のため確認したところ既に全複合型を網羅済みだった(Step13時点の修正で対応済み、Step14固有のバグではない)。

**トップレベル`let`/`const`(11.3節)は、ローカル`let`と全く同じ`checkLetDecl`/`genInit`をそのまま再利用し、対象を`%name`(関数内VAR)から`@name`(GVAR)に差し替えるだけで実装できた。** sema側は`checker.globalScope`という新設フィールド(トップレベルlet群だけを持つ、親を持たない`*scope`)を追加し、全ての関数・stage本体の一番外側のスコープ(`newScope(nil)`だった箇所)を`newScope(c.globalScope)`に差し替えるだけで、トップレベルletがパッケージ内のどこからでも参照できるようになった(クロージャーは既存のスコープ連鎖を通じて自動的に伝播するため無変更)。codegen側も対称的に、`genFuncDecl`/`genStageDecl`が`packageScope *scope`を受け取り`newScope(packageScope)`から関数を組み立てるようにした。**トップレベルlet同士の依存関係は宣言順のみをサポートし、Goのような依存関係グラフに基づく初期化順序の自動解決は行わない**(仕様にトップレベルlet同士が参照し合う例が無いため、先行するletだけを後続のletの初期化式から参照できる、という単純な逐次処理にとどめた——複数ファイルにまたがるトップレベルletの宣言順序は、pkgloaderがファイルをパス名でソートしてからマージする順序に従う)。トップレベルletの初期化式は、コンパイラが内部で合成する`!cascade_init`という専用FUNC(トップレベルletが1つも無いプログラムでは一切生成されない)にまとめて出力し、生成される`!main`ラッパーが`!cascade_main`を呼ぶ**前**にこれを呼ぶよう変更した。

`examples/14_packages/`(ルートパッケージ`main.cas`が`mathutil`という別パッケージをインポートし、`pub`な関数(`Clamp`)・構造体+レシーバー関数(`Vector`/`magnitudeSquared`)・トップレベル`const`(`Version`)の3種類全てを修飾参照する、仕様11.2/11.3節の例をほぼそのまま再現したサンプル)で`amivm`→`go build`→実行まで確認済み。加えて、`pub`でない宣言への他パッケージからの参照・循環import・非ルートパッケージでの`main`宣言という3つのエラーケースをそれぞれ個別に確認し、いずれも意図通り拒否されることを確認した。

### CLI・配布(Step 15)

**CLI(`cmd/cascade`)・Makefile・`cascadert`のgo:embed配布は、いずれもStep 1〜10の時点で[[Seed]]の`cmd/seed`/`seedrt`パターンをそのまま踏襲する形で既に実装済みだったため、Step 15で新たに設計すべき事項はほぼ無く、実質的には「Seedと同じ方針で進めてよいか」というユーザーへの確認と、既存実装の点検・README作成に限られた。** `cascade <build|run|emit-ir|emit-go|help> [-o file] [-v] <file.cas | package-dir>`というコマンド体系、`compileToIR`→`compileToGo`→`compileToBinary`という3段の共通関数への分割、`-v`がamivm自身の`-v`トレースをそのまま素通しする方式は、いずれもSeedの`seed/CLAUDE.md`「確定した設計判断」に記された内容と一致している。

**CLI点検の過程で、Step 14(パッケージ)がディレクトリ引数への対応を追加した際に見落としていた実バグを1件発見・修正した。** `defaultOutPath`(`-o`省略時の出力パス導出)が、単一ファイル(`foo.cas` → `foo.ir`)の場合しか想定しておらず、ディレクトリ引数(`examples/14_packages`)を渡すと`.cas`拡張子が無いため何も変換されず、出力パスが**入力ディレクトリと全く同じ文字列**になっていた。`go build -o <既存ディレクトリ>`はGo自身の挙動として「そのディレクトリの中に、ビルドしたパッケージ名を使ったバイナリを書き込む」という意味になるため、`cascade build examples/14_packages`を実行すると**ソースディレクトリの中にビルド成果物(`cascadebuild`という名前のバイナリ)が紛れ込む**という、サイレントにソースツリーを汚染する形の不具合になっていた(`examples/14_packages`を実際に`cascade build`してみて初めて発覚)。修正として、`defaultOutPath`がディレクトリを検出した場合は`filepath.Base`(ディレクトリ自身の名前)だけをカレントディレクトリに対する相対パスとして使うようにした——`go build ./mypackage`(引数無し)がカレントディレクトリに`mypackage`という実行ファイルを作るのと同じ挙動に合わせている。Step 14時点で「ディレクトリを渡せる」ことは`pkgloader.Load`・`compileToIR`側では確認済みだったが、**CLIの`-o`省略時のデフォルトパス計算という、一見無関係な既存コードとの組み合わせ**までは検証していなかったための見落としだった——「新しい構文・機能を実装したら、既存の周辺コードとの組み合わせも含めて実地検証する」という、本プロジェクトで繰り返し確認されてきた教訓が、機能追加ではなくCLIの引数解釈という一見地味な箇所でも同様に当てはまる例だった。

`README.md`/`README_ja.md`はSeedの同名ファイルの構成(ステータス・パイプライン図・要件・インストール・使い方・例・言語仕様・リポジトリ構成)をそのまま踏襲し、Cascade固有の内容(3本柱・実装済み機能一覧・パイプライン記法を使った例)に差し替えて作成した。掲載したコード例(`Hello, Cascade!`・4段抜きの並行パイプライン)はどちらも実際に`cascade run`へ通して動作確認済み。

### AMIVM命令改修(ADDR拡張・callname拡張・MPKEYS追加)を受けたCascade側の追従(Step 15完了後)

15ステップの実装計画が完了した後、Step 8/9/10で発見した3つのamivm側の制約(下記「命令使用ゴール」節参照)について、amivm本体への改修をユーザーが別セッションで依頼・実装し、`make install`で反映した。Cascade側はこれを受けて、**「前の命令だったので回避していた」実装を、新しい命令をそのまま使う形に置き換える**という後方追従の改修を行った。新しいCascade機能や文法は一切追加していない——既存の`&`/`*p`/クロージャー呼び出し/`for k, v in m`が生成するIRの中身が変わっただけで、これらの構文自体はStep 5〜10で既に確定済みのまま。

**1. `ADDR`の`point`オペランド追加により、`isAddressable`(`internal/sema/sema.go`)を単純な識別子だけでなく、識別子直下の単一階層のフィールドアクセス(`&p.x`)・単一階層のリスト添字アクセス(`&xs[0]`)まで受け入れるよう拡張した。** amivmの`ADDR`は`ADDR single variable (point)`という形になり、`point`省略時は従来通り`single = &variable`、`point`が`>xxx_123`(フィールド名)なら`single = &variable.point`、それ以外(整数リテラル・変数など)なら`single = &variable[point]`を生成する(`amivm/docs/amivm_spec.md`4.2/5節)。ただし`point`はあくまで単一のフィールド名/添字であって経路(パス)ではないため、`&a.b.c`のような多段フィールドパスは今回も非対応のまま(`isAddressable`の`FieldExpr`/`IndexExpr`ケースは、いずれも`X`が裸の`*ast.Ident`であることを要求する)。また`&variable[point]`はスライス/配列専用でmapには使えない(mapの要素はGo自身においてもアドレス不可)ため、`isAddressable`の`IndexExpr`ケースは`ResultType.Nullable`(map読み取りかリスト読み取りかを判別する、Step 10由来の既存パターン)を見て、mapの添字は明示的に拒否する。

**この拡張を安全に行うため、`exprType`の`UnaryExpr`("&")ケースで、`isAddressable`の呼び出し順序を入れ替えた。** 従来は`isAddressable(v.X)`を`c.exprType(sc, v.X)`より先に呼んでいたが(識別子オンリーの旧実装ではv.Xの型情報が不要だったため問題にならなかった)、`IndexExpr`ケースの判定に`v.X`自身の(map/リストを判別する)`ResultType`が必要になったため、`exprType`を先に呼んでからでないと正しく判定できない。この入れ替えにより、型チェック(フィールドの存在確認・添字の妥当性確認)が先に走り、その後でアドレス取得可能性を判定する、という自然な順序になった。

**codegen側では、`&`の対象を`genValue`で一度値として評価してからADDRする、という従来のアプローチを使えないことに注意が必要だった。** `genValue(g, e)`で`p.x`や`xs[0]`を評価すると、`FGET`/`AGET`で**コピー**を新しい一時変数に読み出してしまい、そのコピーをADDRしても元のストレージへのエイリアスにはならない(まさにamivm_addr_revision_planメモリが警告していた「ADDRはコピー往復による回避ができない」という制約そのもの)。そのため、新設した`genAddrOfOperand`(`internal/codegen/struct.go`)は`target`(`&`の対象のAST式)を`genValue`に渡さず、`*ast.Ident`/`*ast.FieldExpr`/`*ast.IndexExpr`で型スイッチし、ベースとなる変数のトークンをスコープから直接引いた上で、`ADDR tmp base`・`ADDR tmp base >field`・`ADDR tmp base indexOp`のいずれかを直接発行する。**この過程で、既存の`genReceiverOperand`(値レシーバー/ポインタレシーバー自動変換、Step 8由来)に潜在バグを発見・修正した**——旧`isAddressable`が識別子オンリーだったため`RecvNeedsAddr`(値レシーバーの構造体をポインタレシーバーメソッドへ渡す際の暗黙ADDR)は今まで常に裸の識別子にしか発生せず問題が顕在化しなかったが、`isAddressable`をフィールド/添字まで拡張した今、`genReceiverOperand`が(拡張前と同じロジックのまま)`genValue(g, call.Receiver)`でレシーバー式を評価してからADDRしていたら、`b.pt.getX()`のようなフィールド経由のポインタレシーバー呼び出しで、コピーをADDRする同じ欠陥が発生するところだった。修正として`genReceiverOperand`も`genAddrOfOperand`を直接使うようにした。**この一連の経緯は「新しい構文・機能を実装したら、既存の周辺コードとの組み合わせも含めて実地検証する」という、本プロジェクトで繰り返し確認されてきた教訓を裏付けるものだった**——`isAddressable`単体のテストだけでは発見できず、既存の値/ポインタレシーバー自動変換との組み合わせで初めて表面化する種類のバグだった。

**2. `callname`オペランドカテゴリへの`$N`/`&N`追加により、`closureCallTarget`(`internal/codegen/closure.go`)のコピー回避ロジックを単純化した。** 従来は`%xxx`/`@xxx`以外の全てのトークン(`$N`/`&N`を含む)を一時変数へ`SET`でコピーしてから呼び出していたが、`callname`が`$N`/`&N`を直接受け付けるようになったため、コピーが必要なのは`@xxx`(グローバルなクロージャー変数。今も`callname`カテゴリに含まれない)だけになった——`applyTwice(f: func(int): int, x: int): int { return f(f(x)) }`のようなプログラムで、パラメータ`f`(`$1`)を経由したクロージャー呼び出しが、コピーを介さず`CALL %tmp : $1 ...`という形へ直接コンパイルされるようになった(実地検証: `amivm`→`go build`→実行、および回帰テスト`TestGenerate_ClosureCallThroughParameterUsesDollarTokenDirectly`)。

**3. `MPKEYS`命令の追加により、`for k, v in m`の実装(`internal/codegen/map.go`の`genForInMapStmt`)から`cascadert.Keys`ヘルパー呼び出しを削除し、`MPKEYS`を直接発行するよう置き換えた。** `MPKEYS single1 single2`は`single1 = slices.Collect(maps.Keys(single2))`を生成する(amivm標準ライブラリの`slices`/`maps`パッケージを利用、`amivm_spec.md`4.16節)。これによりCascadeが自前のGoランタイム(`cascadert`パッケージ)を必要とする箇所が完全になくなったため、**`cascadert/`ディレクトリ・`cmd/cascade/build.go`の`writeCascadert`・amivm呼び出しの`-i cascadert=cascadebuild/cascadert`引数を全て削除した**(下記「独自のGoランタイムを呼ぶ」節参照)。この方式(独自Goランタイムパッケージの`go:embed`配布)自体はSeedの`seedrt`から踏襲した仕組みとして今後も使える設計のまま残っており、将来別のビルトインが必要になれば復活させられる。

**4. `value1`/`value2`オペランドカテゴリへの`!xxx_123`/`?xxx_123`/`?xxx_123.xxx_123`追加(トップレベル関数・Go関数参照を値として渡せるようにする拡張)は、amivm側では実装されたが、Cascade側の追従は意図的にスコープ外とした。** これは「クロージャーとして定義しなかった生の関数を値として扱えない」という別の制約(Step 9の「確定した設計判断」参照)を解消するもので、`filter(numbers, isEven)`のようにトップレベル関数を直接値として渡せるようにする、独立した意味論設計項目(呼び出し可能解決の優先順位への組み込み、`ast.Ident`が指すのが変数かトップレベル関数かの曖昧性解消など)を要する。今回の追従は「前の命令では回避していたものを新しい命令に置き換える」という3項目(ADDR/callname/MPKEYS)に絞り、この項目は別途ユーザーと相談の上で着手する。

### AMIVM命令改修・第2弾(callnameへの`@xxx_123`追加、CLOSの左辺`local`→`shallow`)への追従

前節の改修(ADDR拡張・callname拡張・MPKEYS追加)を踏まえて`cascade_implementation_notes.md`(Seedの知見の続編にあたる知見メモ)を書き起こしたところ、ユーザー自身がその内容を元に2つの追加改修をamivm本体へ加えた。Cascade側もこれに追従した。**当初はこのファイルを`amivm/`配下(Cascadeリポジトリの一部ではない参照用クローン)に置いていたが、amivm側の更新のたびに`git clone`し直されることで2度ファイルごと消失する事態が発生し、「各言語の実装知見はその言語自身のリポジトリで管理する」方針へ転換、以降はCascadeリポジトリ直下(`cascade_implementation_notes.md`)で管理している(下記「リポジトリ構成」参照)。**

**1. `callname`オペランドカテゴリへの`@xxx_123`(グローバル変数)追加により、`closureCallTarget`(`internal/codegen/closure.go`)のコピー回避ロジックが完全に閉じた。** 前節の改修で`$N`/`&N`は直接呼び出し対象にできるようになっていたが、`@xxx`(トップレベル`let`/`const`で宣言されたクロージャー型のグローバル変数)を直接呼ぶケースだけがコピーを要求されたまま残っていた(`let doubler: func(int): int = ...; doubler(21)`のようなコードが`SET %tmp @doubler` → `CALL %tmp 21`という形になっていた)。`@xxx`追加により`genValue`がクロージャー型の式に対して返しうる全てのトークン形(`%xxx`/`@xxx`/`$N`/`&N`)が`callname`カテゴリに揃ったため、`closureCallTarget`のコピー分岐は事実上到達不能になった(`none`リテラルのような`callname`に現れえない形への防御としてのみ残している)。

**2. `CLOS`の左辺オペランドカテゴリが`local`(`%xxx_123`のみ)から新設の`shallow`(`$N`/`%xxx_123`/`@xxx_123`)へ変わったことで、クロージャーリテラルの代入(`let f = func(...) {...}`・トップレベル`let`・単純な再代入)が、必ず新しい一時変数へ`CLOS`してから`SET`でコピーしていた無駄を回避できるようになった。** `SLMAKE`/`MPMAKE`/`CHMAKE`は元々左辺が`single`(`$N`/`&N`/`%xxx_123`/`@xxx_123`)で、構築した値を目的の変数へ直接書き込めていたのに対し、`CLOS`だけが`local`限定という既存の不整合があった——今回の`shallow`追加はこの不整合を解消するものであり、Cascadeにとっては新機能というより「前から使いたかった書き込み先」が使えるようになった形。`genClosureLit`(常に新規一時変数へ`CLOS`する、クロージャーリテラルが呼び出し引数など単独の代入先を持たない文脈で使う汎用版)から、目的のトークンへ直接`CLOS`する`genClosureLitInto`を分離し、`genInit`(`let`宣言・単純代入の値設定。既存の`ListLit`/`MapLit`と同じ立ち位置で`ClosureLit`の分岐を追加)がこちらを直接呼ぶようにした。

**`shallow`が`&N`(クロージャー引数)を含まない設計は、ユーザー自身が実装前の技術レビューの段階で見抜いた点であり、正確だった。** `CLOS`はネスト不可(`CLOS`の中に`CLOS`を書けない)であるため、`CLOS`命令が発行される場所は常に「ある`FUNC`本体の直下」に限られ、`CLOS`本体の中から`CLOS`を発行する状況が構造的に存在しない——`&N`はクロージャー本体の中でしか意味を持たないトークンなので、`CLOS`の左辺として`&N`が現実に出現するケースは原理的にゼロになる。既存の`single`(`&N`を含む)をそのまま流用しても実害は無かったが(正しいフロントエンドが`&N`を左辺に出そうとすること自体がありえないため)、「無効な状態をそもそも表現できなくする」という設計品質の観点で`shallow`という専用カテゴリを新設する方が優れている、という判断は技術的に正しかった。

`amivm`→`go build`→実行での実地検証は、クロージャーをローカル変数・トップレベル変数それぞれへ直接代入するケースと、トップレベル変数経由でクロージャーを呼び出すケースの3パターンで実施し、全て`CLOS`/`CALL`が一時変数を介さず直接目的のトークンを使うIRになっていることを確認済み(回帰テスト`TestGenerate_ClosureLitAssignedToLocalSkipsTempCopy`/`TestGenerate_ClosureLitAssignedToGlobalSkipsTempCopy`/`TestGenerate_ClosureCallThroughGlobalUsesAtTokenDirectly`)。既存13サンプル+`examples/14_packages`も全て再実行し、退行が無いことを確認した。

**追記(第3弾)**: 上記の「`CLOS`はネスト不可であるため`shallow`に`&N`を含めないのは正しい」という判断は、その時点のamivm仕様に対しては正確だったが、直後に`CLOS`自体がネスト可能になったことで前提が崩れた。amivm側は`shallow`という専用カテゴリそのものを廃止し、`CLOS`の代入先を(`&N`を含む)`single1`へ統合する形で対応した——詳細・Cascade側の追従は次節参照。

### AMIVM命令改修・第3弾(`CLOS`のネスト対応)への追従、クロージャーのネスト(カリー化)実装

amivm本体が`CLOS`のネストを許すよう改修された(クロージャーを返すクロージャー、カリー化を表現するため)。これに伴いクロージャー引数`&N`の意味が「常に`amivm_closure_paramN`という固定名」から「**自分がいる`CLOS`階層のN番目**」に変わり、内側の`CLOS`から外側の階層を明示参照する`&L-N`という記法が追加された(`L`は`FUNC`直下を1とするネスト深さ)。`CLOS`の代入先カテゴリだった`shallow`(`$N`/`%xxx`/`@xxx`)は廃止され、`&N`/`&L-N`も含む`single1`へ統合された(ネストを許すと、内側の`CLOS`から見て外側のクロージャー引数へ代入し直すことが`$N`への代入と同じ扱いで自然になるため)。

**Cascade側は「amivmの制約により未対応」と明記していたクロージャーのネスト(カリー化)を、この改修を受けて実装した。** sema側は`checker.closureDepth`によるネスト拒否(`checkClosureLit`の`if c.closureDepth > 0 { return error }`)を単純に削除するだけで済んだ——スコープの親子連鎖(`newScope(sc)`)・`currentFuncResults`のsave/restore・`checkStmtsIn`の`loopDepth=0, breakDepth=0`リセットは、いずれも元から再帰呼び出しのたびに独立して動く設計だったため、ネストの深さを明示的に追跡するコードを一切追加せずに、Goの呼び出しスタックだけで任意の深さのネストが正しく扱えた(`closureDepth`フィールド自体は用済みとなり削除)。

**codegen側は、クロージャー引数トークンを常に完全修飾形`&L-N`で発行する設計にした(bareな`&N`省略形は一切使わない)。** 一見「自分自身のクロージャーの引数を参照するだけなら省略形の`&N`で十分では」と思えるが、`varRef.ValOp`は宣言時に一度だけ決まる固定文字列で、以降はどの深さの子クロージャーから参照されようと同じ文字列がそのまま再利用される(§9の「スコープの親子関係だけで捕捉が実現できる」という既存の仕組みそのもの)。bareな`&N`を宣言時に格納すると、それを宣言したクロージャー自身からの参照では正しく解決できても、**さらに内側のクロージャーがそれを捕捉して参照する際には「自分自身のN番目」と誤読される**(ambiguityが生じる)。完全修飾形`&L-N`はどの深さから参照されても常に一意に解決できるため、これを避けるには「宣言時に完全修飾形で保存する」以外の選択肢が無かった。`funcGen`に新設した`closureDepth`フィールド(0=関数本体直下、CLOS内なら自分の階層L)で追跡し、`genClosureLitInto`が`&{depth}-{argIdx}`という形で全パラメータトークンを発行する。

**副産物として、既存の閉じたクロージャー(ネストしない従来の全ケース)の生成IRも`&1`から`&1-1`のような完全修飾形に変わった。** 意味的には従来と完全に同一(amivm仕様上、bare`&N`と`&{自分の階層}-N`は等価)だが、全ての既存回帰テストの文字列アサーションを更新する必要があった(`TestGenerate_ClosureLitEmitsFNTYPEAndCLOS`)。新たに`TestGenerate_NestedClosureLitUsesQualifiedParamTokens`を追加し、2階層ネストしたクロージャー(`curry(a)(b) = a + b`)が外側の引数(`&1-1`)と内側自身の引数(`&2-1`)を正しく区別して参照すること、bareな`&N`が一切生成されないことを確認した。`examples/09_closures.cas`に`curry`の例を追加し、`amivm`→`go build`→実行まで確認済み(`curry()(5)(10)`相当を`let`で一段ずつ束縛する形で実行し、`15`を得た)。

**既知の未対応事項として記録**: `curry(a)(b)`のように、呼び出し式の結果へ直接もう一段`(...)`を続けて書く「連続呼び出し」構文は、今回は未対応のまま残した。パーサーの式解析(`parsePrimary`の後置ループ)が`[`・`.`・`?`という後置演算子はループで連続して受け付けるのに対し、呼び出し`(...)`はそもそもこのループに含まれておらず、`ast.CallExpr.Callee`が(任意の式ではなく)常に名前文字列という設計になっている——sema側の呼び出し可能解決(`resolveClosureCall`)もcodegen側の対応する解決ロジックも、この「呼び出し対象は名前で引く」という前提の上に築かれている。連続呼び出しを許すには`Callee`を`Expr`へ一般化する必要があり、これは今回のAMIVM改修が要求する範囲(クロージャーリテラルのネストそのものを表現可能にする)を超える、独立した大きめのパーサー/意味検証/コード生成の設計変更になる。現状は`let f = outer(a); f(b)`のように一段ずつ`let`で束縛すれば呼び出せる(`examples/09_closures.cas`の`curry`の例もこの形)。仕様に無い組み合わせは先送りし、曖昧な動作にしないという本プロジェクトの一貫した方針に沿って、今回は着手しないことにした。

### 文字列リテラルのエスケープシーケンス対応(AMIVM文字列/ルーンリテラル修正を受けて)

amivm本体で文字列・ルーンリテラルのトークナイザにあったバグ(`\"`を含む文字列が丸ごと無効判定になる、ルーンリテラルが`\n`等の名前付きエスケープや`\uXXXX`/`\UXXXXXXXX`を一切受け付けない)が修正された。この修正自体はCascadeの出力サーフェスに影響しなかった(Cascadeの旧`lexString`はエスケープ機構を一切持たず、ソース上で`"`を含む文字列を書く手段が無かったため`\"`を生成しようがなく、ルーン/文字リテラル型自体もCascadeには存在しない)が、**「AMIVM/Goがエスケープシーケンスをどう扱うべきか」という仕様がamivm側で明文化されたのに合わせて、Cascade自身の言語機能としてもエスケープシーケンスに対応した**(ユーザーからの依頼)。

**Cascadeの文字列リテラルのエスケープ仕様は、Goの二重引用符文字列リテラルの文法をそのまま採用した(cascade_spec.md 3.1節)。** `\a` `\b` `\f` `\n` `\r` `\t` `\v` `\\` `\"`(名前付きエスケープ)、`\xHH`(16進2桁、バイト値)、`\ooo`(8進3桁、バイト値。256以上はエラー)、`\uXXXX`(16進4桁、Unicodeコードポイント)、`\UXXXXXXXX`(16進8桁、Unicodeコードポイント)を全てサポートする。amivm側がルーンリテラルでは`\x`/8進数エスケープを意図的に対象外としているのに対し、**文字列リテラルではamivm側もこれらを除外していない**(amivmの文字列トークナイザは「`\`に続く任意の1文字」という単純な繰り返しパターンでしかなく、`\xHH`のような複数文字からなるエスケープも「`\x`という2文字エスケープ+後続の2文字はただの非`"`文字」として自然に受理できるため、除外する技術的な理由が無い)。ルーン側で除外されているのは「ルーンリテラルは1文字だけを表すため、可変長のバイト/コードポイント指定は`\u`/`\U`だけで表現力として十分」という設計判断であり、文字列側には同じ理由が当てはまらない。

**実装は字句解析(`internal/lexer/lexer.go`)の`lexString`のみで完結し、parser/ast/sema/codegenは一切変更していない。** `lexString`がエスケープを字句解析の時点で実際のバイト/ルーンへデコードし、`ast.StringLit.Value`には常に「デコード済みの本当の文字列値」が入る設計にした(Goの字句解析自身と同じ考え方——ソース上の`"a\nb"`はレキサーの時点で実際の改行文字を含む3文字の値になり、以降のフェーズは生の`\n`という2文字を意識しない)。この設計により、codegen側の`strconv.Quote(v.Value)`(Step 3から存在する、文字列リテラルをIRへ出力する唯一の箇所)は**一切変更不要**だった——`strconv.Quote`は元々どんなGo文字列値を渡しても正しくエスケープ済みのGoソース文字列リテラルを生成する関数なので、値の出どころ(単純な文字列かエスケープをデコードした結果か)を区別する必要が最初から無い。「新しい構文要素の対応範囲を可能な限り上流のフェーズに閉じ込め、下流のフェーズを一切変更しない」という設計が最小の変更で実現できた例。

**エスケープ処理の実装で発生した唯一の非自明な設計判断: 8進エスケープの3桁目までの読み取り方。** `\ooo`はGo同様「最初の1桁(0〜7)を読んだ時点で8進エスケープと判定し、残り2桁を読んで合成する」という実装にした(`readEscapeDigits(2, 8, line)`が残り2桁を読み、呼び出し側で1桁目の位取り(`×64`)を加算する)。値が255を超える場合(`\400`以上)は明示的にエラーにする——Go自身も同じ制約(3桁8進数の最大値511のうち256以上は不正)を持つため、ここは仕様通りに合わせた。

**16進/8進の桁数が足りないまま閉じクォート`"`やEOFに達した場合のエラーメッセージが、原因によって2パターンに分かれる。** 桁を読んでいる途中でファイル末尾または改行(文字列内の生の改行は元々禁止)に達した場合は「unterminated string literal」、桁の代わりに`"`(閉じクォート)のような非16進/非8進文字に遭遇した場合は「invalid escape sequence digit」になる——後者は一見「桁数不足」の方が直感的な原因に思えるが、実装上は`"`という文字そのものが「有効な桁ではない」ことを検出しているだけなので、素直にこのメッセージになる。両方とも回帰テスト(`internal/lexer/lexer_test.go`の`TestLexString_EscapeErrors`)で個別に確認済み。

`internal/lexer`パッケージにこれまでユニットテストが存在しなかった(sema/codegenレイヤーのテストのみだった)ため、今回`lexer_test.go`を新設し、正常系(全エスケープ種別+非エスケープの生Unicode文字列との混在)・異常系(未知のエスケープ・桁不足・不正な桁・8進数の範囲超過)を直接`Tokenize`関数へ通す形でカバーした。`amivm`→`go build`→実行でも、`\"`(今回のamivm修正の直接のトリガーとなったケース)を含む複数のエスケープを実際に`print`し、生成されたIRの文字列トークンが`strconv.Quote`によって正しく再エスケープされていることを確認済み。

### `IF`/`LOOP`のブロック化・`SEL`ケースのブロック化・`ASSERT`追加への追従(AMIVM命令改修・第4弾)

amivm本体で制御フローの表現方法が全面的に刷新された。単一行`IF boolean1 label`(条件付き`goto`)が廃止され、ブロック形の`IF`/`ELIF`/`ELSE`/`ENDIF`(Goの`if`/`else if`/`else`)と、新設の`LOOP`/`BREAK`/`CONTINUE`/`ENDLOOP`(Goの無限`for {}`)に置き換わった。これに伴い`IF`/`LOOP`/`CLOS`/`SEL`が全てネスト可能に統一され(`CLOS`は元々ネスト可能だった)、`SEL`の`CASESEND`/`CASERECV`/`DEFAULT`も`label`を取らずブロック本体を持つ形に変わった。加えて`any`型からの型アサーション用に`ASSERT`が新設された。**後方互換は無い改修**(旧`IF boolean1 label`は仕様から完全に削除)だったため、Cascadeの制御構文コード生成(`internal/codegen`)を全面的に書き換える必要があった。

**この規模の破壊的変更を安全に乗り切れたのは、Seed(`github.com/amisonnet8/seed`)が同じ改修に一足先に追随し、`seed_implementation_notes.md`に踏んだ地雷と確立したパターンを記録していたためだった。** Cascadeの`while`・`for`(list/map)の書き換えはSeedの`genWhileStmt`/`genForInStmt`(`seed/internal/codegen/stmt.go`)をほぼそのまま移植する形で済んだ——「事前に知見のある移行」と「ゼロから設計判断する移行」の労力差を体感する良い例だった。ただしCascadeには`switch`(Seedに無い構文)・チャネル`for-in`(SEL絡み)・`SEL`を使う`merge`/`abort`/`collect`など、Seedに前例の無い箇所も多く、それらは独自に設計した(下記)。

**`genIfStmt`(if/elif/else)は、`ELIF`トークンを直接使わず`ELSE`の中に次の`IF`をネストする形で自前に構築する。** `ELIF boolean1`のオペランドは、そのIR行に到達した時点で既に値になっている変数/リテラルでなければならず、`a + b < c`のような複数命令を要する条件式を「1つ前の節の`}`」と「`} else if`」の間に挟む構文上の余地が無い。`ELSE`の中に次の`IF`をネストする形にすれば、各節の条件計算命令をそれを守る`ELSE`の中に自然に置け、しかも「前の条件が真なら後の条件は一切計算しない」という短絡評価も無料で手に入る(amivm自身が`ELIF`を解析する際も内部的にこの構造へ変換しているため、生成されるGo ASTは`ELIF`を使った場合と完全に同一になる)。`switch`のcase値についても同じ理由・同じ形で「1つの値をIFでチェック→ELSEの中に次の値、または次のcase、またはdefaultをネスト」という完全再帰の構造にした(`genSwitchCase`)——これは1つのcaseに複数値がある場合(`case 1, 2, 3:`)の値ごとの短絡評価も、旧実装(フラットなIF-GOTO連鎖で、値が1つ一致した時点で残りを評価せずジャンプしていた)と全く同じ挙動を保つための選択で、単純にOR演算子で複数値の条件を合成する案(実装は簡単だが、case値が副作用を持つ式の場合に評価回数が変わってしまう)は採らなかった。

**`switch`はamivmにネイティブ命令が無いため、今回もIF/ELSEのネストへdesugarするCascade独自の実装のまま変わらない。** これが決定的に重要な帰結を生んだ: **`break`(switchを抜ける)と`continue`(switchを素通りして囲むループへ抜ける)を、Go自身の`break`/`continue`キーワードの挙動だけでは正しく実装できない。** Goの`break`は最も内側の`for`/`switch`/`select`を抜けるが、Cascadeの`switch`はGoの本物の`switch`ではなくただのネストした`if`なので、Go側には「switchを抜ける」という受け皿が存在しない——`switch`のcase本体で素の`BREAK`を使うと、（switchを内包する）外側の`LOOP`をGoの通常のスコープルールに従って抜けてしまい、意図と異なる。一方Goの`continue`は最も内側の`for`にしか効かず、間に挟まる`if`/`switch`/`select`は素通りする、という性質を逆手に取れば、`continue`は`LOOP`の中にネストした`IF`をいくつ挟んでいても、Cascadeが期待する「switchを素通りして囲むループへ」という挙動をGo自身が保証してくれる。**この非対称性(breakは特別扱いが要るがcontinueは要らない)がCascadeの`switch`設計の核心であり、Step 5で最初に発見しStep 5の「確定した設計判断」に記録済みだったものが、今回の移行でも変わらず有効だった。**

**`breakStack`/`continueStack`の要素を、単なるラベル名の`string`から、`breakTarget{Label string}`/`continueTarget{Label string}`という小さな構造体に変更した。** `Label`が空文字列なら「ネイティブの`BREAK`/`CONTINUE`を発行する」、非空なら「そのラベルへ`GOTO`する」という2つのモードを切り替える。ループ(while・list/map for-in・channel for-in)の`break`は常にネイティブ(`Label`空)——`LOOP`が本物のGoの`for`を生成するため、最も内側の`LOOP`を抜けるというGoの`break`の意味論がそのままCascadeの要求と一致する。`continue`は「ループ本体の先頭で毎回条件を再チェックする」形(while・channel for-in)ならネイティブで足りるが、「本体の**後**にインデックス更新がある」形(list/map for-in)だけは、ネイティブ`CONTINUE`だと`LOOP`の先頭(長さの再チェック)へ戻ってインデックス更新を飛ばしてしまうため、更新の直前に置いた専用ラベルへの`GOTO`が必要(Seedの`loopInfo`と全く同じ設計)。`switch`の`break`は常にラベルベース(`Label`非空、if-chainの外に置いたラベルへ`GOTO`)——switch自体がネイティブに抜けられる構造を持たないため。`switch`は`continueStack`には一切触れない(元々の設計のまま)。

**LABEL/GOTOで踏んだバグ第2弾: switch自身の終端ラベルが、そのswitch本体が一度も`break`しない場合に「定義されているが誰からも参照されない」状態になり、`go/types`の「未使用ラベル」エラーになる。** これはSeedの`seed_implementation_notes.md`§2.2が記録していた「for-inのcontinueラベルが、本体に`continue`が無いと未参照になる」バグと全く同じ形のバグだが、**switchの`break`という別の場所で独立に踏んだ**(移行時に横展開できていなかった箇所)。対策も同じ: switch本体(if-chain)の通常のフォールスルー末尾に、無条件の`GOTO`をラベルの直前へ置き、`break`が一度も呼ばれないswitchでもラベルが必ず参照される状態にした(`genSwitchStmt`)。**この教訓を活かし、今回のテストインフラ改善として`assertLabelsResolve`(`codegen_test.go`)を「参照→定義」だけでなく「定義→参照」の両方向でチェックするよう拡張した**——ラベルが定義されているのに一度も参照されない、というこの種のバグをテストレベルで機械的に検出できるようにした。

**チャネル絡みの`SEL`(`genForInChannelStmt`のチャネル受信・`genSendCall`の送信・`genAbortCall`のガード・`genCollectorFunc`/`genMergeFunc`の合成関数)は、いずれも`CASESEND`/`CASERECV`/`DEFAULT`のブロック本体化によって大幅に簡潔になった。** 旧実装は「各ケースが成功したらどのラベルへ`GOTO`するか」を`SEL`の外側で個別に管理する必要があったが、新実装では各ケースの処理をそのケース自身の本体に直接書けるため、`genMergeFunc`(2チャネルのファンイン)は最終的に**`LABEL`/`GOTO`を1つも使わない**形にまで単純化できた(`LOOP`の中で「両方閉じたか判定→`IF`で`RET`」「`SEL`の各ケース内で`IF ok`により転送/`SET nil`」という素直な構造だけで完結する)。`genCollectorFunc`も同様に、`SEL`の各`CASERECV`本体へ直接ロジックを書き込む形に整理した。

**この規模の変更にもかかわらず、Cascadeの言語仕様(`cascade_spec.md`)・AST(`internal/ast`)・意味検査(`internal/sema`)は一切変更していない。** amivmのIF/LOOP/SELがどう表現されるかはCascadeにとって完全にcodegen内部の実装詳細であり、`if`/`while`/`for`/`switch`/`break`/`continue`というCascade言語自体の構文・意味論は寸分変わっていない——「意味検証の責任分担」の原則(sema/codegenの独立性、AMIVM-IRの表現詳細をsemaが知る必要がない)がここでも効いた。

**VAR巻き上げの仕組み(関数先頭への全`VAR`宣言の集約)は変更せず維持した。** ブロック形`IF`/`LOOP`が本物のGoブロックスコープを生成するようになったことで、理論上は「gotoが変数宣言を飛び越える」問題(seed_implementation_notes.md §1)の大部分は解消されている——しかし今回もswitchの`break`・list/map for-inの`continue`という2箇所でLABEL/GOTOを使い続けており(上記参照)、これらのジャンプが将来何らかの理由でVAR宣言を飛び越える可能性を完全に排除できない。VAR巻き上げを維持しておけば、どんなLABEL/GOTOが生成されようと「まだ宣言されていない変数を飛び越える」状況が構造的に発生しなくなるため、追加の安全確認コストなしにこのリスクを消せる。Cascadeの変数命名スキーム(シャドーイング対応の一意な名前採番、`scope.go`)も元々この巻き上げに依存しているため、変更する積極的な理由も無かった(Seedが全く同じ判断をしていたことも確認済み)。

**`ASSERT`(型アサーション)はCascade側で一切使わない設計のまま据え置いた。** `any`相当の型がCascadeの型システムに存在しない(スカラー4種・構造体・ポインタ・リスト・map・チャネル・関数型・`error`のみ)ため、`v.(T)`という操作自体が意味を持つ場面が無い。「無理に全命令を使う必要はない」という本プロジェクトの努力目標に照らし、今回追加された`ASSERT`は意図的に「命令使用ゴール」表で対象外として記録した。

**検証は既存の単体テスト修正に加えて、新旧amivmバイナリの並行運用(`/go/bin/amivm`=新・`/go/bin/oldamivm`=旧)を活かし、新バイナリで`amivm`→`go build`→実行の全パイプラインを回した。** 既存13サンプル+`examples/14_packages`が全て無変更で通ることに加え、elif連鎖の短絡評価・switch内breakがループを巻き込まないこと・switch内continueが囲むループへ正しく抜けること・多値case・深いネスト(while内if内whileにswitch)・list/map/channel双方のfor-inにおけるbreak/continue・パイプラインのステージ内for-inでのbreak/continueの組み合わせ、を専用の使い捨てサンプルプログラムで個別に実行し、全て手計算どおりの結果になることを確認した。

## 意味検証の責任分担(重要)

型の整合性・未定義識別子・関数シグネチャの不一致・メソッドの存在チェックなどは、**amivm側では検証せず`go/types`に全面的に委ねている。** amivmが保証するのは「構文的に妥当なGoコードを出力すること」だけ。

これはつまり、**Cascadeの意味検査(型チェック・スコープ解決・null許容型の絞り込み・パイプラインの型接続検査など)はCascadeコンパイラ自身が担う必要がある** ということ。AMIVM-IR生成前の段階で、Cascade言語仕様(`cascade_spec.md`)に基づく検査を完了させておくこと。IRを間違って生成した場合のエラーは、amivmの`go/types`型チェック失敗という形で(生成されたGoコードのエラーとして)返ってくるため非常にわかりにくい。**Cascadeコンパイラ自身が、ユーザーに分かりやすいエラーメッセージをamivm呼び出し前に出せるようにすること**が望ましい。

なお、amivmは生成コード中の未使用変数を自己修復する仕組み(`_ = 変数名`の自動挿入)を内部に持っている。**これはamivm側の責務であり、Cascadeのcodegenが一時変数の使用漏れを気にする必要は無い。**

## 独自のGoランタイムを呼ぶ

`amivm`は`?pkg.Func`+`CALL`の仕組みで任意のGo関数を呼べる。Cascadeのビルトイン関数のうち、単純な演算命令に対応しないものが将来必要になれば、Cascade自身が用意するGoランタイムパッケージ(Seedの`seedrt`に相当)の関数として実装し、生成したIRから`CALL`で呼び出す、という方式を使える(`cascadert/embed.go`が自身の`.go`ファイルを`go:embed`で埋め込み、`cmd/cascade/build.go`がビルド時にスクラッチディレクトリへコピーし`amivm`の`-i`で解決する、というSeedの`seedrt`/`writeSeedrt`と同じ配布方式)。

**この方式は一度(Step 10、`for k, v in m`用の`cascadert.Keys`ヘルパー)導入したが、AMIVM本体に`MPKEYS`命令が追加されたことでネイティブ命令に置き換わり、`cascadert`パッケージ自体は完全に削除した**(下記「確定した設計判断」参照)。現時点でCascadeが自前のGoランタイムを必要とするビルトインは無い——新しいビルトインが、既存のAMIVM命令の組み合わせでは実装できない何かを必要とする時点で、改めてこの方式を復活させる。

## 過去に踏まれた地雷(Seedからの申し送り。Cascadeでも起こりうる)

詳細はSeed自身のリポジトリ(`github.com/amisonnet8/seed`)の実装知見を参照(各言語の実装知見はその言語自身のリポジトリで管理する方針のため、このリポジトリには同梱していない——下記「リポジトリ構成」参照)。特に重要なものを抜粋。

1. **goto/VAR巻き上げ問題(最重要)**: `LABEL`/`GOTO`が生成するGoコードは(ブロック構造を経由しない場合)1関数内のフラットな命令列であり、`goto`は「まだスコープに入っていない変数宣言」を飛び越えられない(Goのルール)。ブロック形の`IF`/`LOOP`(下記「AMIVM命令改修・第4弾」参照)が本物のGoブロックスコープを生成するようになったことで、素直な`if`/`while`/`for`相当の構文を書くだけならこの問題自体は発生しなくなったが、構造化制御フローだけでは表現できないジャンプ(switchの`break`、添字カウント式for-inの`continue`)には今も`LABEL`/`GOTO`が必要であり、そこでこの問題が再発しうる。対象言語の関数・クロージャー・**ステージ関数**ごとに、使う`VAR`を全て先頭に巻き上げ、初期化は元の位置に`SET`だけ残す、という対策は今も維持しておくこと(理論上は不要な範囲が広がったが、シャドーイング用の命名スキームがこの巻き上げに依存しているため変更する理由も無い)。`CLOS`本体も独立した1関数スコープなので同様の巻き上げが必要
2. **スコープはGo側で完全にフラット**: シャドーイングを許す言語仕様(10節)に対して、codegenは内部で一意な変数名を採番すること
3. **`CALL`はキャストにも使われる**: `string(intVal)`のような数値→文字列変換はGoの素の型変換だと`"A"`のようなルーン変換になる罠がある(`strconv`を使うこと)
4. **`CALL`の結果省略は本当に省略する**: 空文字列オペランドではなく`CALL : callname ...`のように空にする
5. **参照渡し/値渡しはGo表現に従う**: スライス・ポインタ・map・チャネル・関数値はコピーせずそのままローカル変数として使い回す。スカラーはコピーしてよい
6. **`fmt.Sprintf`で動的にIR行を組み立てる際、オペランド中の`%`をフォーマット文字列に直接連結しない**(`%s`引数として渡す)
7. **`STTYPE`/`SEL`はネスト不可**。関数内ローカルな構造体定義を許す言語仕様なら、全てトップレベルへ引き上げる必要がある(Cascadeの`struct`は元々トップレベル宣言のみなので該当しない見込みだが、クロージャー内で新たな構造体を作る発展的な構文を将来追加する場合は要注意)。**`CLOS`だけは例外で、`CLOS`本体の中に`CLOS`をネストできる**(ネストするごとにクロージャー引数のトークンが`&L-N`という階層付きの形へ変わる。下記「確定した設計判断」参照)

## リポジトリ構成(予定・未実装)

**`/workspaces/cascade`(このディレクトリ自体)がCascadeのホーム。** Seed(`seed/`)・amivm(`amivm/`)はそれぞれ自分自身が独立リポジトリのルートで、Cascadeから見た参照用クローンとしてこの階層に置かれている。Cascade自身の実装知見(`cascade_implementation_notes.md`)はCascade自身のリポジトリで管理し、Seedの実装知見はSeed自身のリポジトリ(`github.com/amisonnet8/seed`)で管理する——「各言語の実装知見はその言語自身のリポジトリで管理する」方針(前節参照)。

```
/workspaces/cascade/            Cascadeのホーム(このリポジトリのルート)
  cascade_spec.md               Cascade言語仕様(唯一の正)
  cascade_implementation_notes.md  Cascade自身の実装知見(AMIVM-IRを生成するフロントエンドを
                                 書く上での一般的な知見)
  CLAUDE.md                     本ファイル
  seed/                         Seedの参考実装(参照用。Cascade本体の一部ではない)
  amivm/                        amivmのローカルクローン(参照用。Cascade本体の一部ではない)
  README.md / README_ja.md      導入ドキュメント(実装が固まってから作成)
  go.mod                        module github.com/amisonnet8/cascade (想定)
  Makefile                      build/install/test/fmt/vet/tidy/clean タスク
  cmd/cascade/
    main.go                     CLIエントリポイント(build/run/emit-ir/emit-go/help のディスパッチ)
    build.go                    pkgloader.Load → sema.Check → codegen.Generate → amivm → go build の
                                 パイプライン。srcPathは単一.casファイル、またはパッケージ
                                 ディレクトリのいずれも受け付ける(pkgloader.Loadの項参照)
  internal/lexer/               字句解析
  internal/parser/              構文解析 → AST
  internal/ast/                 AST定義
  internal/pkgloader/           パッケージ/importの解決(11節。Seedには無かった新規レイヤー)。Step 14で実装済み。
                                 複数.casファイル・複数パッケージを1つのフラットな*ast.Fileへ解決してから
                                 sema/codegenへ渡すため、両者はパッケージという概念を一切知らない
                                 (下記「確定した設計判断」参照)
  internal/sema/                意味検査(型チェック・スコープ解決・null絞り込み・パイプライン型接続検査)
  internal/codegen/             AST → AMIVM-IR生成
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
| 11 ✅ | エラー処理 | `error`型・`(T, error?)`規約・後置`?`のsema展開(8.6節) | (新規命令なし。`STTYPE`+`IF`+`RET`の組み合わせ) | `error`型の表現を確定(下記「確定した設計判断」参照。旧「オープンな設計課題」課題2) |
| 12 ✅ | パイプライン基礎 | `source`/`stage`/`sink`、`chan<T>`、`send`、`for v in channel`、`\|>`連結(9.1/9.2節) | `CHTYPE` `CHMAKE` `CHSEND` `CHRECV` `SPAWN` `DEFER` | パイプラインの並行実行モデルの一次決定。基礎部分を確定・実装(下記「確定した設計判断」参照。collect/abort/mergeはStep 13へ) |
| 13 ✅ | パイプライン拡張(collect/abort/merge) | `collect`(9.3節)・`abort`(9.4節)・`merge`(9.5節) | `SEL` `CASESEND` `CASERECV` `DEFAULT` `ENDSEL` | パイプラインの並行実行モデルを最終確定(下記「確定した設計判断」参照) |
| 14 ✅ | パッケージ/複数ファイル | ディレクトリ=パッケージの統合(11.1節)、`import`/`pub`(11.2/11.3節)、循環import検出(11.5節)、識別子一意化(11.6節)、トップレベル`let`/`const`(11.3節) | `GVAR`。新規AMIVM命令は無いが`GVAR`が初めて実際に使われた | パッケージ/モジュール解決を確定(下記「確定した設計判断」参照) |
| 15 ✅ | CLI・配布 | `cascade build/run/emit-ir/emit-go/help`、`cascadert`の`go:embed`配布、README作成 | — | — |

特にStep4(ビット演算)・Step8(ポインタ・構造体)・Step9(クロージャー)・Step10(map)・Step12/13(チャネル・SPAWN・SEL)はSeedで未実証だった命令なので、「ロジック上正しそうに見える」だけで次のステップへ進まないこと。設計上の未確定事項に着手する際は、方針を確定させたら「確定した設計判断」節に記録し、仮説のまま放置しないこと。

## 開発の進め方

1. `cascade_spec.md`を正として実装する。仕様に曖昧な点や矛盾を見つけたら、まず仕様側を疑い、確定させてからコードを直す
2. 新しい命令カテゴリ・構文を実装したら、実際に`amivm`(`PATH`にインストール済みのもの)にかけて`go build`まで通し、動作確認する。特にポインタ・構造体・map・クロージャー・チャネル/SPAWN/SELは**Seedで未実証だった命令**なので、「ロジック上正しそうに見える」だけで済ませない(seed_implementation_notes.md §6.1参照)
3. Cascadeの意味検査(型チェック・null絞り込み・パイプライン型接続等)は、amivmに渡す前にCascade側で完了させる。amivmの`go/types`エラーをユーザー向けエラーとしてそのまま出さない
4. 新しい構文・ビルトイン関数を実装したら、対応するサンプルCascadeプログラムを`examples/`に追加し、生成されたIR・Goコード・実行結果まで確認する
5. 設計上の未確定事項は、着手時に方針を確定させ、確定内容を「確定した設計判断」節(または実装コード側のdocコメント)に書き残す。仮説のまま放置しない
6. 新しい命令を使うたびに「命令使用ゴール」節の表を更新し、全命令の使用状況を追跡する
7. amivm本体の仕様が更新された場合(`amivm/docs/amivm_spec.md`を再確認)、本ファイルの「AMIVM-IRの書き方」節が古くなっていないか照合し、必要なら更新する
