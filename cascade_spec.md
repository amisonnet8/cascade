# Cascade 言語仕様

> AMIVM(独自の中間言語からGoコードを生成するコンパイル基盤)向けの2つ目のフロントエンド言語。
> 1つ目の言語Seedがスカラー・固定長配列・制御構文・関数程度しか使わなかったのに対し、
> Cascadeはポインタ・構造体・クロージャー・チャネル/goroutineを積極的に使う設計にしている。
> 言語名・構文・キーワードはSeedを踏襲しておらず、この言語独自のもの。

## 0. 設計の3本柱

1. **手続き型+レシーバー**: クラス・継承は持たないが、構造体に紐づく関数(レシーバー付き関数)を`obj.method()`の形で呼べる
2. **関数型的要素**: 関数は値として扱える(変数に代入・引数として渡せる)。`filter`/`map`/`reduce`のようなコレクション操作をクロージャーで書ける
3. **パイプラインによる縦の並列化**: `source |> stage1 |> stage2 |> sink`のように処理を連結すると、各段が独立したgoroutine相当として並行に動き、チャネル相当の仕組みでデータが順に流れる

上記に加えて、AMIVMの命令セットをできるだけ広く使う設計として、ビット演算・可変長mapを型システムに組み込み、複数戻り値・`switch`・Go/Rustを参考にしたエラー処理も持たせている(詳細は各節参照)。さらに、プログラムを複数の`.cas`ファイル・複数パッケージへ分割し`import`で結合する仕組みも持つ(11節)。

## 1. 字句規則

- 改行が文の区切り(セミコロン等は不要)
- `//`以降、行末までをコメントとして無視する
- ソースファイルの拡張子は`.cas`
- 1ファイル=1コンパイル単位ではない。同じディレクトリの`.cas`ファイル群は1つの**パッケージ**として統合される(11節)

## 2. 型

### 2.1 スカラー型

| 型 | 説明 | ゼロ値 |
|---|---|---|
| `int` | 整数型 64bit | `0` |
| `float` | 浮動小数点型 64bit | `0.0` |
| `string` | 文字列型 | `""` |
| `bool` | 論理型(`true`/`false`) | `false` |

### 2.2 複合型

| 型 | 説明 | ゼロ値 |
|---|---|---|
| `struct` | 名前付きフィールドの集合(4節参照) | 各フィールドのゼロ値を持つ構造体 |
| `*T` | `T`型へのポインタ | `none`(何も指していない) |
| `[]T` | `T`型の可変長リスト | 空リスト`[]` |
| `map<K, V>` | `K`型のキーから`V`型の値への可変長辞書(4.5節) | 空map`{}` |
| `chan<T>` | `T`型を運ぶチャネル(パイプライン専用。9節参照) | (単独では宣言しない) |
| `func(T1, T2, ...): R` | 引数型`T1, T2, ...`・戻り値型`R`を持つ関数の型(戻り値なしなら`: R`を省略) | `none` |
| `func(T1, T2, ...): (R1, R2, ...)` | 複数の戻り値を持つ関数の型(8.5節) | `none` |
| `error` | エラー値を表す組み込み型(8.6節)。実体は`message: string`を1つ持つ構造体的な値 | `none`(通常`error?`として使う) |

`map<K, V>`の`K`にはスカラー型(`int`/`float`/`string`/`bool`)のみ使える(`struct`/`[]T`/`map`自体はキーにできない)。`m[k]`は`V?`(要素が存在しない場合は`none`)を返す(2.3節のnull許容機構を再利用する)。

### 2.3 null許容型(`T?`)

任意の型`T`の末尾に`?`を付けると、その型は`none`を取りうる(null許容)。`?`の付かない型は`none`を取れない。

```
let x: int          // 通常のint。0で初期化される(noneにはならない)
let y: int?          // null許容int。宣言のみだとnoneになる
y = 5
y = none             // 明示的にnoneへ戻せる
```

`?`の付かない型の変数は宣言時点で必ずゼロ値を持ち、`none`という状態自体を取らない(Seedのような「全ての変数が暗黙にnullを取りうる」設計とは異なり、Cascadeでは**nullになりうるかどうかを型で明示する**)。

