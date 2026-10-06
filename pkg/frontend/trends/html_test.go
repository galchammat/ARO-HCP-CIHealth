package trends

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	readmodeltrends "github.com/roivaz/ARO-HCP-CIHealth/pkg/frontend/readmodel/trends"
)

func TestRenderHTMLWithData(t *testing.T) {
	data := readmodeltrends.TrendsData{
		Meta: readmodeltrends.TrendsMeta{
			StartDate:    "2026-03-16",
			EndDate:      "2026-03-18",
			Dates:        []string{"2026-03-16", "2026-03-17", "2026-03-18"},
			Environments: []string{"dev"},
			HasData:      true,
		},
		Environments: []readmodeltrends.EnvironmentTrends{
			{
				Environment: "dev",
				HasData:     true,
				Charts: []readmodeltrends.MetricChart{
					{
						Title:   "Daily run outcomes",
						Kind:    readmodeltrends.ChartKindCount,
						XLabels: []string{"2026-03-16", "2026-03-17", "2026-03-18"},
						Series: []readmodeltrends.MetricSeries{
							{
								Metric: "run_count",
								Label:  "Total runs",
								Color:  "#2563eb",
								Points: []float64{10, 12, 8},
								Total:  30,
								Max:    12,
							},
						},
					},
				},
			},
		},
	}

	out := RenderHTML(data, PageOptions{})
	for _, snippet := range []string{
		"CIHealth Trends",
		"Environment: DEV",
		"Daily run outcomes",
		"trend-line-svg",
		"<polyline",
		"Total runs",
		`tooltip.id = "trend-tooltip"`,
		`target.addEventListener("pointerenter"`,
		`target.addEventListener("focus"`,
		`title.remove()`,
		`class="inline-tooltip-trigger exec-heading-help" data-tooltip-trigger`,
		`<span aria-hidden="true">i</span>`,
		`function setOpen(tooltip, open)`,
	} {
		if !strings.Contains(out, snippet) {
			t.Fatalf("expected rendered HTML to contain %q", snippet)
		}
	}
	panels := regexp.MustCompile(`<span class="inline-tooltip-panel" role="tooltip">[^<]*</span>`)
	if len(panels.FindAllString(out, -1)) != 2 {
		t.Fatal("expected contextual help for the environment and chart headings")
	}
	withoutHelp := panels.ReplaceAllString(out, "")
	for _, explanation := range []string{"Individual-PR eligibility", "95% Wilson"} {
		if !strings.Contains(out, explanation) || strings.Contains(withoutHelp, explanation) {
			t.Fatalf("explanation must appear only in a help panel: %q", explanation)
		}
	}
	for _, repeated := range []string{`<h1`, "CI health trends", `<p class="meta">`, `class="trend-chart-note"`, "view over", "days with stored DEV metrics", "Sample sizes and outcomes", "<table", "table below"} {
		if strings.Contains(out, repeated) {
			t.Fatalf("unexpected repeated explanatory text: %q", repeated)
		}
	}
}

func TestRenderHTMLEmptyState(t *testing.T) {
	data := readmodeltrends.TrendsData{
		Meta: readmodeltrends.TrendsMeta{Environments: []string{"dev"}},
	}
	out := RenderHTML(data, PageOptions{})
	if !strings.Contains(out, "No stored daily metrics were found") {
		t.Fatalf("expected empty-state message in rendered HTML")
	}
	if strings.Contains(out, "<polyline") || strings.Contains(out, "CI health trends") {
		t.Fatalf("did not expect chart polylines or redundant heading in empty-state HTML")
	}
	if !strings.Contains(out, "function setOpen(tooltip, open)") {
		t.Fatal("empty-page help must still support click and keyboard dismissal")
	}
	for _, snippet := range []string{`data-inline-tooltip`, `.inline-tooltip:hover .inline-tooltip-panel`, `.inline-tooltip-trigger:focus-visible + .inline-tooltip-panel`} {
		if !strings.Contains(out, snippet) {
			t.Fatalf("empty-page help is missing shared markup or styling: %q", snippet)
		}
	}
}

