package utils

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	promParser "github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/promql/parser/posrange"
)

type SourceType int

const (
	UnknownSource SourceType = iota
	NumberSource
	StringSource
	SelectorSource
	FuncSource
	AggregateSource
)

type ExcludedLabel struct {
	Reason   string
	Fragment string
}

type Source struct {
	Selectors        []*promParser.VectorSelector
	Call             *promParser.Call
	ExcludeReason    map[string]ExcludedLabel // Reason why a label was excluded
	Operation        string
	Returns          promParser.ValueType
	ReturnedNumbers  []float64 // If AlwaysReturns=true this is the number that's returned
	IncludedLabels   []string  // Labels that are included by filters, they will be present if exist on source series (by).
	ExcludedLabels   []string  // Labels guaranteed to be excluded from the results (without).
	GuaranteedLabels []string  // Labels guaranteed to be present on the results (matchers).
	Type             SourceType
	FixedLabels      bool // Labels are fixed and only allowed labels can be present.
	IsDead           bool // True if this source cannot be reached and is dead code.
	AlwaysReturns    bool // True if this source always returns results.
}

func LabelsSource(expr string, node promParser.Node) (src []Source) {
	return walkNode(expr, node)
}

func walkNode(expr string, node promParser.Node) (src []Source) {
	var s Source
	switch n := node.(type) {
	case *promParser.AggregateExpr:
		src = append(src, walkAggregation(expr, n)...)

	case *promParser.BinaryExpr:
		src = append(src, parseBinOps(expr, n)...)

	case *promParser.Call:
		s = parseCall(expr, n)
		src = append(src, s)

	case *promParser.MatrixSelector:
		src = append(src, walkNode(expr, n.VectorSelector)...)

	case *promParser.SubqueryExpr:
		src = append(src, walkNode(expr, n.Expr)...)

	case *promParser.NumberLiteral:
		s.Type = NumberSource
		s.Returns = promParser.ValueTypeScalar
		s.ReturnedNumbers = append(s.ReturnedNumbers, n.Val)
		s.IncludedLabels = nil
		s.GuaranteedLabels = nil
		s.FixedLabels = true
		s.AlwaysReturns = true
		s.ExcludeReason = setInMap(
			s.ExcludeReason,
			"",
			ExcludedLabel{
				Reason:   "This returns a number value with no labels.",
				Fragment: getQueryFragment(expr, n.PosRange),
			},
		)
		src = append(src, s)

	case *promParser.ParenExpr:
		src = append(src, walkNode(expr, n.Expr)...)

	case *promParser.StringLiteral:
		s.Type = StringSource
		s.Returns = promParser.ValueTypeString
		s.IncludedLabels = nil
		s.GuaranteedLabels = nil
		s.FixedLabels = true
		s.AlwaysReturns = true
		s.ExcludeReason = setInMap(
			s.ExcludeReason,
			"",
			ExcludedLabel{
				Reason:   "This returns a string value with no labels.",
				Fragment: getQueryFragment(expr, n.PosRange),
			},
		)
		src = append(src, s)

	case *promParser.UnaryExpr:
		src = append(src, walkNode(expr, n.Expr)...)

	case *promParser.StepInvariantExpr:
		// Not possible to get this from the parser.

	case *promParser.VectorSelector:
		s.Type = SelectorSource
		s.Returns = promParser.ValueTypeVector
		s.Selectors = append(s.Selectors, n)
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, n)...)
		src = append(src, s)

	default:
		// unhandled type
	}
	return src
}

func removeFromSlice(sl []string, s ...string) []string {
	for _, v := range s {
		idx := slices.Index(sl, v)
		if idx >= 0 {
			if len(sl) == 1 {
				return nil
			}
			sl = slices.Delete(sl, idx, idx+1)
		}
	}
	return sl
}

func appendToSlice(dst []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}