**型の絞り込み(narrowing)**: `if x is none { return ... }`のように、`none`である分岐が必ず`return`/`break`/`continue`で抜ける形になっている場合、その`if`より後のスコープでは`x`は`T?`ではなく`T`(non-null)として扱われる。`else`節の中で`x is not none`が成立している場合も同様に、その節の中では`T`として扱われる。これにより、一度`none`チェックを済ませた変数を、そのつど明示的にアンラップし直さずに`T`を要求する関数へそのまま渡せる。

```
func useIt(list: []string) { ... }   // 引数は非null許容の []string

func run(input: []string?) {
    if input is none {
        return
    }
    useIt(input)   // ここでは input は []string として扱われる(絞り込み済み)
}
```

Goのコンパイル先としては、`T?`は「値+成否フラグ」のペアとして実装される想定(AMIVM-IRの`CALL`が複数の結果オペランドを取れることを利用すると、「操作が失敗したらnoneを返す」ような組み込み関数・レシーバー関数を1回の`CALL`で表現できる)。

## 3. リテラル

```
none
true
false
1234
-1234
1.234
-1.234
"ABC"
[1, 2, 3]              // []int のリスト
[1.1, 2.2]              // []float のリスト
["A", "B", "C"]         // []string のリスト
[]                       // 空リスト(代入先の型で要素型が決まる)
Point{x: 1.0, y: 2.0}   // 構造体リテラル(4.1節)
{"a": 1, "b": 2}         // map<string, int> のmapリテラル(4.5節)
{}                        // 空map(代入先の型でキー・値の型が決まる)
```

### 3.1 文字列リテラルのエスケープシーケンス

文字列リテラル(`"..."`)の中では、バックスラッシュ`\`によるエスケープシーケンスを使える。仕様はAMIVM-IR(生成先であるGoの文字列リテラル構文)にそのまま準拠する。

| エスケープ | 意味 |
|---|---|
| `\n` `\t` `\r` `\a` `\b` `\f` `\v` | 改行・タブ・復帰・ベル・バックスペース・改ページ・垂直タブ |
| `\\` | バックスラッシュ自身 |
| `\"` | ダブルクォート自身(文字列を閉じずに`"`を書ける) |
| `\xHH` | 16進2桁で指定するバイト値(0〜255) |
| `\ooo` | 8進3桁で指定するバイト値(0〜255、`\400`以上はエラー) |
| `\uXXXX` | 16進4桁で指定するUnicodeコードポイント |
| `\UXXXXXXXX` | 16進8桁で指定するUnicodeコードポイント |

```
print("she said \"hi\" to me")
print("line1\nline2")
print("emoji: \U0001F600")
```

上記以外の`\`に続く文字(例: `\q`)はコンパイルエラーになる。エスケープしない生の非ASCII文字(`"あいう"`のような日本語等)はこれまで通りそのまま書ける。

### 3.2 整数リテラルの基数と桁区切り

整数リテラル(3節)は10進に加えて、`0x`/`0X`(16進)・`0o`/`0O`(8進)・`0b`/`0B`(2進)の基数プレフィックスで書ける。仕様はAMIVM-IR(Go 1.13以降がネイティブにサポートする整数リテラル構文)にそのまま準拠する。

| 形式 | 例 | 値 |
|---|---|---|
| 10進 | `1234` | 1234 |
| 16進 | `0x1A`, `0X1a` | 26 |
| 8進 | `0o17`, `0O17` | 15 |
| 2進 | `0b101`, `0B101` | 5 |

どの基数でも、桁区切りの`_`を使える。`_`は基数プレフィックスの直後、または数字と数字の間にのみ置ける(先頭・末尾・連続する`_`はコンパイルエラー)。

```
let million: int = 1_000_000
let flags: int = 0b1010_0101
let addr: int = 0xFF_FF
```

浮動小数点リテラル(`1.234`のような)には基数プレフィックス・桁区切りのいずれも適用されない(常に10進のまま)。先頭が`0`の10進整数リテラル(`0755`等)はこれまでどおり10進の値として解釈される(C言語系の「先頭`0`は8進」という規則は採らない——8進を書きたい場合は明示的に`0o`を使う)。

## 4. 変数・データ構造の宣言

### 4.1 構造体の定義

```
struct Point {
    x: float
    y: float
}

