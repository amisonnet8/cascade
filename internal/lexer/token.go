package lexer

// Kind identifies the lexical category of a Token.
type Kind int

const (
	EOF Kind = iota
	Newline
	Ident
	Int
	Float
	String

	// structural keywords (cascade_spec.md §14). Not every keyword has a
	// parser consumer yet — later development steps extend the grammar
	// one feature at a time (see internal/parser's package doc) — but the
	// full reserved-word set is lexed from the start so a later step never
	// has to revisit tokenization to add a keyword.
	KwLet
	KwConst
	KwStruct
	KwFunc
	KwSource
	KwStage
	KwSink
	KwIf
	KwElif
	KwElse
	KwWhile
	KwFor
	KwIn
	KwBreak
	KwContinue
	KwReturn
	KwSwitch
	KwCase
	KwDefault
	KwTrue
	KwFalse
	KwNone
	KwIs
	KwNot
	KwSend
	KwChan
	KwCollect
	KwMap
	KwError
	KwInt
	KwFloat
	KwString
	KwBool
	KwImport
	KwPub

	// punctuation
	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Colon
	Comma
	Question
	Dot

	// operators (cascade_spec.md §6, §5 for the compound-assignment/
	// inc-dec set). "=" (Step 2), the arithmetic/comparison/logical set
	// (Step 3), the bitwise/shift set (Step 4), and the compound-
	// assignment/inc-dec set below (Step 5). Unary "*"/"&" (pointers,
	// Step 8), postfix "?" (Step 11), and "|>" (Step 12) are lexed
	// starting in the steps that introduce them (see lexer.go's package
	// doc).
	Assign
	Plus
	Minus
	Star
	Slash
	Percent
	Eq
	Neq
	Lt
	Lte
	Gt
	Gte
	AndAnd
	OrOr
	Not
	Amp    // "&" (bitwise AND — binary; unary "&" (address-of) lands in Step 8)
	Pipe   // "|" (bitwise OR)
	Caret  // "^" (bitwise XOR — binary only; unary bit-flip is Tilde, not this)
	AndNot // "&^" (bitwise AND NOT)
	Tilde  // "~" (unary bitwise NOT)
	Shl    // "<<"
	Shr    // ">>"
	PlusAssign
	MinusAssign
	StarAssign
	SlashAssign
	PercentAssign
	Inc // "++"
	Dec // "--"
)

// keywords holds Cascade's full reserved-word set (cascade_spec.md §14).
var keywords = map[string]Kind{
	"let":      KwLet,
	"const":    KwConst,
	"struct":   KwStruct,
	"func":     KwFunc,
	"source":   KwSource,
	"stage":    KwStage,
	"sink":     KwSink,
	"if":       KwIf,
	"elif":     KwElif,
	"else":     KwElse,
	"while":    KwWhile,
	"for":      KwFor,
	"in":       KwIn,
	"break":    KwBreak,
	"continue": KwContinue,
	"return":   KwReturn,
	"switch":   KwSwitch,
	"case":     KwCase,
	"default":  KwDefault,
	"true":     KwTrue,
	"false":    KwFalse,
	"none":     KwNone,
	"is":       KwIs,
	"not":      KwNot,
	"send":     KwSend,
	"chan":     KwChan,
	"collect":  KwCollect,
	"map":      KwMap,
	"error":    KwError,
	"int":      KwInt,
	"float":    KwFloat,
	"string":   KwString,
	"bool":     KwBool,
	"import":   KwImport,
	"pub":      KwPub,
}

// Token is a single lexical token together with its source line (1-based).
type Token struct {
	Kind    Kind
	Literal string
	Line    int
}
