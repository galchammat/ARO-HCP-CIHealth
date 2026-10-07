// Package trends builds the DEV presubmit reliability comparison from daily metrics.
package trends

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	readmodelwindow "github.com/roivaz/ARO-HCP-CIHealth/pkg/frontend/readmodel/window"
	storecontracts "github.com/roivaz/ARO-HCP-CIHealth/pkg/store/contracts"
)

type StoreOpener interface {
	OpenStore() (storecontracts.Store, error)
}

const (
	TrendsModeRolling  = "rolling"
	TrendsModeRelative = "relative"
	TrendsModeWeekly   = "weekly"
	TrendsModeSprint   = "sprint"
	TrendsModeAll      = "all"
	Daily              = "daily"
	Weekly             = "weekly"
	LowSampleSize      = 30
)

type TrendsQuery struct {
	Mode, StartDate, EndDate, Granularity string
	Days                                  string
	Environments                          []string
	GeneratedAt                           time.Time
}

func (query TrendsQuery) WithDefaults() TrendsQuery {
	if strings.TrimSpace(query.Granularity) == "" {
		query.Granularity = Daily
	}
	if strings.TrimSpace(query.Mode) == "" && strings.TrimSpace(query.StartDate) == "" &&
		strings.TrimSpace(query.EndDate) == "" && strings.TrimSpace(query.Days) == "" {
		query.Mode, query.Days = TrendsModeRelative, "30"
	}
	return query
}

type TrendsData struct {
	Meta         TrendsMeta          `json:"meta"`
	Environments []EnvironmentTrends `json:"environments"`
	Buckets      []Bucket            `json:"buckets"`
}

type TrendsMeta struct {
	StartDate    string   `json:"start_date"`
	EndDate      string   `json:"end_date"`
	Dates        []string `json:"dates"`
	Environments []string `json:"environments"`
	GeneratedAt  string   `json:"generated_at"`
	Granularity  string   `json:"granularity"`
	HasData      bool     `json:"has_data"`
	RelativeDays int      `json:"relative_days,omitempty"`
}

func RelativeDayOptions() []int {
	return []int{1, 2, 5, 7, 14, 30, 90}
}