struct Counter {
    count: int
    label: string
}
```

構造体リテラルはフィールド名を明示して書く(順序は自由)。

```
let p: Point = Point{x: 1.0, y: 2.0}
let c = Counter{count: 0, label: "hits"}   // 型は右辺から推論
```

### 4.2 変数宣言

```
let x: int              // 宣言のみ。ゼロ値(0)で初期化される
let x: int = 100         // 宣言+初期化
let x = 100               // 型推論(int)
let name: int?            // null許容。宣言のみだとnone
const pi: float = 3.14159 // 再代入不可
```

`let`は再代入可能な変数、`const`は初期化必須・再代入不可の定数。

### 4.3 リストの宣言

```
let xs: []int = [1, 2, 3]
let ys: []int              // 空リストで初期化
let zs = [1.0, 2.0, 3.0]   // 型推論([]float)
```

Seedの配列と異なり**要素数を宣言時に固定しない**(可変長)。要素の追加は組み込み関数`append`で行う(13節)。

### 4.4 ポインタの宣言

```
let p: *Point = &pt        // ptのアドレスを取る(pt: Point が既に存在する前提)
let q: *int = none          // 何も指していないポインタ
```

### 4.5 mapの宣言

```
let counts: map<string, int> = {"a": 1, "b": 2}
let empty: map<string, int>                       // 空mapで初期化
let scores = {"alice": 90, "bob": 75}              // 型推論(map<string, int>)
```

要素の追加・更新は`m[k] = v`(5節)、存在確認と取得は添字アクセス`m[k]`(`V?`を返す)で行う。

## 5. 代入

```
x = 100                 // スカラーへの代入
p.x = 1.0                 // 構造体フィールドへの代入(pがPointでも*Pointでも同じ書き方)
xs[0] = 100               // リスト要素への代入
*ptr = 5                  // ポインタの指す先への代入(デリファレンス代入)
counts["a"] = 1            // map要素への代入(キーが無ければ新規追加、あれば上書き)
```

複数戻り値を返す関数呼び出しの結果は、カンマ区切りの左辺へまとめて受け取る(8.5節)。不要な戻り値は`_`で読み捨てられる。

```
let q, r = divmod(17, 5)
let _, ok = tryParse("123")   // 1つ目を読み捨てる
```

複合代入演算子(`+=` `-=` `*=` `/=` `%=`)と後置`++`/`--`が使える。いずれも式ではなく**独立した文としてのみ**使用できる。

```
count += 1
count++
```

## 6. 演算子

```
+   -   *   /   %        // 算術演算子(2項)
+=  -=  *=  /=  %=        // 複合代入
++  --                     // 後置インクリメント/デクリメント(文としてのみ)

==  !=                      // 等価比較
<   <=   >   >=             // 順序比較

&&                            // 論理AND
||                            // 論理OR
!                             // 論理NOT(前置単項)

&   \|   ^   &^              // ビット演算(AND/OR/XOR/AND NOT)。int専用
<<  >>                       // シフト(左/右)。int専用
~                              // ビット反転(前置単項)。int専用

-                             // 単項マイナス(前置)
&                             // アドレス取得(前置単項。&x で x へのポインタを得る)
*                             // デリファレンス(前置単項。*p で p の指す値を得る)

?                             // エラー伝播(後置単項。8.6節)