func setInMap(dst map[string]ExcludedLabel, key string, val ExcludedLabel) map[string]ExcludedLabel {
	if dst == nil {
		dst = map[string]ExcludedLabel{}
	}
	dst[key] = val
	return dst
}

var guaranteedLabelsMatches = []labels.MatchType{labels.MatchEqual, labels.MatchRegexp}

func labelsFromSelectors(matches []labels.MatchType, selectors ...*promParser.VectorSelector) (names []string) {
	nameCount := map[string]int{}
	var ok bool
	for _, selector := range selectors {
		// Any label used in positive filters is gurnateed to be present.
		for _, lm := range selector.LabelMatchers {
			if lm.Name == labels.MetricName {
				continue
			}

			if !slices.Contains(matches, lm.Type) {
				continue
			}

			names = appendToSlice(names, lm.Name)

			if _, ok = nameCount[lm.Name]; !ok {
				nameCount[lm.Name] = 0
			}
			nameCount[lm.Name]++
		}
	}
	for name, cnt := range nameCount {
		if cnt != len(selectors) {
			names = removeFromSlice(names, name)
		}
	}
	return names
}

func getQueryFragment(expr string, pos posrange.PositionRange) string {
	return expr[pos.Start:pos.End]
}

func walkAggregation(expr string, n *promParser.AggregateExpr) (src []Source) {
	var s Source
	switch n.Op {
	case promParser.SUM:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "sum"
			src = append(src, s)
		}
	case promParser.MIN:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "min"
			src = append(src, s)
		}
	case promParser.MAX:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "max"
			src = append(src, s)
		}
	case promParser.AVG:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "avg"
			src = append(src, s)
		}
	case promParser.GROUP:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "group"
			src = append(src, s)
		}
	case promParser.STDDEV:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "stddev"
			src = append(src, s)
		}
	case promParser.STDVAR:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "stdvar"
			src = append(src, s)
		}
	case promParser.COUNT:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "count"
			src = append(src, s)
		}
	case promParser.COUNT_VALUES:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "count_values"
			// Param is the label to store the count value in.
			s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, n.Param.(*promParser.StringLiteral).Val)
			s.IncludedLabels = appendToSlice(s.IncludedLabels, n.Param.(*promParser.StringLiteral).Val)
			s.ExcludedLabels = removeFromSlice(s.ExcludedLabels, n.Param.(*promParser.StringLiteral).Val)
			delete(s.ExcludeReason, n.Param.(*promParser.StringLiteral).Val)
			src = append(src, s)
		}
	case promParser.QUANTILE:
		for _, s = range parseAggregation(expr, n) {
			s.Operation = "quantile"
			src = append(src, s)
		}
	case promParser.TOPK:
		for _, s = range walkNode(expr, n.Expr) {
			s.Type = AggregateSource
			s.Operation = "topk"
			src = append(src, s)
		}
	case promParser.BOTTOMK:
		for _, s = range walkNode(expr, n.Expr) {
			s.Type = AggregateSource
			s.Operation = "bottomk"
			src = append(src, s)
		}
		/*
			TODO these are experimental and promParser.EnableExperimentalFunctions must be set to true to enable parsing of these.
				case promParser.LIMITK:
					s = walkNode(expr, n.Expr)
					s.Type = AggregateSource
					s.Operation = "limitk"
				case promParser.LIMIT_RATIO:
					s = walkNode(expr, n.Expr)
					s.Type = AggregateSource
					s.Operation = "limit_ratio"
		*/
	}
	return src
}

