package billingexpr

import "github.com/expr-lang/expr/ast"

const requestRuleTraceFunction = "__billing_request_rule"

type requestRuleTracePatcher struct{}

func (requestRuleTracePatcher) Visit(node *ast.Node) {
	conditional, ok := (*node).(*ast.ConditionalNode)
	if !ok {
		return
	}
	switch conditional.Exp1.(type) {
	case *ast.IntegerNode, *ast.FloatNode:
	default:
		return
	}
	switch fallback := conditional.Exp2.(type) {
	case *ast.IntegerNode:
		if fallback.Value != 1 {
			return
		}
	case *ast.FloatNode:
		if fallback.Value != 1 {
			return
		}
	default:
		return
	}
	requestReference := ast.Find(conditional.Cond, func(candidate ast.Node) bool {
		call, ok := candidate.(*ast.CallNode)
		if !ok {
			return false
		}
		identifier, ok := call.Callee.(*ast.IdentifierNode)
		if !ok {
			return false
		}
		switch identifier.Value {
		case "param", "header", "hour", "minute", "weekday", "month", "day":
			return true
		}
		return false
	})
	if requestReference == nil {
		return
	}
	expression := conditional.String()
	ast.Patch(&conditional.Cond, &ast.CallNode{
		Callee: &ast.IdentifierNode{Value: requestRuleTraceFunction},
		Arguments: []ast.Node{
			&ast.StringNode{Value: expression},
			conditional.Cond,
		},
	})
}