|>                            // パイプライン連結(9節)
```

`*`は乗算(2項)とデリファレンス(前置単項)の両方に使う。`&`はアドレス取得(前置単項)とビットAND(2項)の両方に使う(構文上の位置で区別できるため、`&&`とは別に単項`&`とビット`&`が共存できる)。`^`はビットXOR(2項)専用で、ビット反転の前置単項は`~`という別のトークンを使う(GoはXORと反転の両方に`^`を使うが、Cascadeでは前置/中置を別トークンにして読みやすさを優先した)。`&^`はGoの`&^`(AND NOT。`a &^ b`は`a & (~b)`と同じ)をそのまま踏襲し、AMIVM-IRの`BCLEAR`に直接対応する。

ビット演算・シフト・`~`は**`int`型のオペランドにのみ**使える(`float`/`string`/`bool`/`struct`等に対してはコンパイルエラー)。

### 演算子の優先順位(上から高い順)

| 優先度 | 演算子 | 結合方向 |
|---|---|---|
| 1(最高) | `( )`、`.`(フィールド/メソッドアクセス)、`[ ]`(添字)、関数呼び出し`( )`、後置`?`(エラー伝播) | 左から右 |
| 2 | 単項`!`、単項`-`、単項`*`(デリファレンス)、単項`&`(アドレス取得)、単項`~`(ビット反転) | 右から左 |
| 3 | `*` `/` `%` | 左から右 |
| 4 | `+` `-` | 左から右 |
| 5 | `<<` `>>` | 左から右 |
| 6 | `&` `&^` | 左から右 |
| 7 | `\|` `^`(ビットXOR) | 左から右 |
| 8 | `<` `<=` `>` `>=` | 左から右 |
| 9 | `==` `!=` | 左から右 |
| 10 | `&&` | 左から右 |
| 11 | `\|\|` | 左から右 |
| 12(最低) | `\|>`(パイプライン) | 左から右 |

```
d = *p + 1        // (*p) + 1 (単項*の方が+より優先度が高い)
r = a * b + c       // (a*b) + c
mask = a & 255 | flags   // (a & 255) | flags (& のほうが | より優先度が高い)
v = read()?               // 後置?はほぼ最優先(8.6節)。エラーがあれば即座にreturnする
pipe = src |> s1 |> s2 |> snk   // 左結合だが、全体を1つのパイプライン式として構文木に組む(9節参照)。
                                  // src と s1 を先に実行してから s2 へ渡す、という逐次評価ではない
```

### `+`演算子の型ごとの意味

`int + int`→整数の加算、`float + float`→浮動小数点の加算、`string + string`→文字列結合。異なる型同士の`+`は許可しない(暗黙の型変換はしない)。

## 7. 制御構文

### if / elif / else

```
if x == 100 {
    y = 100
} elif x == 200 {
    z = 200
} else {
    x += 1
}
```

### null許容型のチェック

```
if y is none {
    print("y is empty")
} elif y is not none {
    print("y has a value")
}
```

### while

```
while i < 10 {
    i++
}
```

### for-in(リスト・map・チャネルに使える)

```
for x in xs {           // リストの要素を先頭から順に走査
    print(string(x))
}

for k, v in counts {     // mapのキー・値の組を走査(順序は不定)
    print(k + ": " + string(v))
}

for v in someChannel {   // チャネルが閉じるまで受信し続ける(9節)
    print(string(v))
}
```

### break / continue

`while`・`for`のどちらでも使用できる。最も内側のループにのみ作用する。

### switch(タグ付き)

式の値を複数の候補と比較する。Goと同様に**フォールスルーしない**(各`case`は自動的に`break`相当で終わる)。1つの`case`に複数の値をカンマ区切りで書ける。

```
switch day {
case 1, 7:
    print("weekend")
case 2, 3, 4, 5, 6:
    print("weekday")
default:
    print("invalid")
}
```

### switch(タグなし/条件列挙)

`switch`の後に式を書かない形。各`case`は任意の真偽式を書け、上から順に評価して最初に`true`になったものが実行される(`if`/`elif`の連鎖と等価だが、意図が読み取りやすい)。

```
switch {
case score >= 90:
    print("A")
case score >= 70:
    print("B")
default:
    print("C")
}
```

`default`は省略可能(どの`case`にも一致しなければ何もしない)。`switch`の本体内でも`break`(その`switch`を抜ける)・`continue`(囲むループへ作用する。`switch`自体はループではない)が使える。

## 8. 関数(通常の関数・レシーバー付き関数・クロージャー)

### 8.1 通常の関数

```
func add(a: int, b: int): int {
    return a + b
}

func log(message: string) {   // 戻り値の型を省略すると戻り値なし
    print(message)
}
```

### 8.2 レシーバー付き関数

構造体名の前に`(レシーバー名: 型)`を書く。レシーバーの型は値型(`Point`)でもポインタ型(`*Point`)でもよい。フィールドアクセスの書き方(`p.x`)はどちらでも同じ。

```
struct Point {
    x: float
    y: float
}

// 値レシーバー: 呼び出し元のPointは変更されない(読み取り専用の操作向け)
func (p: Point) magnitude(): float {
    return sqrt(p.x * p.x + p.y * p.y)
}

