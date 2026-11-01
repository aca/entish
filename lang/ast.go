package lang

import "strconv"

type Pos struct {
	File      string
	Line, Col int
}

func (p Pos) String() string {
	if p.File == "" {
		return strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Col)
	}
	return p.File + ":" + strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Col)
}

type Block struct {
	Pos   Pos
	Stmts []Stmt
	Fns   []*FnDef // hoisted
}

type Stmt interface{ stmt() }

type LetStmt struct {
	Pos  Pos
	Name string
	Val  Expr
}

type FnStmt struct{ Def *FnDef }

type ExprStmt struct{ X Expr }

func (*LetStmt) stmt()  {}
func (*FnStmt) stmt()   {}
func (*ExprStmt) stmt() {}

type FnDef struct {
	Pos    Pos
	Name   string
	Params []string
	Rest   string
	Body   *Block
}

type Expr interface{ pos() Pos }

type OrExpr struct {
	P    Pos
	L, R Expr
}
type AndExpr struct {
	P    Pos
	L, R Expr
}
type NotExpr struct {
	P Pos
	X Expr
}

// Pipeline joins stages with "|" (bytes) or "|>" (values).
type Pipeline struct {
	P      Pos
	Stages []Expr
	Ops    []string
}

type target int

const (
	tExternal target = iota
	tFn
	tBuiltin
	tVar
)

// Call is `name args...`, `^name args...`, `./path args...` or `$f args...`.
type Call struct {
	P       Pos
	Name    string
	Caret   bool
	HeadVar *VarRef
	Args    []*Word
	Redirs  []*Redir

	target  target
	fn      *FnDef
	builtin *Builtin
	piped   bool // the value of a preceding |> is appended as the last arg
	stdin   bool // a converter after | whose data arg is stdin: ls | lines
}

type Redir struct {
	Op     string // > >> < 2> 2>> 2>&1 >&2
	Target *Word
}

type BinExpr struct {
	P    Pos
	Op   string
	L, R Expr
}

type IfExpr struct {
	P    Pos
	Cond Expr
	Then *Block
	Else Expr // *IfExpr, *BlockExpr or nil
}

type TryExpr struct {
	P       Pos
	Body    Expr // *BlockExpr or *Call with HeadVar
	Name    string
	Handler *Block
}

type BlockExpr struct{ B *Block }

// WordExpr is a single word used as a value.
type WordExpr struct{ W *Word }

func (e *OrExpr) pos() Pos    { return e.P }
func (e *AndExpr) pos() Pos   { return e.P }
func (e *NotExpr) pos() Pos   { return e.P }
func (e *Pipeline) pos() Pos  { return e.P }
func (e *Call) pos() Pos      { return e.P }
func (e *BinExpr) pos() Pos   { return e.P }
func (e *IfExpr) pos() Pos    { return e.P }
func (e *TryExpr) pos() Pos   { return e.P }
func (e *BlockExpr) pos() Pos { return e.B.Pos }
func (e *WordExpr) pos() Pos  { return e.W.Pos }

// Word is a run of glued atoms.
type Word struct {
	Pos   Pos
	Parts []Atom
}

type Atom interface{ atom() }

// LitAtom is unquoted text. Glob marks text whose * ? [..] are active.
type LitAtom struct {
	S    string
	Glob bool
}
type NumAtom struct{ N *Num }
type StrAtom struct{ Parts []StrPart }
type StrPart struct {
	Lit string
	Var *VarRef
}

// MLAtom is a Zig-style multiline string; each line is raw (\\) or
// interpolated ($\\).
type MLAtom struct{ Lines [][]StrPart }

type VarRef struct {
	Pos   Pos
	Name  string
	Env   bool
	Index []Index
}

// Index is [key], or a slice [lo:hi] whose omitted ends are nil.
type Index struct {
	Key, Lo, Hi *IndexArg
}

type IndexArg struct {
	Lit   Value // string or *Num
	Var   *VarRef
	X     Expr // $xs[(len $xs) - 1]; never inside a string
}

// indexExprs is every expression in v's indexes, including those of
// variables used as indexes.
func (v *VarRef) indexExprs() []Expr {
	var xs []Expr
	for _, ix := range v.Index {
		for _, a := range []*IndexArg{ix.Key, ix.Lo, ix.Hi} {
			switch {
			case a == nil:
			case a.X != nil:
				xs = append(xs, a.X)
			case a.Var != nil:
				xs = append(xs, a.Var.indexExprs()...)
			}
		}
	}
	return xs
}

type SubAtom struct{ X Expr }   // $( ... )
type GroupAtom struct{ X Expr } // ( ... )
type ListAtom struct{ Items []*Word }
type MapAtom struct {
	Keys []string
	Vals []*Word
}
type LambdaAtom struct {
	Pos    Pos
	Params []string
	Rest   string
	Body   *Block
	usesIt bool
}
type SpreadAtom struct{ X Atom } // ...$x or ...( )
type HomeAtom struct{ Pos Pos }  // ~ at the start of a word

func (*LitAtom) atom()    {}
func (*NumAtom) atom()    {}
func (*StrAtom) atom()    {}
func (*MLAtom) atom()     {}
func (*VarRef) atom()     {}
func (*SubAtom) atom()    {}
func (*GroupAtom) atom()  {}
func (*ListAtom) atom()   {}
func (*MapAtom) atom()    {}
func (*LambdaAtom) atom() {}
func (*SpreadAtom) atom() {}
func (*HomeAtom) atom()   {}
