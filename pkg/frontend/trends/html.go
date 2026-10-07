// Package trends renders the historical metric-evolution surface as HTML,
// drawing each stored daily metric as a self-contained inline-SVG line chart.
package trends

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	readmodeltrends "github.com/roivaz/ARO-HCP-CIHealth/pkg/frontend/readmodel/trends"
	frontui "github.com/roivaz/ARO-HCP-CIHealth/pkg/frontend/ui"
)

// PageOptions carries the chrome and request context for the trends page.
type PageOptions struct {
	Chrome frontui.ReportChromeOptions
	Query  readmodeltrends.TrendsQuery
}

const (
	chartViewBoxWidth  = 960
	chartViewBoxHeight = 300
	chartPadLeft       = 52
	chartPadRight      = 18
	chartPadTop        = 16
	chartPadBottom     = 42
	chartGridLines     = 4
	maxXAxisLabels     = 7
)

// RenderHTML renders the trends surface for the supplied read model.
func RenderHTML(data readmodeltrends.TrendsData, options PageOptions) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n")
	b.WriteString("<html lang=\"en\">\n")
	b.WriteString("<head>\n")
	b.WriteString("  <meta charset=\"utf-8\" />\n")
	b.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\" />\n")
	b.WriteString("  <title>CIHealth Trends</title>\n")
	b.WriteString(frontui.ThemeInitScriptTag())
	b.WriteString("  <style>\n")
	b.WriteString(trendsCSS())
	b.WriteString(frontui.StylesCSS())
	b.WriteString(frontui.ReportChromeCSS())
	b.WriteString(frontui.ThemeCSS())
	b.WriteString("  </style>\n")
	b.WriteString("</head>\n")
	b.WriteString("<body>\n")
	b.WriteString(frontui.ReportChromeHTML(options.Chrome))
	b.WriteString("<main class=\"page-content\">\n")
	if !data.Meta.HasData || len(data.Meta.Dates) == 0 {
		b.WriteString("  <p class=\"muted\">No stored daily metrics were found for the selected window.</p>\n")
		b.WriteString("</main>\n")
		b.WriteString(frontui.TooltipScriptTag())
		b.WriteString(frontui.ThemeToggleScriptTag())
		b.WriteString("</body>\n")
		b.WriteString("</html>\n")
		return b.String()
	}

	for _, environment := range data.Environments {
		b.WriteString(fmt.Sprintf(
			"  <section id=\"trends-%s\" class=\"section\">\n",
			html.EscapeString(strings.TrimSpace(environment.Environment)),
		))
		b.WriteString(fmt.Sprintf(
			"    <h2>Environment: %s %s</h2>\n",
			html.EscapeString(strings.ToUpper(strings.TrimSpace(environment.Environment))),
			frontui.HelpTooltipHTMLWithPlacement(
				"DEV e2e-parallel presubmits, including Tide batches. PR-regression-filtered means runs on the final commit of a PR that subsequently merged, plus batch retests of PRs that passed their individual checks. Each run counts once. This measures observed reliability, not test effectiveness. Individual-PR eligibility becomes known after merge, so recent filtered points can change. Batches qualify immediately.",
				"exec-heading-help", "start"),
		))
		if !environment.HasData || len(environment.Charts) == 0 {
			b.WriteString("    <p class=\"muted\">No stored metrics for this environment in the selected window.</p>\n")
			b.WriteString("  </section>\n")
			continue
		}
		for _, chart := range environment.Charts {
			b.WriteString(renderChart(chart))
		}
		b.WriteString("  </section>\n")
	}

	b.WriteString("</main>\n")
	b.WriteString(chartTooltipScript())
	b.WriteString(frontui.TooltipScriptTag())
	b.WriteString(frontui.ThemeToggleScriptTag())
	b.WriteString("</body>\n")
	b.WriteString("</html>\n")
	return b.String()
}