// ポインタレシーバー: 呼び出し元の構造体を書き換える
func (p: *Point) scale(factor: float) {
    p.x = p.x * factor
    p.y = p.y * factor
}
```

呼び出し方は`obj.method(引数...)`。

```
let pt: Point = Point{x: 3.0, y: 4.0}
print(string(pt.magnitude()))   // "5"

pt.scale(2.0)                    // ポインタレシーバーなので pt 自体が書き換わる
print(string(pt.x))              // "6"
```

値レシーバーで宣言したメソッドを、ポインタ変数から呼んでもよい(自動でデリファレンスされる)。逆にポインタレシーバーのメソッドを値変数から呼んだ場合は、その変数が呼び出し可能な場所に存在する(アドレスを取れる)限り自動でアドレスが取られる。

### 8.3 クロージャー

`func(引数...): 戻り値型 { ... }`という形の式は、その場で関数値(クロージャー)を作る。周囲のスコープの変数を捕捉できる。

```
func makeAdder(base: int): func(int): int {
    return func(n: int): int {
        return base + n
    }
}

let add5 = makeAdder(5)
print(string(add5(10)))   // "15"
```

関数値は変数に代入したり、他の関数へ引数として渡したりできる(8.4節)。

### 8.4 高階関数(組み込みのコレクション操作)

```
let numbers: []int = [1, 2, 3, 4, 5, 6]

let evens: []int = filter(numbers, func(n: int): bool {
    return n % 2 == 0
})
// evens = [2, 4, 6]

let doubled: []int = map(numbers, func(n: int): int {
    return n * 2
})
// doubled = [2, 4, 6, 8, 10, 12]

let total: int = reduce(numbers, 0, func(acc: int, n: int): int {
    return acc + n
})
// total = 21
```

`filter`/`map`/`reduce`/`append`は組み込み関数(13節)。ユーザー定義関数も同じ形でクロージャーを引数に取れる。

### 8.5 複数戻り値

戻り値の型を`(T1, T2, ...)`と書くと、その関数は複数の値を返す。`return`もカンマ区切りで複数の式を書く。呼び出し側は5節の通りカンマ区切りの左辺で受け取る(不要な値は`_`で読み捨てる)。

```
func divmod(a: int, b: int): (int, int) {
    return a / b, a % b
}

let q, r = divmod(17, 5)   // q = 3, r = 2
```

AMIVM-IRの`RET`と`CALL`はどちらも複数オペランドをそのまま扱えるため、Cascadeの複数戻り値はコード生成上の特別な変換を必要としない(タプル型を新設せず、戻り値の個数がそのままIRの`RET`/`CALL`の結果オペランド数になる)。

### 8.6 エラー処理(`error`型・`(T, error?)`規約・後置`?`)

Cascadeには例外機構が無い。失敗しうる関数は、**最後の戻り値として`error?`を返す**Go風の規約でエラーを表現する。

```
func parseAge(s: string): (int, error?) {
    let n: int? = int(s)
    if n is none {
        return 0, error("invalid age: " + s)
    }
    return n, none   // 成功時は error? 側を none にする
}
```

呼び出し側は複数戻り値として受け取り、`is none` / `is not none`(2.3節・7節)でエラーの有無を判定する。

```
let age, err = parseAge("abc")
if err is not none {
    print("failed: " + err.message)
    return 1
}
print(string(age))
```

**後置`?`演算子**(Rust風の伝播記法)を使うと、この判定+早期returnを1トークンに圧縮できる。`expr?`は次のように展開される。

- `expr`(戻り値の型が`(T, error?)`である呼び出し式)を評価する
- エラー側が`none`でなければ、`?`を書いた**その関数自身から**`ゼロ値..., エラー`を即座に`return`する(呼び出しの伝播元とエラーの伝播先で、関数の戻り値の型を合わせておく必要がある)
- エラー側が`none`なら、`expr`全体は成功値`T`(エラーでない側)に評価される

```
func loadAge(s: string): (int, error?) {
    let age = parseAge(s)?   // parseAgeがエラーを返せば、ここで (0, err) をそのままreturnする
    return age, none
}
```

`?`は**戻り値が`(T, error?)`(末尾が`error?`)の形をした呼び出し式にのみ**使え、かつ`?`を書ける関数自身の戻り値も末尾が`error?`である必要がある(コンパイル時に検査する)。`error`はメッセージ1つを持つ組み込み型で、`error(message: string): error`(13節)で作る。

## 9. パイプライン構文

### 9.1 3種類のステージ

パイプラインは「入力チャネルを持たない`source`」「入力と出力の両方を持つ`stage`」「出力チャネルを持たない`sink`」の3種類の宣言から組み立てる。それぞれ独立したgoroutine相当として動き、チャネルでデータを受け渡す。

```
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