func TestChartHelpEscapesNote(t *testing.T) {
	out := renderChart(readmodeltrends.MetricChart{
		Title: "Success rates",
		Note:  `<script>alert("note")</script>`,
	})
	if strings.Contains(out, "<script>") || !strings.Contains(out, "&lt;script&gt;") {
		t.Fatal("chart help must escape note text")
	}
}

func TestRenderLineChartSingleSampleUsesMarker(t *testing.T) {
	series := []readmodeltrends.MetricSeries{
		{Metric: "run_count", Label: "Total runs", Color: "#2563eb", Points: []float64{5}, Total: 5, Max: 5},
	}
	svg := renderLineChartSVG(series, []string{"2026-03-16"}, readmodeltrends.ChartKindCount)
	if strings.Contains(svg, "<polyline") {
		t.Fatalf("single-sample series should not render a polyline")
	}
	if !strings.Contains(svg, "<circle") {
		t.Fatalf("single-sample series should render a marker circle")
	}
}

func TestRenderLinePercentChartUsesPercentAxisAndBreaksGaps(t *testing.T) {
	series := []readmodeltrends.MetricSeries{
		{
			Metric:  "overall_success_rate",
			Label:   "Overall success",
			Color:   "#059669",
			Points:  []float64{80, 0, 60},
			Defined: []bool{true, false, true},
			Max:     100,
		},
	}
	svg := renderLineChartSVG(series, []string{"2026-03-16", "2026-03-23", "2026-03-30"}, readmodeltrends.ChartKindPercent)
	if !strings.Contains(svg, "100%") {
		t.Fatalf("percent chart should render a %% axis label, got: %s", svg)
	}
	// Two defined points separated by a gap are isolated, so each is a marker
	// and no polyline should connect across the undefined middle week.
	if strings.Contains(svg, "<polyline") {
		t.Fatalf("gap-separated single points should not be joined by a polyline")
	}
	if strings.Count(svg, "<circle") < 2 {
		t.Fatalf("expected a marker for each defined point, got: %s", svg)
	}
}

func TestRenderLinePercentChartConnectsConsecutiveDefinedPoints(t *testing.T) {
	series := []readmodeltrends.MetricSeries{
		{
			Metric:  "overall_success_rate",
			Label:   "Overall success",
			Color:   "#059669",
			Points:  []float64{80, 90, 0},
			Defined: []bool{true, true, false},
			Max:     100,
		},
	}
	svg := renderLineChartSVG(series, []string{"2026-03-16", "2026-03-23", "2026-03-30"}, readmodeltrends.ChartKindPercent)
	if !strings.Contains(svg, "<polyline") {
		t.Fatalf("consecutive defined points should be joined by a polyline, got: %s", svg)
	}
}

func TestNiceAxis(t *testing.T) {
	cases := []struct {
		max     float64
		wantMin float64
	}{
		{max: 0, wantMin: 1},
		{max: 3, wantMin: 3},
		{max: 12, wantMin: 12},
		{max: 97, wantMin: 97},
	}

	for _, tc := range cases {
		axisMax, step := niceAxis(tc.max)
		if axisMax < tc.wantMin {
			t.Fatalf("niceAxis(%v) axisMax = %v, want >= %v", tc.max, axisMax, tc.wantMin)
		}
		if step <= 0 {
			t.Fatalf("niceAxis(%v) step = %v, want > 0", tc.max, step)
		}
	}
}