func renderChart(chart readmodeltrends.MetricChart) string {
	var b strings.Builder
	b.WriteString("    <div class=\"trend-chart-card\">\n")
	help := strings.TrimSpace(chart.Note) + " Hover or focus on points for counts and 95% Wilson confidence intervals. These describe sample-size uncertainty, not a guarantee; retests and shared incidents are correlated. Hollow markers indicate fewer than 30 eligible runs or a partial period. Missing or invalid samples remain gaps."
	if chart.Other == nil {
		help += " Whiskers show the intervals. Raw is offset left and filtered right of each date for readability."
	} else {
		help += " Series are slightly offset around each date for readability. Intervals are in tooltips only."
	}
	fmt.Fprintf(&b, "<h3 class=\"trend-chart-title\">%s %s</h3>\n",
		html.EscapeString(strings.TrimSpace(chart.Title)), frontui.HelpTooltipHTMLWithPlacement(help, "exec-heading-help", "start"))
	b.WriteString(renderLegend(chart.Series))
	b.WriteString(renderLineChartSVG(chart.Series, chart.XLabels, chart.Kind))
	if chart.Other != nil {
		fmt.Fprintf(&b, "<h4 class=\"trend-chart-title\">%s %s</h4>\n",
			html.EscapeString(chart.Other.Label),
			frontui.HelpTooltipHTMLWithPlacement("Other failures divided by runs in this chart's population; lower is better. Includes build failures, CI infrastructure failures, and unclassified failures, not a separate pipeline stage. The post-good + batches strip uses only that subset's failures and run count. Both strips use a 0–100% scale. Hollow bars mark low samples or partial periods; hover or focus for counts and confidence intervals.", "exec-heading-help", "start"))
		b.WriteString(renderChartSVG([]readmodeltrends.MetricSeries{*chart.Other}, chart.XLabels, readmodeltrends.ChartKindPercent, true))
	}
	b.WriteString("    </div>\n")
	return b.String()
}

func renderLegend(series []readmodeltrends.MetricSeries) string {
	var b strings.Builder
	b.WriteString("      <div class=\"trend-legend\">\n")
	for _, s := range series {
		b.WriteString(fmt.Sprintf(
			"        <span class=\"trend-legend-item\"><span class=\"trend-legend-swatch\" style=\"background:%s;\"></span>%s</span>\n",
			html.EscapeString(strings.TrimSpace(s.Color)),
			html.EscapeString(strings.TrimSpace(s.Label)),
		))
	}
	b.WriteString("      </div>\n")
	return b.String()
}

func renderLineChartSVG(series []readmodeltrends.MetricSeries, xLabels []string, kind readmodeltrends.ChartKind) string {
	return renderChartSVG(series, xLabels, kind, false)
}