stage toString(input: chan<int>, output: chan<string>) {
    for n in input {
        send(output, string(n))
    }
}

sink printAll(input: chan<string>) {
    for s in input {
        print(s)
    }
}
```

`input`/`output`はチャネルを受け取るパラメータの慣習的な名前(予約語ではないので自由に変えてよいが、`in`という名前だけは避けること — `for`文のキーワード`in`と衝突する)で、対応する要素型は宣言ごとに変わってよい(このパイプライン例では `int → int → string`)。`send(output, value)`で出力チャネルへ値を送る。

### 9.2 連結(`|>`演算子)

```
numbers |> double |> toString |> printAll
```

- **先頭は必ず`source`**、**末尾は`sink`または`collect`**(9.3節)、間に0個以上の`stage`を挟む、という並びをコンパイル時に検査する
- 隣り合うステージの出力型・入力型が一致していなければコンパイルエラーになる(この例では `numbers`の出力`int`と`double`の入力`int`が一致、`double`の出力`int`と`toString`の入力`int`が一致、`toString`の出力`string`と`printAll`の入力`string`が一致)
- 各ステージは連結された瞬間に並行実行が始まる(全ステージが同時に動きながら、データがチャネルを通じて順に流れていく)

### 9.3 パイプラインの終端 — 結果を受け取る

末尾が`sink`のパイプラインは戻り値を持たない文として書く(9.1/9.2の例)。最終結果を**値として**受け取りたい場合は、`sink`の代わりに組み込みキーワード`collect`で終える。

```
let results: []string? = numbers |> double |> toString |> collect
```

`collect`で終わるパイプライン式は`[]T?`(要素型のリストのnull許容版)を返す。全ステージが正常に完了すれば集められた値のリスト、9.4節のように途中で中断した場合は`none`になる。

### 9.4 エラー時の終了

いずれかのステージ内で組み込み関数`abort(message: string)`を呼ぶと、そのパイプライン全体が即座に終了する(まだ処理中の他ステージも巻き込んで停止する — 名前の"cascade"はこの「1箇所の異常が連鎖的に全体を止める」動きにも由来する)。

```
stage validate(input: chan<int>, output: chan<int>) {
    for n in input {
        if n < 0 {
            abort("negative value: " + string(n))
        }
        send(output, n)
    }
}
```

- `collect`で受けているパイプライン式は、`abort`されると`none`を返す
- `sink`で終わる(値を受け取らない)パイプライン文は、`abort`されると単にその時点で全ステージが停止する(失敗を検知したい場合は`collect`を使うこと)

### 9.5 ファンイン(複数チャネルの合流)

2本のチャネルを1本に合流させたい場合は組み込み関数`merge`を使う(内部的にはどちらか先に値が来た方を受け取る、select相当の仕組みで実装される想定)。

```
let combined: chan<int> = merge(channelA, channelB)
```

## 10. スコープ規則

- スコープは**ブロック単位**。`{ }`(`if`/`while`/`for`/`switch`の各`case`/`func`/`source`/`stage`/`sink`の本体)ごとに新しいスコープが作られる
- 内側のブロックで外側と同名の変数を宣言すること(シャドーイング)は許可する
- 同一スコープ内での同名変数の再宣言はエラーとする
- `for x in xs`のループ変数`x`のスコープは`for`文全体
- クロージャーは定義された時点で周囲のスコープの変数を捕捉する。捕捉した変数への書き込みは、クロージャーの外の変数にも反映される(参照捕捉)
- レシーバー・関数・クロージャー・`source`/`stage`/`sink`のパラメータのスコープはその本体全体

## 11. モジュールと複数ファイルの統合

### 11.1 パッケージ = ディレクトリ

**同じディレクトリに置かれた`.cas`ファイル群は、1つの「パッケージ」として1つのプログラムへ統合される。** パッケージ内のファイル間では`import`は不要で、すべてのファイルが1つの共有スコープ(10節のトップレベルスコープ)にあるかのようにコンパイルされる。ファイルをどう分割するかは純粋に開発者の整理都合であり、言語仕様上の意味は持たない。

```
myproject/
    main.cas      // func main() を含む
    reading.cas    // struct Reading と関連するレシーバー関数
    pipeline.cas    // source/stage/sink 群