func relativeDays(mode, value string) (int, error) {
	mode = NormalizeTrendsMode(mode)
	value = strings.TrimSpace(value)
	if mode == TrendsModeRolling && value == "" {
		return 7, nil
	}
	if mode != TrendsModeRelative {
		if value != "" {
			return 0, fmt.Errorf("days requires mode=relative")
		}
		return 0, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || !slices.Contains(RelativeDayOptions(), days) {
		return 0, fmt.Errorf("relative days must be one of 1, 2, 5, 7, 14, 30, 90")
	}
	return days, nil
}

type EnvironmentTrends struct {
	Environment string        `json:"environment"`
	HasData     bool          `json:"has_data"`
	Charts      []MetricChart `json:"charts"`
}

type ChartKind string

const (
	ChartKindCount   ChartKind = "count"
	ChartKindPercent ChartKind = "percent"
)

type MetricChart struct {
	Title   string         `json:"title"`
	Kind    ChartKind      `json:"kind"`
	Note    string         `json:"note,omitempty"`
	XLabels []string       `json:"x_labels"`
	Series  []MetricSeries `json:"series"`
	Other   *MetricSeries  `json:"other,omitempty"`
}

type MetricSeries struct {
	Metric        string    `json:"metric"`
	Label         string    `json:"label"`
	Color         string    `json:"color"`
	Points        []float64 `json:"points"`
	Defined       []bool    `json:"defined,omitempty"`
	Lower         []float64 `json:"lower,omitempty"`
	Upper         []float64 `json:"upper,omitempty"`
	Notes         []string  `json:"notes,omitempty"`
	Caution       []bool    `json:"caution,omitempty"`
	Total         float64   `json:"total"`
	Max           float64   `json:"max"`
	HideIntervals bool      `json:"hide_intervals,omitempty"`
	Emphasize     bool      `json:"emphasize,omitempty"`
}

type Outcomes struct {
	Runs         int     `json:"runs"`
	Failed       int     `json:"failed"`
	Provision    int     `json:"provision"`
	E2E          int     `json:"e2e"`
	Other        int     `json:"other"`
	Defined      bool    `json:"defined"`
	LanesDefined bool    `json:"lanes_defined"`
	Rate         float64 `json:"rate"`
	Lower        float64 `json:"lower"`
	Upper        float64 `json:"upper"`
	LowSample    bool    `json:"low_sample"`
}

type Bucket struct {
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
	Partial   bool     `json:"partial"`
	Raw       Outcomes `json:"raw"`
	Filtered  Outcomes `json:"filtered"`
	Coverage  *float64 `json:"coverage,omitempty"`
}

func BuildTrends(ctx context.Context, service StoreOpener, query TrendsQuery) (TrendsData, error) {
	query = query.WithDefaults()
	if service == nil {
		return TrendsData{}, fmt.Errorf("service is required")
	}
	for _, env := range query.Environments {
		if strings.TrimSpace(env) != "" && !strings.EqualFold(strings.TrimSpace(env), "dev") {
			return TrendsData{}, fmt.Errorf("trends supports DEV presubmits only")
		}
	}
	granularity := strings.ToLower(strings.TrimSpace(query.Granularity))
	if granularity != Daily && granularity != Weekly {
		return TrendsData{}, fmt.Errorf("granularity must be daily or weekly")
	}
	now := query.GeneratedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	days, err := relativeDays(query.Mode, query.Days)
	if err != nil {
		return TrendsData{}, err
	}
	start, end, err := resolveTrendsWindow(query.Mode, query.StartDate, query.EndDate, days, now)
	if err != nil {
		return TrendsData{}, err
	}
	store, err := service.OpenStore()
	if err != nil {
		return TrendsData{}, err
	}
	defer store.Close()
	dates, err := store.ListMetricDates(ctx)
	if err != nil {
		return TrendsData{}, fmt.Errorf("list metric dates: %w", err)
	}
	selected := make([]string, 0, len(dates))
	for _, date := range dates {
		if (start == "" || date >= start) && (end == "" || date <= end) {
			selected = append(selected, date)
		}
	}
	rows, err := store.ListMetricsDailyForDates(ctx, []string{"dev"}, selected)
	if err != nil {
		return TrendsData{}, fmt.Errorf("list DEV metrics: %w", err)
	}
	byDate := map[string]map[string]float64{}
	first, last := "", ""
	for _, row := range rows {
		if row.Environment != "dev" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, row.Date); err != nil {
			return TrendsData{}, fmt.Errorf("invalid metric date %q: %w", row.Date, err)
		}
		if byDate[row.Date] == nil {
			byDate[row.Date] = map[string]float64{}
		}
		byDate[row.Date][row.Metric] = row.Value
		if first == "" || row.Date < first {
			first = row.Date
		}
		if row.Date > last {
			last = row.Date
		}
	}
	data := TrendsData{
		Meta: TrendsMeta{StartDate: start, EndDate: end, Granularity: granularity, RelativeDays: days,
			Environments: []string{"dev"}, GeneratedAt: now.Format(time.RFC3339), Dates: []string{}},
		Environments: []EnvironmentTrends{{Environment: "dev"}},
		Buckets:      []Bucket{},
	}
	if first == "" {
		return data, nil
	}
	if start == "" {
		start, end = first, last
	}
	begin, _ := time.Parse(time.DateOnly, start)
	finish, _ := time.Parse(time.DateOnly, end)
	axisStart := begin
	step := 1
	if granularity == Weekly {
		axisStart = readmodelwindow.WeekStartForDate(begin)
		step = 7
	}
	for date := begin; !date.After(finish); date = date.AddDate(0, 0, 1) {
		if byDate[date.Format(time.DateOnly)] != nil {
			data.Meta.Dates = append(data.Meta.Dates, date.Format(time.DateOnly))
		}
	}
	for date := axisStart; !date.After(finish); date = date.AddDate(0, 0, step) {
		until := date.AddDate(0, 0, step)
		bucket := Bucket{StartDate: date.Format(time.DateOnly), EndDate: until.AddDate(0, 0, -1).Format(time.DateOnly),
			Partial: date.Before(begin) || until.After(finish.AddDate(0, 0, 1)) || until.After(now)}
		rawComplete, filteredComplete, lanesComplete := true, true, true
		hasRows := false
		for day := date; day.Before(until); day = day.AddDate(0, 0, 1) {
			if day.Before(begin) || day.After(finish) || day.After(now) {
				continue
			}
			metrics := byDate[day.Format(time.DateOnly)]
			if len(metrics) == 0 {
				continue
			}
			hasRows = true
			raw, rawOK := counts(metrics, "run_count", "failure_count")
			lanes, lanesOK := counts(metrics, "failed_provision_run_count", "failed_e2e_run_count", "failed_ci_infra_run_count")
			filtered, filteredOK := counts(metrics, "post_good_run_count", "post_good_failed_provision_run_count", "post_good_failed_e2e_jobs", "post_good_failed_ci_infra_run_count")
			rawComplete = rawComplete && rawOK && raw[1] <= raw[0]
			lanesComplete = lanesComplete && lanesOK && lanes[0]+lanes[1]+lanes[2] == raw[1]
			filteredFailed := filtered[1] + filtered[2] + filtered[3]
			filteredComplete = filteredComplete && filteredOK && filteredFailed <= filtered[0] && rawOK && filtered[0] <= raw[0] && filteredFailed <= raw[1]
			bucket.Raw.Runs += raw[0]
			bucket.Raw.Failed += raw[1]
			bucket.Raw.Provision += lanes[0]
			bucket.Raw.E2E += lanes[1]
			bucket.Raw.Other += lanes[2]
			bucket.Filtered.Runs += filtered[0]
			bucket.Filtered.Failed += filteredFailed
			bucket.Filtered.Provision += filtered[1]
			bucket.Filtered.E2E += filtered[2]
			bucket.Filtered.Other += filtered[3]
		}
		bucket.Raw.Defined = hasRows && rawComplete && bucket.Raw.Runs > 0
		bucket.Raw.LanesDefined = hasRows && lanesComplete
		bucket.Filtered.Defined = hasRows && filteredComplete && bucket.Filtered.Runs > 0
		bucket.Filtered.LanesDefined = hasRows && filteredComplete
		finishOutcomes(&bucket.Raw)
		finishOutcomes(&bucket.Filtered)
		if bucket.Raw.Defined && hasRows && filteredComplete {
			coverage := 100 * float64(bucket.Filtered.Runs) / float64(bucket.Raw.Runs)
			bucket.Coverage = &coverage
		}
		data.Buckets = append(data.Buckets, bucket)
	}
	data.Meta.HasData = true
	data.Environments[0].HasData = true
	data.Environments[0].Charts = []MetricChart{
		comparisonChart(data.Buckets),
		diagnosticChart(data.Buckets, false),
		diagnosticChart(data.Buckets, true),
	}
	return data, nil
}

