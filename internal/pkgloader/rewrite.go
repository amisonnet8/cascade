package pkgloader

import (
	"fmt"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// rewriter walks one package's *ast.File, resolving two kinds of name
// references in place — self-renames (this package's own top-level
// declarations, referenced elsewhere in this same package's own files —
// renames is nil for the root package, which never self-renames) and
// qualifier resolution (a `qualifier.Name` reference, resolved against
// quals — an already-loaded, already-self-renamed imported package) —
// wherever either can appear: a type, a struct literal's type name, a
// plain function call, or (for a qualified const/value reference only) a
// field-access expression rewritten into a plain identifier. A method
// call's own method name (`obj.method(...)`) is never touched by
// either — cascade_spec.md §11.3's own note that "修飾子はレシーバー呼
// び出しの構文には現れない" confirms a qualifier is never meaningful in
// that position, and method names aren't independently prefixed anyway
// (see collectRenames's doc) since they're already namespaced per
// struct by codegen's existing StructName_Method convention.
type rewriter struct {
	renames map[string]string
	quals   map[string]*loadedPackage
}

// resolveDottedName resolves name, which the PARSER already encoded as
// "qualifier.Rest" whenever it saw a genuine `pkg.Type` reference in TYPE
// or STRUCT-LITERAL position (see parser's parseTypeBase/
// parseSelectorSuffix) — an embedded '.' here is therefore never
// ambiguous with anything else (a type name or a struct literal's own
// name never legitimately contains a '.' otherwise), unlike a
// FieldExpr/CallExpr receiver, which needs qualifierTarget instead (see
// its own doc for why). A name with no '.' is checked against this
// package's own self-rename table instead (unchanged if it's not one of
// this package's own renamed declarations).
func (r *rewriter) resolveDottedName(name string) (string, error) {
	idx := strings.IndexByte(name, '.')
	if idx < 0 {
		if newName, ok := r.renames[name]; ok {
			return newName, nil
		}
		return name, nil
	}
	qualifier, rest := name[:idx], name[idx+1:]
	target, ok := r.quals[qualifier]
	if !ok {
		return "", fmt.Errorf("undeclared import qualifier %q", qualifier)
	}
	return r.resolveMemberName(target, rest)
}

// qualifierTarget reports whether name is a declared import qualifier in
// the file currently being rewritten. Used to disambiguate a
// FieldExpr/CallExpr's own receiver expression, which — unlike a type
// name or a struct literal's own name — is never unambiguously "a
// qualifier" from syntax alone: an ordinary field read or method call on
// an ordinary local variable (`p.x`, `err.message`) looks identical at
// the AST level to a genuinely qualified reference (`mathutil.Clamp`)
// until compared against the actual set of import qualifiers this file
// declared. A real bug caught only by running the full example suite
// through pkgloader for the first time: naively reusing
// resolveDottedName here (by string-concatenating the receiver's Name
// and the field/callee into one "x.y" string) treated *every* ordinary
// field access as an unresolved qualifier reference the moment any
// import existed anywhere in the program, since resolveDottedName has no
// way to tell "this dot is synthetic, built from two already-distinct
// AST fields" apart from "this dot was genuinely present in a Type.Name
// string" — see CLAUDE.md's "確定した設計判断" for the full account.
func (r *rewriter) qualifierTarget(name string) (*loadedPackage, bool) {
	target, ok := r.quals[name]
	return target, ok
}

// resolveMemberName resolves rest (a bare struct/func/const name, with
// no qualifier prefix) against target, an already-loaded package,
// validating that target actually marked it `pub` (cascade_spec.md
// §11.3) — shared by resolveDottedName and every qualifierTarget-based
// call site.
func (r *rewriter) resolveMemberName(target *loadedPackage, rest string) (string, error) {
	prefixed := target.Name + "_" + rest
	if !target.PubNames[prefixed] {
		return "", fmt.Errorf("%s.%s is not declared 'pub' in package %q and cannot be used outside it (cascade_spec.md §11.3)", target.Name, rest, target.Name)
	}
	return prefixed, nil
}

// rewriteFile walks every declaration in file, resolving names in every
// type, body statement, and (for a top-level let) initializer.
func (r *rewriter) rewriteFile(file *ast.File) error {
	for _, sd := range file.Structs {
		for i := range sd.Fields {
			t, err := r.rewriteType(sd.Fields[i].Type, sd.Line)
			if err != nil {
				return err
			}
			sd.Fields[i].Type = t
		}
	}
	for _, fn := range file.Funcs {
		if fn.Receiver != nil {
			t, err := r.rewriteType(fn.Receiver.Type, fn.Line)
			if err != nil {
				return err
			}
			fn.Receiver.Type = t
		}
		for i := range fn.Params {
			t, err := r.rewriteType(fn.Params[i].Type, fn.Line)
			if err != nil {
				return err
			}
			fn.Params[i].Type = t
		}
		for i := range fn.Results {
			t, err := r.rewriteType(fn.Results[i], fn.Line)
			if err != nil {
				return err
			}
			fn.Results[i] = t
		}
		if err := r.rewriteStmts(fn.Body); err != nil {
			return err
		}
	}
	for _, sd := range file.Stages {
		for i := range sd.Params {
			t, err := r.rewriteType(sd.Params[i].Type, sd.Line)
			if err != nil {
				return err
			}
			sd.Params[i].Type = t
		}
		if err := r.rewriteStmts(sd.Body); err != nil {
			return err
		}
	}
	for _, ld := range file.Lets {
		t, err := r.rewriteType(ld.Type, ld.Line)
		if err != nil {
			return err
		}
		ld.Type = t
		if ld.Init != nil {
			e, err := r.rewriteExpr(ld.Init)
			if err != nil {
				return err
			}
			ld.Init = e
		}
	}
	return nil
}

// rewriteType recursively resolves t's own leaf name (a scalar/struct
// name), or recurses into Elem/Ptr/Func/Map/Chan. line is only used for
// error messages — ast.Type itself carries no line of its own.
func (r *rewriter) rewriteType(t ast.Type, line int) (ast.Type, error) {
	if t.Elem != nil {
		e, err := r.rewriteType(*t.Elem, line)
		if err != nil {
			return ast.Type{}, err
		}
		t.Elem = &e
		return t, nil
	}
	if t.Ptr != nil {
		e, err := r.rewriteType(*t.Ptr, line)
		if err != nil {
			return ast.Type{}, err
		}
		t.Ptr = &e
		return t, nil
	}
	if t.Func != nil {
		ft := *t.Func
		for i := range ft.Params {
			p, err := r.rewriteType(ft.Params[i], line)
			if err != nil {
				return ast.Type{}, err
			}
			ft.Params[i] = p
		}
		for i := range ft.Results {
			res, err := r.rewriteType(ft.Results[i], line)
			if err != nil {
				return ast.Type{}, err
			}
			ft.Results[i] = res
		}
		t.Func = &ft
		return t, nil
	}
	if t.Map != nil {
		mt := *t.Map
		k, err := r.rewriteType(mt.Key, line)
		if err != nil {
			return ast.Type{}, err
		}
		mt.Key = k
		v, err := r.rewriteType(mt.Value, line)
		if err != nil {
			return ast.Type{}, err
		}
		mt.Value = v
		t.Map = &mt
		return t, nil
	}
	if t.Chan != nil {
		e, err := r.rewriteType(*t.Chan, line)
		if err != nil {
			return ast.Type{}, err
		}
		t.Chan = &e
		return t, nil
	}
	resolved, err := r.resolveDottedName(t.Name)
	if err != nil {
		return ast.Type{}, fmt.Errorf("line %d: %v", line, err)
	}
	t.Name = resolved
	return t, nil
}

func (r *rewriter) rewriteStmts(stmts []ast.Stmt) error {
	for i := range stmts {
		if err := r.rewriteStmt(stmts[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *rewriter) rewriteStmt(s ast.Stmt) error {
	switch v := s.(type) {
	case *ast.ExprStmt:
		e, err := r.rewriteExpr(v.X)
		if err != nil {
			return err
		}
		v.X = e
		return nil
	case *ast.ReturnStmt:
		for i := range v.Results {
			e, err := r.rewriteExpr(v.Results[i])
			if err != nil {
				return err
			}
			v.Results[i] = e
		}
		return nil
	case *ast.LetDecl:
		t, err := r.rewriteType(v.Type, v.Line)
		if err != nil {
			return err
		}
		v.Type = t
		if v.Init != nil {
			e, err := r.rewriteExpr(v.Init)
			if err != nil {
				return err
			}
			v.Init = e
		}
		return nil
	case *ast.MultiLetDecl:
		e, err := r.rewriteExpr(v.Init)
		if err != nil {
			return err
		}
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return fmt.Errorf("pkgloader: internal error: MultiLetDecl.Init rewrote to a non-call (%T)", e)
		}
		v.Init = call
		return nil
	case *ast.AssignStmt:
		if v.Index != nil {
			idx, err := r.rewriteExpr(v.Index)
			if err != nil {
				return err
			}
			v.Index = idx
		}
		val, err := r.rewriteExpr(v.Value)
		if err != nil {
			return err
		}
		v.Value = val
		return nil
	case *ast.DerefAssignStmt:
		p, err := r.rewriteExpr(v.Ptr)
		if err != nil {
			return err
		}
		v.Ptr = p
		val, err := r.rewriteExpr(v.Value)
		if err != nil {
			return err
		}
		v.Value = val
		return nil
	case *ast.CompoundAssignStmt:
		val, err := r.rewriteExpr(v.Value)
		if err != nil {
			return err
		}
		v.Value = val
		return nil
	case *ast.IncDecStmt:
		return nil
	case *ast.IfStmt:
		for i := range v.Clauses {
			c, err := r.rewriteExpr(v.Clauses[i].Cond)
			if err != nil {
				return err
			}
			v.Clauses[i].Cond = c
			if err := r.rewriteStmts(v.Clauses[i].Body); err != nil {
				return err
			}
		}
		if v.Else != nil {
			if err := r.rewriteStmts(v.Else); err != nil {
				return err
			}
		}
		return nil
	case *ast.WhileStmt:
		c, err := r.rewriteExpr(v.Cond)
		if err != nil {
			return err
		}
		v.Cond = c
		return r.rewriteStmts(v.Body)
	case *ast.BreakStmt, *ast.ContinueStmt:
		return nil
	case *ast.SwitchStmt:
		if v.Tag != nil {
			t, err := r.rewriteExpr(v.Tag)
			if err != nil {
				return err
			}
			v.Tag = t
		}
		for i := range v.Cases {
			for j := range v.Cases[i].Values {
				val, err := r.rewriteExpr(v.Cases[i].Values[j])
				if err != nil {
					return err
				}
				v.Cases[i].Values[j] = val
			}
			if err := r.rewriteStmts(v.Cases[i].Body); err != nil {
				return err
			}
		}
		if v.Default != nil {
			if err := r.rewriteStmts(v.Default); err != nil {
				return err
			}
		}
		return nil
	case *ast.ForInStmt:
		l, err := r.rewriteExpr(v.List)
		if err != nil {
			return err
		}
		v.List = l
		return r.rewriteStmts(v.Body)
	default:
		return fmt.Errorf("pkgloader: unsupported statement %T", s)
	}
}

func (r *rewriter) rewriteExpr(e ast.Expr) (ast.Expr, error) {
	switch v := e.(type) {
	case *ast.Ident:
		// A bare identifier never carries an embedded '.' from parsing
		// (a genuine qualified reference always arrives as a FieldExpr
		// or a CallExpr's own Receiver instead — see qualifierTarget's
		// doc) — only a same-package self-rename can apply here.
		if newName, ok := r.renames[v.Name]; ok {
			return &ast.Ident{Name: newName, Line: v.Line}, nil
		}
		return v, nil
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NoneLit:
		return v, nil
	case *ast.ListLit:
		for i := range v.Elems {
			e2, err := r.rewriteExpr(v.Elems[i])
			if err != nil {
				return nil, err
			}
			v.Elems[i] = e2
		}
		return v, nil
	case *ast.MapLit:
		for i := range v.Pairs {
			k, err := r.rewriteExpr(v.Pairs[i].Key)
			if err != nil {
				return nil, err
			}
			v.Pairs[i].Key = k
			val, err := r.rewriteExpr(v.Pairs[i].Value)
			if err != nil {
				return nil, err
			}
			v.Pairs[i].Value = val
		}
		return v, nil
	case *ast.IndexExpr:
		x, err := r.rewriteExpr(v.X)
		if err != nil {
			return nil, err
		}
		v.X = x
		idx, err := r.rewriteExpr(v.Index)
		if err != nil {
			return nil, err
		}
		v.Index = idx
		return v, nil
	case *ast.CallExpr:
		return r.rewriteCallExpr(v)
	case *ast.StructLit:
		resolved, err := r.resolveDottedName(v.TypeName)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", v.Line, err)
		}
		v.TypeName = resolved
		for i := range v.Fields {
			val, err := r.rewriteExpr(v.Fields[i].Value)
			if err != nil {
				return nil, err
			}
			v.Fields[i].Value = val
		}
		return v, nil
	case *ast.FieldExpr:
		// A qualified const/value reference (`pkg.SomeConst`) — only
		// meaningful when v.X is a bare identifier that's actually a
		// declared import qualifier in this file (checked via
		// qualifierTarget, never by treating "X.Field" as a single
		// dotted name — see its doc for why); otherwise this is an
		// ordinary struct-field read, left alone beyond recursing into
		// X.
		if id, isIdent := v.X.(*ast.Ident); isIdent {
			if target, ok := r.qualifierTarget(id.Name); ok {
				resolved, err := r.resolveMemberName(target, v.Field)
				if err != nil {
					return nil, fmt.Errorf("line %d: %v", v.Line, err)
				}
				return &ast.Ident{Name: resolved, Line: v.Line}, nil
			}
		}
		x, err := r.rewriteExpr(v.X)
		if err != nil {
			return nil, err
		}
		v.X = x
		return v, nil
	case *ast.UnaryExpr:
		x, err := r.rewriteExpr(v.X)
		if err != nil {
			return nil, err
		}
		v.X = x
		return v, nil
	case *ast.BinaryExpr:
		x, err := r.rewriteExpr(v.X)
		if err != nil {
			return nil, err
		}
		v.X = x
		y, err := r.rewriteExpr(v.Y)
		if err != nil {
			return nil, err
		}
		v.Y = y
		return v, nil
	case *ast.NullCheckExpr:
		x, err := r.rewriteExpr(v.X)
		if err != nil {
			return nil, err
		}
		v.X = x
		return v, nil
	case *ast.ClosureLit:
		for i := range v.Params {
			t, err := r.rewriteType(v.Params[i].Type, v.Line)
			if err != nil {
				return nil, err
			}
			v.Params[i].Type = t
		}
		for i := range v.Results {
			t, err := r.rewriteType(v.Results[i], v.Line)
			if err != nil {
				return nil, err
			}
			v.Results[i] = t
		}
		if err := r.rewriteStmts(v.Body); err != nil {
			return nil, err
		}
		return v, nil
	case *ast.ErrorPropExpr:
		c, err := r.rewriteExpr(v.Call)
		if err != nil {
			return nil, err
		}
		call, ok := c.(*ast.CallExpr)
		if !ok {
			return nil, fmt.Errorf("pkgloader: internal error: ErrorPropExpr.Call rewrote to a non-call (%T)", c)
		}
		v.Call = call
		return v, nil
	case *ast.PipelineExpr:
		// Cross-package pipeline stage references are deliberately not
		// supported (cascade_spec.md §9 never shows a `|>` chain naming
		// an imported source/stage/sink — see CLAUDE.md's "確定した設計
		// 判断" for this scope decision) — but a pipeline defined WITHIN
		// a non-root package still needs its own stage names
		// self-renamed to match that package's now-prefixed
		// source/stage/sink declarations.
		for i := range v.Stages {
			if newName, ok := r.renames[v.Stages[i].Name]; ok {
				v.Stages[i].Name = newName
			}
		}
		return v, nil
	default:
		return nil, fmt.Errorf("pkgloader: unsupported expression %T", e)
	}
}

// rewriteCallExpr handles both a qualified function call
// (`pkg.Func(...)`, syntactically identical to a method call until
// resolved — see the package doc) and an ordinary call (method, closure
// variable, or plain function).
func (r *rewriter) rewriteCallExpr(call *ast.CallExpr) (ast.Expr, error) {
	if call.Receiver != nil {
		if id, isIdent := call.Receiver.(*ast.Ident); isIdent {
			if target, ok := r.qualifierTarget(id.Name); ok {
				resolved, err := r.resolveMemberName(target, call.Callee)
				if err != nil {
					return nil, fmt.Errorf("line %d: %v", call.Line, err)
				}
				call.Receiver = nil
				call.Callee = resolved
				for i := range call.Args {
					a, err := r.rewriteExpr(call.Args[i])
					if err != nil {
						return nil, err
					}
					call.Args[i] = a
				}
				return call, nil
			}
		}
		r2, err := r.rewriteExpr(call.Receiver)
		if err != nil {
			return nil, err
		}
		call.Receiver = r2
		for i := range call.Args {
			a, err := r.rewriteExpr(call.Args[i])
			if err != nil {
				return nil, err
			}
			call.Args[i] = a
		}
		return call, nil
	}
	if newName, ok := r.renames[call.Callee]; ok {
		call.Callee = newName
	}
	for i := range call.Args {
		a, err := r.rewriteExpr(call.Args[i])
		if err != nil {
			return nil, err
		}
		call.Args[i] = a
	}
	return call, nil
}