```

上記3ファイルは`myproject`という1つのパッケージとして一括コンパイルされ、`main.cas`から`reading.cas`で定義した`struct Reading`をそのまま(`import`なしで)参照できる。

パッケージ名はディレクトリ名(`myproject`)がそのまま使われる。

### 11.2 パッケージをまたぐ参照 — `import`

**別のディレクトリにあるパッケージ**を参照したい場合は、ファイル先頭で`import`する。

```
import mathutil "./mathutil"
```

`"./mathutil"`は現在のパッケージのディレクトリからの相対パス、`mathutil`はそのパッケージを参照するときの修飾子(デフォルトはディレクトリ名。上記のように明示すれば別名も付けられる)。参照は`修飾子.識別子`の形で書く。

```
import mathutil "./mathutil"

func main(): int {
    let r = mathutil.Clamp(150, 0, 100)
    print(string(r))
    return 0
}
```

同一ファイル内で複数の`import`を並べてよい。`import`はファイル先頭(構造体・関数などの宣言より前)にまとめて書く。

### 11.3 公開/非公開 — `pub`

他パッケージから`import`経由で参照できるのは、宣言の前に`pub`を付けたものだけ(`pub`が無い宣言はそのパッケージ内だけで使える)。対象は`struct`・`func`(レシーバー付き関数含む)・`const`・`source`/`stage`/`sink`・トップレベルの`let`。

```
// mathutil/mathutil.cas
pub func Clamp(value: int, min: int, max: int): int {
    if value < min {
        return min
    }
    if value > max {
        return max
    }
    return value
}

func helper(): int {   // pub が無いので mathutil パッケージの外からは見えない
    return 0
}

pub struct Vector {
    x: float
    y: float
}

pub func (v: Vector) length(): float {
    return sqrt(v.x * v.x + v.y * v.y)
}
```

`import`したパッケージの`pub`構造体のレシーバー関数(`pub func (v: Vector) length()`)は、値を`修飾子.型名{...}`で作れば`obj.length()`とそのまま呼べる(修飾子はレシーバー呼び出しの構文には現れない。型と結びついているのはそのパッケージ内で定義されたレシーバーだけであり、他パッケージが同じ型に新たなレシーバー関数を追加すること — いわゆる後付け拡張 — はできない)。

```
import mathutil "./mathutil"