// Missing or inconsistent facts must not become apparent successes.
func counts(metrics map[string]float64, keys ...string) ([]int, bool) {
	out := make([]int, len(keys))
	valid := true
	for i, key := range keys {
		v, ok := metrics[key]
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v != math.Trunc(v) || v >= float64(math.MaxInt) {
			valid = false
			continue
		}
		out[i] = int(v)
	}
	return out, valid
}

func finishOutcomes(out *Outcomes) {
	if !out.Defined {
		return
	}
	n := float64(out.Runs)
	p := float64(out.Runs-out.Failed) / n
	const z = 1.959963984540054
	denom := 1 + z*z/n
	center := (p + z*z/(2*n)) / denom
	radius := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denom
	out.Rate = p * 100
	out.Lower = math.Max(0, (center-radius)*100)
	out.Upper = math.Min(100, (center+radius)*100)
	out.LowSample = out.Runs < LowSampleSize
}

func comparisonChart(buckets []Bucket) MetricChart {
	chart := MetricChart{Title: "Presubmit overall job success", Kind: ChartKindPercent,
		Note: "Raw includes all DEV e2e-parallel runs, including Tide batches. Filtered includes post-good runs plus batches, counted once.",
		Series: []MetricSeries{
			{Metric: "raw_success_rate", Label: "Raw success rate", Color: "#2563eb", Max: 100},
			{Metric: "filtered_success_rate", Label: "PR-regression-filtered success rate", Color: "#059669", Max: 100},
		},
	}
	for _, bucket := range buckets {
		chart.XLabels = append(chart.XLabels, bucket.StartDate)
		for i, outcome := range []Outcomes{bucket.Raw, bucket.Filtered} {
			s := &chart.Series[i]
			appendRatePoint(s, bucket, outcome.Runs-outcome.Failed, outcome.Runs, outcome.Defined)
			note := failureCountsNote(outcome)
			if bucket.Coverage != nil {
				note += fmt.Sprintf("; filtered share %.1f%%", *bucket.Coverage)
			}
			if bucket.Raw.Defined && bucket.Filtered.Defined {
				note += fmt.Sprintf("; filtered minus raw %+.1f pp", bucket.Filtered.Rate-bucket.Raw.Rate)
			}
			s.Notes[len(s.Notes)-1] += note
		}
	}
	return chart
}

