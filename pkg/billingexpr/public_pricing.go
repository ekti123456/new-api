package billingexpr

import (
	"strings"

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
	parts := make([]string, 0)
	hasBase := false
	for _, factor := range splitPricingFactors(tree.Node) {
		tierReference := ast.Find(factor, func(node ast.Node) bool {
			identifier, ok := node.(*ast.IdentifierNode)
			return ok && identifier.Value == "tier"
		})
		if tierReference != nil {
			if hasBase {
				return "", true
			}
			base := publicBaseTier(factor)
			if base == "" {
				return "", true
			}
			hasBase = true
			parts = append(parts, base)
			continue
		}
		privateReference := ast.Find(factor, func(node ast.Node) bool {
			identifier, ok := node.(*ast.IdentifierNode)
			return ok && identifier.Value == "channel_id"
		})
		if privateReference != nil {
			return "", true
		}
		parts = append(parts, "("+factor.String()+")")
	}
	if !hasBase {
		return "", true
	}
	return strings.Join(parts, " * "), true
}

func splitPricingFactors(node ast.Node) []ast.Node {
	if binary, ok := node.(*ast.BinaryNode); ok && binary.Operator == "*" {
		return append(splitPricingFactors(binary.Left), splitPricingFactors(binary.Right)...)
	}
	return []ast.Node{node}
}

func publicBaseTier(fallback ast.Node) string {
	for {
		conditional, ok := fallback.(*ast.ConditionalNode)
		if !ok {
			break
		}
		fallback = conditional.Exp2
	}
	call, ok := fallback.(*ast.CallNode)
	if !ok || len(call.Arguments) != 2 {
		return ""
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != "tier" {
		return ""
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
		return ""
	}
	return `tier("base", ` + call.Arguments[1].String() + `)`
}