func main(): int {
    let v: mathutil.Vector = mathutil.Vector{x: 3.0, y: 4.0}
    print(string(v.length()))   // "5"
    return 0
}
```

### 11.4 エントリーポイントとパッケージ

`func main(): int`(12節)は、コンパイル時にルートとして指定したパッケージ(通常はプロジェクトの最上位ディレクトリ)にのみ書ける。`import`されている側のパッケージに`main`という名前の`func`を置くことはコンパイルエラーとする(ルートパッケージと紛らわしいため)。

### 11.5 循環importの禁止

パッケージAがパッケージBを`import`し、BもAを直接・間接に`import`している場合はコンパイルエラーとする(依存関係は有向非巡回グラフでなければならない)。

### 11.6 AMIVM-IRへの落とし込み

AMIVM-IRの識別子(`!`関数名・`^`型名・`@`グローバル変数名)は1プログラム全体で1つのフラットな名前空間を共有し、パッケージという概念を持たない。そのためCascadeコンパイラは、IRを生成する段階で**パッケージ名を各識別子の前に連結して一意化する**(例: `mathutil`パッケージの`Clamp`関数は`!mathutil_Clamp`、`Vector`型は`^mathutil_Vector`として出力する)。ルートパッケージの識別子には接頭辞を付けない(`main`は他言語実装同様`!main`のまま)。これはSeedの`main`/`seed_main`分離(ユーザーの`main`を内部的に別名へ退避してからGoの`func main()`と衝突しないラッパーを生成する設計)と同じ「フロントエンドがamivmへ渡す前に名前の一意性を保証する」という考え方の延長にあたる。パッケージ内の複数ファイルは統合時点(11.1節)で単一の名前空間になっているため、この接頭辞付けはパッケージ単位でのみ行えばよく、ファイル単位では発生しない。

## 12. エントリーポイント

```
func main(): int {
    print("Hello, Cascade!")
    return 0
}
```

- 引数を取らない(コマンドライン引数が必要な場合は組み込み関数`args(): []string`を呼ぶ)
- 戻り値は終了コードとして扱う。`0`は正常終了、`0`以外は異常終了
- 書ける場所については11.4節を参照

## 13. 組み込み関数

| 関数 | シグネチャ | 説明 |
|---|---|---|
| `print` | `(value: string) -> なし` | 文字列を標準出力へ書き、末尾に改行を付ける |
| `len` | `(value: string \| []T \| map<K, V>) -> int` | 文字列の文字数、リストの要素数、またはmapの要素数 |
| `range` | `(from: int, to: int) -> []int` | `from`以上`to`未満の整数のリストを作る |
| `append` | `(list: []T, value: T) -> []T` | `list`の末尾に`value`を加えた**新しい**リストを返す(元のリストは変更しない) |
| `filter` | `(list: []T, pred: func(T): bool) -> []T` | `pred`が`true`を返す要素だけのリストを返す |
| `map` | `(list: []T, f: func(T): U) -> []U` | 各要素に`f`を適用したリストを返す |
| `reduce` | `(list: []T, initial: U, f: func(U, T): U) -> U` | `initial`から始めて`f`を畳み込んだ結果を返す |
| `int` | `(value: int \| float \| bool) -> int`、`(value: string) -> int?` | `int`へ変換する。`string`からの変換は失敗しうるため戻り値が`int?`になる(数値変換の他は失敗しない) |
| `float` | `(value: int \| float) -> float`、`(value: string) -> float?` | `float`へ変換する。`string`からの変換は失敗しうるため戻り値が`float?`になる |
| `string` | `(value: int \| float \| bool) -> string` | `string`へ変換する |
| `sqrt` | `(value: float) -> float` | 平方根 |
| `send` | `(output: chan<T>, value: T) -> なし` | `source`/`stage`本体内で出力チャネルへ値を送る |
| `merge` | `(a: chan<T>, b: chan<T>) -> chan<T>` | 2本のチャネルを1本に合流させる(9.5節) |
| `abort` | `(message: string) -> なし` | 現在のパイプラインを異常終了させる(9.4節) |
| `args` | `() -> []string` | コマンドライン引数を返す |
| `error` | `(message: string) -> error` | エラー値を作る(8.6節) |
| `delete` | `(m: map<K, V>, key: K) -> なし` | mapから指定キーの要素を削除する |

## 14. キーワード一覧

```
let const struct func source stage sink
if elif else while for in break continue return
switch case default
true false none is not
send chan collect map
error
int float string bool
import pub
```

## 15. まとめのサンプルプログラム

3本柱(レシーバー・クロージャー・パイプライン)を1つのプログラムに詰め込んだ例。

```
struct Reading {
    sensorId: int
    value: float
}

func (r: Reading) isValid(): bool {
    return r.value >= 0.0
}

source readings(output: chan<Reading>) {
    let raw: []Reading = [
        Reading{sensorId: 1, value: 12.5},
        Reading{sensorId: 2, value: -3.0},
        Reading{sensorId: 3, value: 8.0},
    ]
    for r in raw {
        send(output, r)
    }
}

stage validOnly(input: chan<Reading>, output: chan<Reading>) {
    for r in input {
        if r.isValid() {
            send(output, r)
        }
    }
}

stage format(input: chan<Reading>, output: chan<string>) {
    for r in input {
        send(output, "sensor " + string(r.sensorId) + ": " + string(r.value))
    }
}

func main(): int {
    let lines: []string? = readings |> validOnly |> format |> collect
    if lines is none {
        print("pipeline failed")
        return 1
    }

    let formatted: []string = map(lines, func(s: string): string {
        return "[OK] " + s
    })
    for line in formatted {
        print(line)
    }
    return 0
}
```
