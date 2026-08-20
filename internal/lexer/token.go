package lexer

// Kind identifies the lexical category of a Token.
type Kind int

const (
	EOF Kind = iota
	Newline
	Ident
	Int
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
	Colon
	Comma
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