func TestDiagnosticChartKeepsIntervalsInPointDetails(t *testing.T) {
	series := readmodeltrends.MetricSeries{
		Label: "Overall success", Color: "blue", Points: []float64{80, 0, 90},
		Defined: []bool{true, false, true}, Lower: []float64{40, 0, 50}, Upper: []float64{99, 0, 100},
		Notes:   []string{"2026-03-16 UTC; 4/5 runs; n=5; low sample; partial period", "", "2026-03-18 UTC; 9/10 runs"},
		Caution: []bool{true, false, true}, HideIntervals: true, Emphasize: true,
	}
	other := series
	other.Label, other.Points = "Other failures (% of all runs)", []float64{0, 0, 10}
	out := renderChart(readmodeltrends.MetricChart{
		Title: "All runs", Kind: readmodeltrends.ChartKindPercent,
		XLabels: []string{"2026-03-16", "2026-03-17", "2026-03-18"},
		Series:  []readmodeltrends.MetricSeries{series, series, series}, Other: &other,
	})
	for _, want := range []string{"95% confidence interval: 40.0–99.0%", "4/5 runs", "low sample", "partial period", `data-emphasize="true"`, `viewBox="0 0 960 150"`, "build failures, CI infrastructure failures, and unclassified failures", "height=\"8.00\""} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, `class="trend-ci"`) || strings.Contains(out, "<polyline") {
		t.Fatal("diagnostic chart should hide whiskers and not connect across missing periods")
	}
	if strings.Count(out, `class="trend-marker"`) != 6 || strings.Count(out, `class="trend-bar"`) != 2 {
		t.Fatal("every defined point must have a tooltip target, including zero Other failures")
	}
	if strings.Count(out, "<g ") != strings.Count(out, "</g>") {
		t.Fatal("SVG series and bar groups must close even for isolated points")
	}
	svgs := strings.Split(out, `<svg`)
	if len(svgs) != 3 {
		t.Fatal("expected one success chart and one Other strip")
	}
	ticks := regexp.MustCompile(`<text class="trend-axis-text trend-axis-x"[^>]*>[^<]+</text>`)
	mainTicks, stripTicks := ticks.FindAllString(svgs[1], -1), ticks.FindAllString(svgs[2], -1)
	x := regexp.MustCompile(`x="([0-9.]+)"`)
	for i := range mainTicks {
		if x.FindString(mainTicks[i]) != x.FindString(stripTicks[i]) {
			t.Fatal("success chart and Other strip must align on the same period centers")
		}
	}
	for _, svg := range svgs[1:] {
		if !strings.Contains(svg, ">100%</text>") || !strings.Contains(svg, ">0%</text>") {
			t.Fatal("success charts and Other strips must use the fixed percentage scale")
		}
	}
}

func TestComparisonOffsetsKeepMarkersWhiskersAndLinesAligned(t *testing.T) {
	series := []readmodeltrends.MetricSeries{
		{Label: "Raw", Color: "blue", Points: []float64{80, 80}, Defined: []bool{true, true}, Lower: []float64{60, 60}, Upper: []float64{90, 90}},
		{Label: "Filtered", Color: "green", Points: []float64{80, 80}, Defined: []bool{true, true}, Lower: []float64{60, 60}, Upper: []float64{90, 90}},
	}
	out := renderLineChartSVG(series, []string{"2026-03-16", "2026-03-23"}, readmodeltrends.ChartKindPercent)
	groups := strings.Split(out, `<g class="trend-series">`)
	if len(groups) != 3 {
		t.Fatalf("expected two series groups: %s", out)
	}
	for i, want := range []float64{55, 67} {
		group := groups[i+1]
		for _, snippet := range []string{
			fmt.Sprintf(`d="M %.2f `, want),
			fmt.Sprintf(`cx="%.2f"`, want),
			fmt.Sprintf(`points="%.2f,`, want),
		} {
			if !strings.Contains(group, snippet) {
				t.Fatalf("series %d missing aligned geometry %q", i, snippet)
			}
		}
	}
	if !strings.Contains(out, `x="61.00"`) || !strings.Contains(trendsCSS(), ".trend-series:focus-within .trend-ci") {
		t.Fatal("expected centered date label and keyboard highlight")
	}
}

