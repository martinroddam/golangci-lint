package checkers

import (
	"go/ast"
	"go/token"

	"github.com/go-critic/checkers/internal/lintutil"
	"github.com/go-lintpack/lintpack"
	"github.com/go-lintpack/lintpack/astwalk"
	"github.com/go-toolsmith/astcopy"
	"github.com/go-toolsmith/astequal"
	"golang.org/x/tools/go/ast/astutil"
)

func init() {
	var info lintpack.CheckerInfo
	info.Name = "boolExprSimplify"
	info.Tags = []string{"style", "experimental"}
	info.Summary = "Detects bool expressions that can be simplified"
	info.Before = `
a := !(elapsed >= expectElapsedMin)
b := !(x) == !(y)`
	info.After = `
a := elapsed < expectElapsedMin
b := (x) == (y)`

	lintpack.AddChecker(&info, func(ctx *lintpack.CheckerContext) lintpack.FileWalker {
		return astwalk.WalkerForExpr(&boolExprSimplifyChecker{ctx: ctx})
	})
}

type boolExprSimplifyChecker struct {
	astwalk.WalkHandler
	ctx *lintpack.CheckerContext
}

func (c *boolExprSimplifyChecker) VisitExpr(x ast.Expr) {
	// TODO: avoid eager copy?
	// Can't be stable until wasted copying is fixed.
	y := c.simplifyBool(astcopy.Expr(x))
	if !astequal.Expr(x, y) {
		c.warn(x, y)
	}
}

func (c *boolExprSimplifyChecker) simplifyBool(x ast.Expr) ast.Expr {
	return astutil.Apply(x, nil, func(cur *astutil.Cursor) bool {
		return c.doubleNegation(cur) ||
			c.negatedEquals(cur) ||
			c.invertComparison(cur) ||
			c.combineChecks(cur) ||
			true
	}).(ast.Expr)
}

func (c *boolExprSimplifyChecker) doubleNegation(cur *astutil.Cursor) bool {
	neg1 := lintutil.AsUnaryExprOp(cur.Node(), token.NOT)
	neg2 := lintutil.AsUnaryExprOp(astutil.Unparen(neg1.X), token.NOT)
	if !lintutil.IsNil(neg1) && !lintutil.IsNil(neg2) {
		cur.Replace(astutil.Unparen(neg2.X))
		return true
	}
	return false
}

func (c *boolExprSimplifyChecker) negatedEquals(cur *astutil.Cursor) bool {
	x, ok := cur.Node().(*ast.BinaryExpr)
	if !ok || x.Op != token.EQL {
		return false
	}
	neg1 := lintutil.AsUnaryExprOp(x.X, token.NOT)
	neg2 := lintutil.AsUnaryExprOp(x.Y, token.NOT)
	if !lintutil.IsNil(neg1) && !lintutil.IsNil(neg2) {
		x.X = neg1.X
		x.Y = neg2.X
		return true
	}
	return false
}

func (c *boolExprSimplifyChecker) invertComparison(cur *astutil.Cursor) bool {
	neg := lintutil.AsUnaryExprOp(cur.Node(), token.NOT)
	cmp := lintutil.AsBinaryExpr(astutil.Unparen(neg.X))
	if lintutil.IsNil(neg) || lintutil.IsNil(cmp) {
		return false
	}

	// Replace operator to its negated form.
	switch cmp.Op {
	case token.EQL:
		cmp.Op = token.NEQ
	case token.NEQ:
		cmp.Op = token.EQL
	case token.LSS:
		cmp.Op = token.GEQ
	case token.GTR:
		cmp.Op = token.LEQ
	case token.LEQ:
		cmp.Op = token.GTR
	case token.GEQ:
		cmp.Op = token.LSS

	default:
		return false
	}
	cur.Replace(cmp)
	return true
}

func (c *boolExprSimplifyChecker) combineChecks(cur *astutil.Cursor) bool {
	or := lintutil.AsBinaryExprOp(cur.Node(), token.LOR)
	lhs := lintutil.AsBinaryExpr(astutil.Unparen(or.X))
	rhs := lintutil.AsBinaryExpr(astutil.Unparen(or.Y))

	if !astequal.Expr(lhs.X, rhs.X) || !astequal.Expr(lhs.Y, rhs.Y) {
		return false
	}
	if !isSafeExpr(c.ctx.TypesInfo, lhs.X) || !isSafeExpr(c.ctx.TypesInfo, lhs.Y) {
		return false
	}

	combTable := [...]struct {
		x      token.Token
		y      token.Token
		result token.Token
	}{
		{token.GTR, token.EQL, token.GEQ},
		{token.EQL, token.GTR, token.GEQ},
		{token.LSS, token.EQL, token.LEQ},
		{token.EQL, token.LSS, token.LEQ},
	}
	for _, comb := range &combTable {
		if comb.x == lhs.Op && comb.y == rhs.Op {
			lhs.Op = comb.result
			cur.Replace(lhs)
			return true
		}
	}
	return false
}

func (c *boolExprSimplifyChecker) warn(cause, suggestion ast.Expr) {
	c.SkipChilds = true
	c.ctx.Warn(cause, "can simplify `%s` to `%s`", cause, suggestion)
}
