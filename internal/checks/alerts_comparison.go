package checks

import (
	"context"

	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/utils"

	promParser "github.com/prometheus/prometheus/promql/parser"
)

const (
	ComparisonCheckName = "alerts/comparison"
)

func NewComparisonCheck() ComparisonCheck {
	return ComparisonCheck{}
}

type ComparisonCheck struct{}

func (c ComparisonCheck) Meta() CheckMeta {
	return CheckMeta{IsOnline: false}
}

func (c ComparisonCheck) String() string {
	return ComparisonCheckName
}

func (c ComparisonCheck) Reporter() string {
	return ComparisonCheckName
}

func (c ComparisonCheck) Check(_ context.Context, _ string, rule parser.Rule, _ []discovery.Entry) (problems []Problem) {
	if rule.AlertingRule == nil {
		return problems
	}

	if rule.AlertingRule.Expr.SyntaxError != nil {
		return problems
	}

	expr := rule.Expr().Query

	if n := utils.HasOuterBinaryExpr(expr); n != nil && n.Op == promParser.LOR {
		if (hasComparision(n.LHS) == nil || hasComparision(n.RHS) == nil) && !isAbsent(n.LHS) && !isAbsent(n.RHS) {
			problems = append(problems, Problem{
				Fragment: rule.AlertingRule.Expr.Value.Value,
				Lines:    rule.AlertingRule.Expr.Lines(),
				Reporter: c.Reporter(),
				Text:     "alert query uses 'or' operator with one side of the query that will always return a result, this alert will always fire",
				Severity: rewriteSeverity(Warning, n.LHS, n.RHS),
			})
		}
	}

	if n := hasComparision(expr.Node); n != nil {
		if n.ReturnBool && hasComparision(n.LHS) == nil && hasComparision(n.RHS) == nil {
			problems = append(problems, Problem{
				Fragment: rule.AlertingRule.Expr.Value.Value,
				Lines:    rule.AlertingRule.Expr.Lines(),
				Reporter: c.Reporter(),
				Text:     "alert query uses bool modifier for comparison, this means it will always return a result and the alert will always fire",
				Severity: Bug,
			})
		}
		return problems
	}

	if hasAbsent(expr) {
		return problems
	}

	problems = append(problems, Problem{
		Fragment: rule.AlertingRule.Expr.Value.Value,
		Lines:    rule.AlertingRule.Expr.Lines(),
		Reporter: c.Reporter(),
		Text:     "alert query doesn't have any condition, it will always fire if the metric exists",
		Severity: Warning,
	})

	return problems
}

func hasComparision(n promParser.Node) *promParser.BinaryExpr {
	if node, ok := n.(*promParser.BinaryExpr); ok {
		if node.Op.IsComparisonOperator() {
			return node
		}
		if node.Op == promParser.LUNLESS {
			return node
		}
	}

	for _, child := range promParser.Children(n) {
		if node := hasComparision(child); node != nil {
			return node
		}
	}

	return nil
}

func isAbsent(node promParser.Node) bool {
	if node, ok := node.(*promParser.Call); ok && (node.Func.Name == "absent") {
		return true
	}

	for _, child := range promParser.Children(node) {
		if isAbsent(child) {
			return true
		}
	}

	return false
}

func hasAbsent(n *parser.PromQLNode) bool {
	if isAbsent(n.Node) {
		return true
	}
	for _, child := range n.Children {
		if hasAbsent(child) {
			return true
		}
	}
	return false
}

func rewriteSeverity(s Severity, nodes ...promParser.Node) Severity {
	for _, node := range nodes {
		if n, ok := node.(*promParser.Call); ok && n.Func.Name == "vector" {
			return Bug
		}
	}
	return s
}