func diagnosticChart(buckets []Bucket, filtered bool) MetricChart {
	title := "All runs"
	otherPopulation := "all runs"
	if filtered {
		title = "Post-good + batches"
		otherPopulation = "post-good + batch runs"
	}
	chart := MetricChart{
		Title: title, Kind: ChartKindPercent,
		Note: "Provision success excludes Other failures and counts runs that passed provisioning even if E2E later failed. E2E success excludes provision and Other failures. Overall success includes all runs. These rates have different denominators and are not additive. Weekly rates use summed counts; weeks start Monday UTC.",
		Series: []MetricSeries{
			{Metric: "provision_success_rate", Label: "Provision success", Color: "#d97706", Max: 100, HideIntervals: true},
			{Metric: "e2e_success_rate", Label: "E2E success", Color: "#7c3aed", Max: 100, HideIntervals: true},
			{Metric: "overall_success_rate", Label: "Overall success", Color: "#2563eb", Max: 100, HideIntervals: true, Emphasize: true},
		},
		Other: &MetricSeries{Metric: "other_failure_rate", Label: "Other failures (% of " + otherPopulation + ")", Color: "#64748b", Max: 100, HideIntervals: true},
	}
	for _, bucket := range buckets {
		out := bucket.Raw
		if filtered {
			out = bucket.Filtered
		}
		chart.XLabels = append(chart.XLabels, bucket.StartDate)
		successful := out.Runs - out.Failed
		valid := out.Defined && out.LanesDefined
		appendRatePoint(&chart.Series[0], bucket, successful+out.E2E, successful+out.E2E+out.Provision, valid)
		appendRatePoint(&chart.Series[1], bucket, successful, successful+out.E2E, valid)
		appendRatePoint(&chart.Series[2], bucket, successful, out.Runs, out.Defined)
		chart.Series[2].Notes[len(chart.Series[2].Notes)-1] += failureCountsNote(out)
		appendRatePoint(chart.Other, bucket, out.Other, out.Runs, valid)
	}
	return chart
}

func appendRatePoint(series *MetricSeries, bucket Bucket, count, total int, valid bool) {
	out := Outcomes{Runs: total, Failed: total - count, Defined: valid && total > 0 && count >= 0 && count <= total}
	finishOutcomes(&out)
	series.Points = append(series.Points, out.Rate)
	series.Defined = append(series.Defined, out.Defined)
	series.Lower = append(series.Lower, out.Lower)
	series.Upper = append(series.Upper, out.Upper)
	note := bucket.StartDate
	if bucket.EndDate != bucket.StartDate {
		note += " to " + bucket.EndDate
	}
	note += fmt.Sprintf(" UTC; %d/%d runs; n=%d", count, total, total)
	if out.LowSample {
		note += "; low sample (<30 runs)"
	}
	if bucket.Partial {
		note += "; partial period (clipped or unfinished)"
	}
	series.Notes = append(series.Notes, note)
	series.Caution = append(series.Caution, out.LowSample || bucket.Partial)
}

func failureCountsNote(out Outcomes) string {
	if !out.LanesDefined {
		return "; failure lanes unavailable"
	}
	return fmt.Sprintf("; failures: provision %d / E2E %d / other %d", out.Provision, out.E2E, out.Other)
}

func resolveTrendsWindow(mode, start, end string, days int, now time.Time) (string, string, error) {
	switch NormalizeTrendsMode(mode) {
	case TrendsModeRolling, TrendsModeRelative:
		return now.AddDate(0, 0, 1-days).Format(time.DateOnly), now.Format(time.DateOnly), nil
	case TrendsModeWeekly:
		begin := readmodelwindow.WeekStartForDate(now)
		return begin.Format(time.DateOnly), begin.AddDate(0, 0, 6).Format(time.DateOnly), nil
	case TrendsModeSprint:
		begin, finish := readmodelwindow.SprintWindowForDate(now)
		return begin.Format(time.DateOnly), finish.Format(time.DateOnly), nil
	case TrendsModeAll:
		return "", "", nil
	}
	start, end = strings.TrimSpace(start), strings.TrimSpace(end)
	if start == "" && end == "" {
		return "", "", nil
	}
	if _, err := time.Parse(time.DateOnly, start); err != nil {
		return "", "", fmt.Errorf("start_date must be YYYY-MM-DD")
	}
	if _, err := time.Parse(time.DateOnly, end); err != nil {
		return "", "", fmt.Errorf("end_date must be YYYY-MM-DD")
	}
	if end < start {
		return "", "", fmt.Errorf("end_date must not be before start_date")
	}
	return start, end, nil
}

func NormalizeTrendsMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case TrendsModeRelative:
		return TrendsModeRelative
	case TrendsModeRolling:
		return TrendsModeRolling
	case TrendsModeWeekly:
		return TrendsModeWeekly
	case TrendsModeSprint:
		return TrendsModeSprint
	case TrendsModeAll:
		return TrendsModeAll
	default:
		return ""
	}
}