func renderChartSVG(series []readmodeltrends.MetricSeries, xLabels []string, kind readmodeltrends.ChartKind, bars bool) string {
	var (
		axisMax float64
		step    float64
	)
	if kind == readmodeltrends.ChartKindPercent {
		axisMax, step = 100, 25
	} else {
		axisMax, step = niceAxis(seriesMax(series))
	}
	height := chartViewBoxHeight
	if bars {
		height = 150
		step = 50
	}
	plotWidth := float64(chartViewBoxWidth - chartPadLeft - chartPadRight)
	plotHeight := float64(height - chartPadTop - chartPadBottom)
	n := len(xLabels)
	dodged := kind == readmodeltrends.ChartKindPercent && len(series) > 1
	inset := 0.0
	if kind == readmodeltrends.ChartKindPercent {
		inset = 9
	}

	xFor := func(i int) float64 {
		if n <= 1 {
			return float64(chartPadLeft) + plotWidth/2
		}
		return float64(chartPadLeft) + inset + (plotWidth-2*inset)*float64(i)/float64(n-1)
	}
	offset := 0.0
	if dodged {
		offset = 6
		if n > 1 {
			offset = math.Min(offset, (plotWidth-2*inset)/float64(n-1)*0.18)
		}
	}
	yFor := func(v float64) float64 {
		if axisMax <= 0 {
			return float64(chartPadTop) + plotHeight
		}
		return float64(chartPadTop) + plotHeight*(1-v/axisMax)
	}
	formatY := func(v float64) string {
		if kind == readmodeltrends.ChartKindPercent {
			return fmt.Sprintf("%d%%", int64(math.Round(v)))
		}
		return formatAxisValue(v)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"      <svg class=\"trend-line-svg\" viewBox=\"0 0 %d %d\" preserveAspectRatio=\"xMidYMid meet\" role=\"img\" aria-label=\"Metric evolution over UTC periods; hover or focus on data points for exact values, counts and confidence intervals.\">\n",
		chartViewBoxWidth,
		height,
	))

	// Horizontal gridlines + y-axis value labels.
	for value := 0.0; value <= axisMax+1e-9; value += stepOrAxis(step, axisMax) {
		y := yFor(value)
		b.WriteString(fmt.Sprintf(
			"        <line class=\"trend-grid\" x1=\"%.2f\" y1=\"%.2f\" x2=\"%.2f\" y2=\"%.2f\" />\n",
			float64(chartPadLeft), y, float64(chartViewBoxWidth-chartPadRight), y,
		))
		b.WriteString(fmt.Sprintf(
			"        <text class=\"trend-axis-text trend-axis-y\" x=\"%.2f\" y=\"%.2f\">%s</text>\n",
			float64(chartPadLeft)-6, y+3, html.EscapeString(formatY(value)),
		))
		if step <= 0 {
			break
		}
	}

	// X-axis labels.
	for _, idx := range xLabelIndexes(n) {
		x := xFor(idx)
		anchor := "middle"
		if idx == 0 {
			anchor = "start"
		} else if idx == n-1 {
			anchor = "end"
		}
		label := xLabels[idx]
		if date, err := time.Parse(time.DateOnly, label); err == nil {
			label = date.Format("02/01")
		}
		b.WriteString(fmt.Sprintf(
			"        <text class=\"trend-axis-text trend-axis-x\" x=\"%.2f\" y=\"%d\" text-anchor=\"%s\">%s</text>\n",
			x, height-chartPadBottom+18, anchor, html.EscapeString(label),
		))
	}

	// Series polylines (broken at gaps) plus markers on defined points.
	for seriesIndex, s := range series {
		seriesX := func(i int) float64 {
			if !dodged {
				return xFor(i)
			}
			return xFor(i) + (2*float64(seriesIndex)/float64(len(series)-1)-1)*offset
		}
		if s.Emphasize {
			b.WriteString(`<g class="trend-series" data-emphasize="true">`)
		} else {
			b.WriteString(`<g class="trend-series">`)
		}
		if bars {
			width := 12.0
			if n > 1 {
				width = math.Min(width, (plotWidth-2*inset)/float64(n-1)*0.6)
			}
			for _, segment := range definedSegments(s) {
				for _, i := range segment {
					fill, dash := s.Color, ""
					if i < len(s.Caution) && s.Caution[i] {
						fill, dash = "none", ` stroke-dasharray="3 2"`
					}
					barHeight := math.Max(1, yFor(0)-yFor(s.Points[i]))
					fmt.Fprintf(&b, `<g class="trend-bar"><title>%s</title><rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s" stroke="%s"%s/><rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="transparent" pointer-events="all"/></g>`,
						html.EscapeString(pointTooltip(s, i)), seriesX(i)-width/2, yFor(0)-barHeight, width, barHeight,
						html.EscapeString(fill), html.EscapeString(s.Color), dash,
						seriesX(i)-width/2, yFor(0)-math.Max(8, barHeight), width, math.Max(8, barHeight))
				}
			}
			b.WriteString("</g>\n")
			continue
		}
		for i := range s.Points {
			if s.HideIntervals || i >= len(s.Lower) || i >= len(s.Upper) || i >= len(s.Defined) || !s.Defined[i] {
				continue
			}
			dash := ""
			note := ""
			if i < len(s.Caution) && s.Caution[i] {
				dash = ` stroke-dasharray="3 2"`
			}
			if i < len(s.Notes) {
				note = "; " + s.Notes[i]
			}
			fmt.Fprintf(&b, `<path class="trend-ci"%s d="M %.2f %.2f V %.2f M %.2f %.2f h 6 M %.2f %.2f h 6" stroke="%s"><title>%s: %.1f%% (95%% confidence interval: %.1f–%.1f%%)%s</title></path>`,
				dash,
				seriesX(i), yFor(s.Lower[i]), yFor(s.Upper[i]), seriesX(i)-3, yFor(s.Lower[i]), seriesX(i)-3, yFor(s.Upper[i]),
				html.EscapeString(s.Color), html.EscapeString(s.Label+" "+xLabels[i]), s.Points[i], s.Lower[i], s.Upper[i], html.EscapeString(note))
		}
		for _, segment := range definedSegments(s) {
			if len(segment) == 1 {
				i := segment[0]
				b.WriteString(renderMarker(s, i, seriesX(i), yFor(s.Points[i])))
				continue
			}
			points := make([]string, 0, len(segment))
			for _, i := range segment {
				points = append(points, fmt.Sprintf("%.2f,%.2f", seriesX(i), yFor(s.Points[i])))
			}
			b.WriteString(fmt.Sprintf(
				"        <polyline class=\"trend-line\" points=\"%s\" fill=\"none\" stroke=\"%s\" />\n",
				strings.Join(points, " "),
				html.EscapeString(strings.TrimSpace(s.Color)),
			))
			for _, i := range segment {
				b.WriteString(renderMarker(s, i, seriesX(i), yFor(s.Points[i])))
			}
		}
		b.WriteString("</g>\n")
	}

	b.WriteString("      </svg>\n")
	return b.String()
}