func parseAggregation(expr string, n *promParser.AggregateExpr) (src []Source) {
	var s Source
	for _, s = range walkNode(expr, n.Expr) {
		if n.Without {
			s.ExcludedLabels = appendToSlice(s.ExcludedLabels, n.Grouping...)
			s.IncludedLabels = removeFromSlice(s.IncludedLabels, n.Grouping...)
			s.GuaranteedLabels = removeFromSlice(s.GuaranteedLabels, n.Grouping...)
			for _, name := range n.Grouping {
				s.ExcludeReason = setInMap(
					s.ExcludeReason,
					name,
					ExcludedLabel{
						Reason: fmt.Sprintf("Query is using aggregation with `without(%s)`, all labels included inside `without(...)` will be removed from the results.",
							strings.Join(n.Grouping, ", ")),
						Fragment: getQueryFragment(expr, n.PosRange),
					},
				)
			}
		} else {
			if len(n.Grouping) == 0 {
				s.IncludedLabels = nil
				s.GuaranteedLabels = nil
				s.ExcludeReason = setInMap(
					s.ExcludeReason,
					"",
					ExcludedLabel{
						Reason:   "Query is using aggregation that removes all labels.",
						Fragment: getQueryFragment(expr, n.PosRange),
					},
				)
			} else {
				// Check if source of labels already fixes them.
				if !s.FixedLabels {
					s.IncludedLabels = appendToSlice(s.IncludedLabels, n.Grouping...)
					for _, name := range n.Grouping {
						s.ExcludedLabels = removeFromSlice(s.ExcludedLabels, name)
					}
					s.ExcludeReason = setInMap(
						s.ExcludeReason,
						"",
						ExcludedLabel{
							Reason: fmt.Sprintf("Query is using aggregation with `by(%s)`, only labels included inside `by(...)` will be present on the results.",
								strings.Join(n.Grouping, ", ")),
							Fragment: getQueryFragment(expr, n.PosRange),
						},
					)
				}
				for _, name := range s.GuaranteedLabels {
					if !slices.Contains(n.Grouping, name) {
						s.GuaranteedLabels = removeFromSlice(s.GuaranteedLabels, name)
					}
				}
			}
			s.FixedLabels = true
		}
		s.Type = AggregateSource
		s.Returns = promParser.ValueTypeVector
		s.Call = nil
		src = append(src, s)
	}
	return src
}