func TestComparisonOffsetsAdaptToDenseHistory(t *testing.T) {
	for _, n := range []int{1, 365} {
		dates := make([]string, n)
		s := readmodeltrends.MetricSeries{Points: make([]float64, n), Defined: make([]bool, n), Lower: make([]float64, n), Upper: make([]float64, n)}
		for i := range dates {
			dates[i] = "date"
			s.Points[i], s.Lower[i], s.Upper[i], s.Defined[i] = 80, 60, 90, true
		}
		out := renderLineChartSVG([]readmodeltrends.MetricSeries{s, s}, dates, readmodeltrends.ChartKindPercent)
		if strings.Count(out, `class="trend-marker"`) != 2*n {
			t.Fatal("dense history must retain a tooltip target on every point")
		}
		if strings.Count(out, "<g ") != strings.Count(out, "</g>") {
			t.Fatal("SVG series groups must remain balanced")
		}
		matches := regexp.MustCompile(`class="trend-ci"[^>]*d="M ([0-9.]+) `).FindAllStringSubmatch(out, -1)
		if len(matches) != 2*n {
			t.Fatalf("expected %d intervals, got %d", 2*n, len(matches))
		}
		left, _ := strconv.ParseFloat(matches[0][1], 64)
		right, _ := strconv.ParseFloat(matches[n][1], 64)
		if left >= right || left < chartPadLeft || right > chartViewBoxWidth-chartPadRight {
			t.Fatalf("invalid offset for %d periods: %f, %f", n, left, right)
		}
		if n > 1 {
			nextLeft, _ := strconv.ParseFloat(matches[1][1], 64)
			if right >= nextLeft {
				t.Fatal("offset crossed into the next period")
			}
		}
	}
}

func TestChartAxisUsesCompactDatesAndPreservesTooltipYear(t *testing.T) {
	series := []readmodeltrends.MetricSeries{{
		Label: "Raw", Points: []float64{80, 90}, Defined: []bool{true, true},
		Lower: []float64{60, 70}, Upper: []float64{95, 99},
	}}
	out := renderLineChartSVG(series, []string{"2026-12-31", "2027-01-01"}, readmodeltrends.ChartKindPercent)
	labels := regexp.MustCompile(`<text class="trend-axis-text trend-axis-x"[^>]*>([^<]+)</text>`).FindAllStringSubmatch(out, -1)
	if len(labels) != 2 || labels[0][1] != "31/12" || labels[1][1] != "01/01" {
		t.Fatalf("unexpected chart date labels: %v", labels)
	}
	for _, date := range []string{"2026-12-31", "2027-01-01"} {
		if !strings.Contains(out, "<title>Raw "+date+":") {
			t.Fatalf("tooltip lost full date %s", date)
		}
	}
}

func TestMarkerHoverIncludesInterior(t *testing.T) {
	for _, caution := range []bool{false, true} {
		series := readmodeltrends.MetricSeries{
			Label: "Filtered", Color: "#059669", Points: []float64{80},
			Caution: []bool{caution}, Notes: []string{"n=5; low sample"},
		}
		out := renderMarker(series, 0, 100, 100)
		fill := "#059669"
		if caution {
			fill = "none"
		}
		for _, want := range []string{
			`pointer-events="all"`,
			`fill="` + fill + `"`,
			`<title>Filtered: 80.0%; n=5; low sample</title>`,
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("caution=%t: expected %q in %s", caution, want, out)
			}
		}
	}
}

func TestConfidenceWhiskersSkipUndefinedPoints(t *testing.T) {
	series := []readmodeltrends.MetricSeries{{
		Label: "Raw", Color: "#2563eb", Points: []float64{80, 0}, Defined: []bool{true, false},
		Lower: []float64{60, 0}, Upper: []float64{90, 0},
	}}
	out := renderLineChartSVG(series, []string{"2026-03-16", "2026-03-17"}, readmodeltrends.ChartKindPercent)
	if strings.Count(out, `class="trend-ci"`) != 1 || !strings.Contains(out, "95% confidence interval:") || strings.Contains(out, "95% CI") {
		t.Fatalf("wrong confidence interval rendering: %s", out)
	}
}