func renderMarker(s readmodeltrends.MetricSeries, i int, x, y float64) string {
	fill := s.Color
	if i < len(s.Caution) && s.Caution[i] {
		fill = "none"
	}
	return fmt.Sprintf(`<circle class="trend-marker" cx="%.2f" cy="%.2f" r="3" fill="%s" stroke="%s" pointer-events="all"><title>%s</title></circle>`,
		x, y, html.EscapeString(fill), html.EscapeString(s.Color), html.EscapeString(pointTooltip(s, i)))
}

func pointTooltip(s readmodeltrends.MetricSeries, i int) string {
	text := fmt.Sprintf("%s: %.1f%%", s.Label, s.Points[i])
	if i < len(s.Lower) && i < len(s.Upper) {
		text += fmt.Sprintf(" (95%% confidence interval: %.1f–%.1f%%)", s.Lower[i], s.Upper[i])
	}
	if i < len(s.Notes) {
		text += "; " + s.Notes[i]
	}
	return text
}

// definedSegments groups a series' point indexes into maximal runs of
// consecutive defined points, so lines break across gaps. A nil Defined slice
// means every point is defined.
func definedSegments(s readmodeltrends.MetricSeries) [][]int {
	segments := make([][]int, 0)
	current := make([]int, 0)
	for i := range s.Points {
		defined := s.Defined == nil
		if i < len(s.Defined) {
			defined = s.Defined[i]
		}
		if defined {
			current = append(current, i)
			continue
		}
		if len(current) > 0 {
			segments = append(segments, current)
			current = make([]int, 0)
		}
	}
	if len(current) > 0 {
		segments = append(segments, current)
	}
	return segments
}

// stepOrAxis guards against a zero step producing an infinite gridline loop.
func stepOrAxis(step float64, axisMax float64) float64 {
	if step > 0 {
		return step
	}
	if axisMax > 0 {
		return axisMax
	}
	return 1
}

func seriesMax(series []readmodeltrends.MetricSeries) float64 {
	maxValue := 0.0
	for _, s := range series {
		if s.Max > maxValue {
			maxValue = s.Max
		}
	}
	return maxValue
}

func xLabelIndexes(n int) []int {
	if n <= 0 {
		return nil
	}
	if n == 1 {
		return []int{0}
	}
	labels := maxXAxisLabels
	if n < labels {
		labels = n
	}
	seen := map[int]struct{}{}
	out := make([]int, 0, labels)
	for i := 0; i < labels; i++ {
		idx := int(math.Round(float64(i) * float64(n-1) / float64(labels-1)))
		if idx < 0 {
			idx = 0
		}
		if idx > n-1 {
			idx = n - 1
		}
		if _, dup := seen[idx]; dup {
			continue
		}
		seen[idx] = struct{}{}
		out = append(out, idx)
	}
	return out
}

// niceAxis returns a rounded-up axis maximum and gridline step for the supplied
// data maximum, aiming for roughly chartGridLines intervals.
func niceAxis(maxValue float64) (float64, float64) {
	if maxValue <= 0 {
		return 1, 1
	}
	step := niceNum(maxValue / float64(chartGridLines))
	if step <= 0 {
		step = 1
	}
	axisMax := math.Ceil(maxValue/step) * step
	if axisMax <= 0 {
		axisMax = step
	}
	return axisMax, step
}

func niceNum(x float64) float64 {
	if x <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(x))
	fraction := x / math.Pow(10, exp)
	var niceFraction float64
	switch {
	case fraction < 1.5:
		niceFraction = 1
	case fraction < 3:
		niceFraction = 2
	case fraction < 7:
		niceFraction = 5
	default:
		niceFraction = 10
	}
	return niceFraction * math.Pow(10, exp)
}

func formatAxisValue(value float64) string {
	if math.Abs(value-math.Round(value)) < 1e-9 {
		return fmt.Sprintf("%d", int64(math.Round(value)))
	}
	return fmt.Sprintf("%.1f", value)
}