func parseCall(expr string, n *promParser.Call) (s Source) {
	s.Type = FuncSource
	s.Operation = n.Func.Name
	s.Call = n

	var vt promParser.ValueType
	for i, e := range n.Args {
		if i >= len(n.Func.ArgTypes) {
			vt = n.Func.ArgTypes[len(n.Func.ArgTypes)-1]
		} else {
			vt = n.Func.ArgTypes[i]
		}

		// nolint: exhaustive
		switch vt {
		case promParser.ValueTypeVector, promParser.ValueTypeMatrix:
			for _, es := range walkNode(expr, e) {
				s.Selectors = append(s.Selectors, es.Selectors...)
			}
		}
	}

	switch n.Func.Name {
	case "abs", "sgn", "acos", "acosh", "asin", "asinh", "atan", "atanh", "cos", "cosh", "sin", "sinh", "tan", "tanh":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "ceil", "floor", "round":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "changes", "resets":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "clamp", "clamp_max", "clamp_min":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "absent", "absent_over_time":
		s.Returns = promParser.ValueTypeVector
		s.FixedLabels = true
		for _, name := range labelsFromSelectors([]labels.MatchType{labels.MatchEqual}, s.Selectors...) {
			s.IncludedLabels = appendToSlice(s.IncludedLabels, name)
			s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, name)
		}
		s.ExcludeReason = setInMap(
			s.ExcludeReason,
			"",
			ExcludedLabel{
				Reason: fmt.Sprintf(`The [%s()](https://prometheus.io/docs/prometheus/latest/querying/functions/#%s) function is used to check if provided query doesn't match any time series.
You will only get any results back if the metric selector you pass doesn't match anything.
Since there are no matching time series there are also no labels. If some time series is missing you cannot read its labels.
This means that the only labels you can get back from absent call are the ones you pass to it.
If you're hoping to get instance specific labels this way and alert when some target is down then that won't work, use the `+"`up`"+` metric instead.`,
					n.Func.Name, n.Func.Name),
				Fragment: getQueryFragment(expr, n.PosRange),
			},
		)

	case "avg_over_time", "count_over_time", "last_over_time", "max_over_time", "min_over_time", "present_over_time", "quantile_over_time", "stddev_over_time", "stdvar_over_time", "sum_over_time":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "days_in_month", "day_of_month", "day_of_week", "day_of_year", "hour", "minute", "month", "year":
		s.Returns = promParser.ValueTypeVector
		// No labels if we don't pass any arguments.
		// Otherwise no change to labels.
		if len(s.Call.Args) == 0 {
			s.FixedLabels = true
			s.AlwaysReturns = true
			s.IncludedLabels = nil
			s.GuaranteedLabels = nil
			s.ExcludeReason = setInMap(
				s.ExcludeReason,
				"",
				ExcludedLabel{
					Reason: fmt.Sprintf("Calling `%s()` with no arguments will return an empty time series with no labels.",
						n.Func.Name),
					Fragment: getQueryFragment(expr, n.PosRange),
				},
			)
		} else {
			s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)
		}

	case "deg", "rad", "ln", "log10", "log2", "sqrt", "exp":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "delta", "idelta", "increase", "deriv", "irate", "rate":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "histogram_avg", "histogram_count", "histogram_sum", "histogram_stddev", "histogram_stdvar", "histogram_fraction", "histogram_quantile":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "holt_winters", "predict_linear":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "label_replace", "label_join":
		// One label added to the results.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, s.Call.Args[1].(*promParser.StringLiteral).Val)

	case "pi":
		s.Returns = promParser.ValueTypeScalar
		s.IncludedLabels = nil
		s.GuaranteedLabels = nil
		s.FixedLabels = true
		s.AlwaysReturns = true
		s.ExcludeReason = setInMap(
			s.ExcludeReason,
			"",
			ExcludedLabel{
				Reason:   fmt.Sprintf("Calling `%s()` will return a scalar value with no labels.", n.Func.Name),
				Fragment: getQueryFragment(expr, n.PosRange),
			},
		)

	case "scalar":
		s.Returns = promParser.ValueTypeScalar
		s.IncludedLabels = nil
		s.GuaranteedLabels = nil
		s.FixedLabels = true
		s.AlwaysReturns = true
		s.ExcludeReason = setInMap(
			s.ExcludeReason,
			"",
			ExcludedLabel{
				Reason:   fmt.Sprintf("Calling `%s()` will return a scalar value with no labels.", n.Func.Name),
				Fragment: getQueryFragment(expr, n.PosRange),
			},
		)

	case "sort", "sort_desc":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector

	case "time":
		s.Returns = promParser.ValueTypeScalar
		s.IncludedLabels = nil
		s.GuaranteedLabels = nil
		s.FixedLabels = true
		s.AlwaysReturns = true
		s.ExcludeReason = setInMap(
			s.ExcludeReason,
			"",
			ExcludedLabel{
				Reason:   fmt.Sprintf("Calling `%s()` will return a scalar value with no labels.", n.Func.Name),
				Fragment: getQueryFragment(expr, n.PosRange),
			},
		)

	case "timestamp":
		// No change to labels.
		s.Returns = promParser.ValueTypeVector
		s.GuaranteedLabels = appendToSlice(s.GuaranteedLabels, labelsFromSelectors(guaranteedLabelsMatches, s.Selectors...)...)

	case "vector":
		s.Returns = promParser.ValueTypeVector
		s.IncludedLabels = nil
		s.GuaranteedLabels = nil
		s.FixedLabels = true
		s.AlwaysReturns = true
		if v, ok := n.Args[0].(*promParser.NumberLiteral); ok {
			s.ReturnedNumbers = append(s.ReturnedNumbers, v.Val)
		}
		s.ExcludeReason = setInMap(
			s.ExcludeReason,
			"",
			ExcludedLabel{
				Reason:   fmt.Sprintf("Calling `%s()` will return a vector value with no labels.", n.Func.Name),
				Fragment: getQueryFragment(expr, n.PosRange),
			},
		)

	default:
		// Unsupported function
		s.Returns = promParser.ValueTypeNone
		s.Call = nil
	}
	return s
}

