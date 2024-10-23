package checks_test

import (
	"fmt"
	"testing"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"
)

func newFragileCheck(_ *promapi.FailoverGroup) checks.RuleChecker {
	return checks.NewFragileCheck()
}

func fragileSampleFunc(s string) string {
	return fmt.Sprintf("Using `%s` to select time series might return different set of time series on every query, which would cause flapping alerts.", s)
}

func TestFragileCheck(t *testing.T) {
	text := "Aggregation using `without()` can be fragile when used inside binary expression because both sides must have identical sets of labels to produce any results, adding or removing labels to metrics used here can easily break the query, consider aggregating using `by()` to ensure consistent labels."

	testCases := []checkTest{
		{
			description: "ignores syntax errors",
			content:     "- record: foo\n  expr: up ==\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
		{
			description: "ignores simple comparison",
			content:     "- record: foo\n  expr: up == 0\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
		{
			description: "ignores simple division",
			content:     "- record: foo\n  expr: foo / bar\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
		{
			description: "ignores unless",
			content:     "- record: foo\n  expr: foo unless sum(bar) without(job)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
		{
			description: "ignores safe division",
			content:     "- record: foo\n  expr: foo / sum(bar)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
		{
			description: "warns about fragile division",
			content:     "- record: foo\n  expr: foo / sum(bar) without(job)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     text,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "warns about fragile sum",
			content:     "- record: foo\n  expr: sum(foo) without(job) + sum(bar) without(job)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     text,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "warns about fragile sum inside a condition",
			content:     "- alert: foo\n  expr: (sum(foo) without(job) + sum(bar) without(job)) > 1\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     text,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "warns about fragile division inside a condition",
			content:     "- alert: foo\n  expr: (foo / sum(bar) without(job)) > 1\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     text,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "warns about fragile sum inside a complex rule",
			content:     "- alert: foo\n  expr: (sum(foo) without(job) + sum(bar)) > 1 unless sum(bob) without(job) < 10\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     text,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "ignores safe addition",
			content:     "- record: foo\n  expr: sum(foo) + sum(bar)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
		{
			description: "ignores addition if source metric is the same",
			content:     "- record: foo\n  expr: sum(foo) without(bar) + sum(foo) without(bar)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
		{
			description: "handles nested aggregations correctly / LHS",
			content: `
- alert: foo
  expr: |
    count without (foo) (
        probe_success{job="foo"} == 0 or probe_duration_seconds{job="foo"} >= 15
    ) > 3
`,
			checker:    newFragileCheck,
			prometheus: noProm,
			problems:   noProblems,
		},
		{
			description: "handles nested aggregations correctly / RHS",
			content: `
- alert: foo
  expr: |
    3 <
    count without (foo) (
        probe_success{job="foo"} == 0 or probe_duration_seconds{job="foo"} >= 15
    )
`,
			checker:    newFragileCheck,
			prometheus: noProm,
			problems:   noProblems,
		},
		{
			description: "handles nested aggregations correctly / both",
			content: `
- alert: foo
  expr: |
    3 <
    count without (foo) (
        probe_success{job="foo"} == 0 or probe_duration_seconds{job="foo"} >= 15
    ) > 2
`,
			checker:    newFragileCheck,
			prometheus: noProm,
			problems:   noProblems,
		},
		{
			description: "(...) without(instance) on(app_name) is ok",
			content: `
- alert: foo
  expr: |
    quantile(0.95,
      container_memory_working_set_bytes{app_name!="foo.service"}
      / (container_spec_memory_limit_bytes > 0)
    ) without(instance)
    * on(app_name) group_left(product, team, notify) job:ownership
`,
			checker:    newFragileCheck,
			prometheus: noProm,
			problems:   noProblems,
		},
		{
			description: "warns about topk() as source of series",
			content:     "- alert: foo\n  expr: topk(10, foo)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     fragileSampleFunc("topk"),
						Details:  checks.FragileCheckSamplingDetails,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "warns about topk() as source of series (or)",
			content:     "- alert: foo\n  expr: bar or topk(10, foo)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     fragileSampleFunc("topk"),
						Details:  checks.FragileCheckSamplingDetails,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "warns about topk() as source of series (multiple)",
			content:     "- alert: foo\n  expr: bar or topk(10, foo) or bottomk(10, foo)\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems: func(_ string) []checks.Problem {
				return []checks.Problem{
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     fragileSampleFunc("topk"),
						Details:  checks.FragileCheckSamplingDetails,
						Severity: checks.Warning,
					},
					{
						Lines: parser.LineRange{
							First: 2,
							Last:  2,
						},
						Reporter: checks.FragileCheckName,
						Text:     fragileSampleFunc("bottomk"),
						Details:  checks.FragileCheckSamplingDetails,
						Severity: checks.Warning,
					},
				}
			},
		},
		{
			description: "ignores aggregated topk()",
			content:     "- alert: foo\n  expr: min(topk(10, foo)) > 5000\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    noProblems,
		},
	}

	runTests(t, testCases)
}