func chartTooltipScript() string {
	return `<script>
(function () {
  var tooltip = document.createElement("div");
  tooltip.id = "trend-tooltip";
  tooltip.className = "trend-tooltip";
  tooltip.setAttribute("role", "tooltip");
  tooltip.hidden = true;
  document.body.appendChild(tooltip);
  var active = null;

  function hide() {
    tooltip.hidden = true;
    if (active) active.removeAttribute("aria-describedby");
    active = null;
  }
  function position(x, y) {
    var gap = 12;
    var width = tooltip.offsetWidth;
    var height = tooltip.offsetHeight;
    tooltip.style.left = Math.max(8, Math.min(x + gap, window.innerWidth - width - 8)) + "px";
    var top = y + gap;
    if (top + height > window.innerHeight - 8) top = y - height - gap;
    tooltip.style.top = Math.max(8, top) + "px";
  }
  function show(target, text, x, y) {
    hide();
    active = target;
    tooltip.textContent = text;
    tooltip.hidden = false;
    active.setAttribute("aria-describedby", tooltip.id);
    position(x, y);
  }
  document.querySelectorAll(".trend-marker, .trend-ci, .trend-bar").forEach(function (target) {
    var title = target.querySelector("title");
    if (!title) return;
    var text = title.textContent;
    title.remove();
    target.setAttribute("tabindex", "0");
    target.setAttribute("aria-label", text);
    target.addEventListener("pointerenter", function (event) {
      show(target, text, event.clientX, event.clientY);
    });
    target.addEventListener("pointermove", function (event) {
      if (active === target) position(event.clientX, event.clientY);
    });
    target.addEventListener("pointerleave", hide);
    target.addEventListener("focus", function () {
      var rect = target.getBoundingClientRect();
      show(target, text, rect.right, rect.top);
    });
    target.addEventListener("blur", hide);
  });
  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape") hide();
  });
  window.addEventListener("scroll", hide, true);
  window.addEventListener("resize", hide);
})();
</script>
`
}

func trendsCSS() string {
	return strings.Join([]string{
		"    body { font-family: Arial, sans-serif; margin: 0; color: #1f2937; }",
		"    .page-content { padding: 16px 20px 32px; }",
		"    h2 { margin-top: 22px; margin-bottom: 8px; }",
		"    .muted { color: #6b7280; }",
		"    .section { border: 1px solid #e5e7eb; border-radius: 8px; padding: 12px 14px; margin: 14px 0; }",
		"    .trend-chart-card { margin: 8px 0 18px; }",
		"    .trend-chart-title { margin: 4px 0 6px; font-size: 14px; }",
		"    .trend-legend { display: flex; flex-wrap: wrap; gap: 10px 16px; margin: 4px 0 8px; font-size: 12px; color: #374151; }",
		"    .trend-legend-item { display: inline-flex; align-items: center; gap: 6px; }",
		"    .trend-legend-swatch { width: 12px; height: 12px; border-radius: 3px; display: inline-block; }",
		"    .trend-line-svg { width: 100%; height: auto; max-width: 100%; display: block; }",
		"    .trend-line { stroke-width: 2; vector-effect: non-scaling-stroke; }",
		"    .trend-series[data-emphasize] .trend-line { stroke-width: 3.5; }",
		"    .trend-bar rect { vector-effect: non-scaling-stroke; }",
		"    .trend-ci { stroke-width: 1; opacity: .4; vector-effect: non-scaling-stroke; }",
		"    .trend-series:hover .trend-ci, .trend-series:focus-within .trend-ci { opacity: 1; stroke-width: 2; }",
		"    .trend-tooltip { position: fixed; z-index: 100; pointer-events: none; max-width: min(360px, calc(100vw - 32px)); padding: 8px 10px; border: 1px solid #60a5fa; border-radius: 8px; background: #dbeafe; color: #172554; font-size: 12px; line-height: 1.45; overflow-wrap: anywhere; box-shadow: 0 12px 30px rgba(15, 23, 42, .18); }",
		"    :root[data-theme=\"dark\"] .trend-tooltip { background: #172554; color: #dbeafe; }",
		"    .trend-grid { stroke: #e5e7eb; stroke-width: 1; }",
		"    .trend-axis-text { fill: #6b7280; font-size: 11px; }",
		"    .trend-axis-y { text-anchor: end; }",
		"    :root[data-theme=\"dark\"] body { color: #e2e8f0; }",
		"    :root[data-theme=\"dark\"] .muted { color: #94a3b8; }",
		"    :root[data-theme=\"dark\"] .section { background: #111827; border-color: #334155; }",
		"    :root[data-theme=\"dark\"] .trend-legend { color: #cbd5e1; }",
		"    :root[data-theme=\"dark\"] .trend-grid { stroke: #334155; }",
		"    :root[data-theme=\"dark\"] .trend-axis-text { fill: #94a3b8; }",
	}, "\n") + "\n"
}