func parseBinOps(expr string, n *promParser.BinaryExpr) (src []Source) {
	var s Source
	switch {

	// foo{} + 1
	// 1 + foo{}
	// foo{} > 1
	// 1 > foo{}
	case n.VectorMatching == nil:
		lhs := walkNode(expr, n.LHS)
		rhs := walkNode(expr, n.RHS)
		for _, ls := range lhs {
			for _, rs := range rhs {
				switch {
				case ls.AlwaysReturns && rs.AlwaysReturns:
					// Both sides always return something
					for i, lv := range ls.ReturnedNumbers {
						for _, rv := range rs.ReturnedNumbers {
							ls.ReturnedNumbers[i], ls.IsDead = calculateStaticReturn(lv, rv, n.Op, ls.IsDead)
						}
					}
					src = append(src, ls)
				case ls.Returns == promParser.ValueTypeVector, ls.Returns == promParser.ValueTypeMatrix:
					// Use labels from LHS
					src = append(src, ls)
				case rs.Returns == promParser.ValueTypeVector, rs.Returns == promParser.ValueTypeMatrix:
					// Use labels from RHS
					src = append(src, rs)
				}
			}
		}

		// foo{} +               bar{}
		// foo{} + on(...)       bar{}
		// foo{} + ignoring(...) bar{}
	case n.VectorMatching.Card == promParser.CardOneToOne:
		for _, s = range walkNode(expr, n.LHS) {
			if n.VectorMatching.On {
				s.FixedLabels = true
				s.IncludedLabels = appendToSlice(s.IncludedLabels, n.VectorMatching.MatchingLabels...)
				s.ExcludedLabels = removeFromSlice(s.ExcludedLabels, n.VectorMatching.MatchingLabels...)
				for _, name := range n.VectorMatching.MatchingLabels {
					delete(s.ExcludeReason, name)
				}
				s.ExcludeReason = setInMap(
					s.ExcludeReason,
					"",
					ExcludedLabel{
						Reason: fmt.Sprintf(
							"Query is using %s vector matching with `on(%s)`, only labels included inside `on(...)` will be present on the results.",
							n.VectorMatching.Card, strings.Join(n.VectorMatching.MatchingLabels, ", "),
						),
						Fragment: getQueryFragment(
							expr,
							posrange.PositionRange{
								Start: n.LHS.PositionRange().Start,
								End:   n.RHS.PositionRange().End,
							},
						),
					},
				)
			} else {
				s.IncludedLabels = removeFromSlice(s.IncludedLabels, n.VectorMatching.MatchingLabels...)
				s.GuaranteedLabels = removeFromSlice(s.GuaranteedLabels, n.VectorMatching.MatchingLabels...)
				s.ExcludedLabels = appendToSlice(s.ExcludedLabels, n.VectorMatching.MatchingLabels...)
				for _, name := range n.VectorMatching.MatchingLabels {
					s.ExcludeReason = setInMap(
						s.ExcludeReason,
						name,
						ExcludedLabel{
							Reason: fmt.Sprintf(
								"Query is using %s vector matching with `ignoring(%s)`, all labels included inside `ignoring(...)` will be removed on the results.",
								n.VectorMatching.Card, strings.Join(n.VectorMatching.MatchingLabels, ", "),
							),
							Fragment: getQueryFragment(
								expr,
								posrange.PositionRange{
									Start: n.LHS.PositionRange().Start,
									End:   n.RHS.PositionRange().End,
								},
							),
						},
					)
				}
			}
			if s.Operation == "" {
				s.Operation = n.VectorMatching.Card.String()
			}
			src = append(src, s)
		}

		// foo{} + on(...)       group_left(...) bar{}
		// foo{} + ignoring(...) group_left(...) bar{}
	case n.VectorMatching.Card == promParser.CardOneToMany:
		for _, s = range walkNode(expr, n.RHS) {
			s.IncludedLabels = appendToSlice(s.IncludedLabels, n.VectorMatching.Include...)
			if n.VectorMatching.On {
				s.IncludedLabels = appendToSlice(s.IncludedLabels, n.VectorMatching.MatchingLabels...)
				for _, name := range n.VectorMatching.MatchingLabels {
					delete(s.ExcludeReason, name)
				}
			}
			s.ExcludedLabels = removeFromSlice(s.ExcludedLabels, n.VectorMatching.Include...)
			for _, name := range n.VectorMatching.Include {
				delete(s.ExcludeReason, name)
			}
			if s.Operation == "" {
				s.Operation = n.VectorMatching.Card.String()
			}
			src = append(src, s)
		}

		// foo{} + on(...)       group_right(...) bar{}
		// foo{} + ignoring(...) group_right(...) bar{}
	case n.VectorMatching.Card == promParser.CardManyToOne:
		for _, s = range walkNode(expr, n.LHS) {
			s.IncludedLabels = appendToSlice(s.IncludedLabels, n.VectorMatching.Include...)
			if n.VectorMatching.On {
				s.IncludedLabels = appendToSlice(s.IncludedLabels, n.VectorMatching.MatchingLabels...)
				for _, name := range n.VectorMatching.MatchingLabels {
					delete(s.ExcludeReason, name)
				}
			}
			s.ExcludedLabels = removeFromSlice(s.ExcludedLabels, n.VectorMatching.Include...)
			for _, name := range n.VectorMatching.Include {
				delete(s.ExcludeReason, name)
			}
			if s.Operation == "" {
				s.Operation = n.VectorMatching.Card.String()
			}
			src = append(src, s)
		}

		// foo{} and on(...)       bar{}
		// foo{} and ignoring(...) bar{}
	case n.VectorMatching.Card == promParser.CardManyToMany:
		var lhsCanBeEmpty bool // true if any of the LHS query can produce empty results.
		for _, s = range walkNode(expr, n.LHS) {
			if n.VectorMatching.On {
				s.IncludedLabels = appendToSlice(s.IncludedLabels, n.VectorMatching.MatchingLabels...)
				for _, name := range n.VectorMatching.MatchingLabels {
					delete(s.ExcludeReason, name)
				}
			}
			if s.Operation == "" {
				s.Operation = n.VectorMatching.Card.String()
			}
			if !s.AlwaysReturns {
				lhsCanBeEmpty = true
			}
			src = append(src, s)
		}
		if n.Op == promParser.LOR {
			for _, s = range walkNode(expr, n.RHS) {
				if s.Operation == "" {
					s.Operation = n.VectorMatching.Card.String()
				}
				// If LHS can NOT be empty then RHS is dead code.
				if !lhsCanBeEmpty {
					s.IsDead = true
				}
				src = append(src, s)
			}
		}
	}
	return src
}

func calculateStaticReturn(lv, rv float64, op promParser.ItemType, isDead bool) (float64, bool) {
	switch op {
	case promParser.EQLC:
		if lv != rv {
			return lv, true
		}
	case promParser.NEQ:
		if lv == rv {
			return lv, true
		}
	case promParser.LTE:
		if lv > rv {
			return lv, true
		}
	case promParser.LSS:
		if lv >= rv {
			return lv, true
		}
	case promParser.GTE:
		if lv < rv {
			return lv, true
		}
	case promParser.GTR:
		if lv <= rv {
			return lv, true
		}
	case promParser.ADD:
		return lv + rv, isDead
	case promParser.SUB:
		return lv - rv, isDead
	case promParser.MUL:
		return lv * rv, isDead
	case promParser.DIV:
		return lv / rv, isDead
	case promParser.MOD:
		return math.Mod(lv, rv), isDead
	case promParser.POW:
		return math.Pow(lv, rv), isDead
	}
	return lv, isDead
}
