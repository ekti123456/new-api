package billingexpr

import (
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

func PublicPricingExpr(expression string) (string, bool) {
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return "", true
	}
	channelReference := ast.Find(tree.Node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.IdentifierNode)
		return ok && identifier.Value == "channel_id"
	})
	if channelReference == nil {
		return expression, false
	}
	fallback := tree.Node
	for {
		conditional, ok := fallback.(*ast.ConditionalNode)
		if !ok {
			break
		}
		fallback = conditional.Exp2
	}
	call, ok := fallback.(*ast.CallNode)
	if !ok || len(call.Arguments) != 2 {
		return "", true
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != "tier" {
		return "", true
	}
	unsafeReference := ast.Find(call.Arguments[1], func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.IdentifierNode:
			return typed.Value == "channel_id"
		case *ast.ConditionalNode, *ast.CallNode:
			return true
		}
		return false
	})
	if unsafeReference != nil {
		return "", true
	}
	return `tier("base", ` + call.Arguments[1].String() + `)`, true
}
