package trends

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	storecontracts "github.com/roivaz/ARO-HCP-CIHealth/pkg/store/contracts"
)

type fakeStore struct {
	storecontracts.Store
	rows []storecontracts.MetricDailyRecord
	err  error
}

func (f *fakeStore) ListMetricDates(context.Context) ([]string, error) {
	var dates []string
	seen := map[string]bool{}
	for _, row := range f.rows {
		if !seen[row.Date] {
			dates = append(dates, row.Date)
			seen[row.Date] = true
		}
	}
	return dates, f.err
}

func (f *fakeStore) ListMetricsDailyForDates(_ context.Context, envs, dates []string) ([]storecontracts.MetricDailyRecord, error) {
	var out []storecontracts.MetricDailyRecord
	for _, row := range f.rows {
		for _, date := range dates {
			if row.Date == date && row.Environment == envs[0] {
				out = append(out, row)
			}
		}
	}
	return out, f.err
}

func (f *fakeStore) Close() error                             { return nil }
func (f *fakeStore) OpenStore() (storecontracts.Store, error) { return f, nil }

func dailyRows(date string, runs, failures, filtered, provision, e2e, other int) []storecontracts.MetricDailyRecord {
	metrics := map[string]int{
		"run_count": runs, "failure_count": failures,
		"failed_provision_run_count": failures, "failed_e2e_run_count": 0, "failed_ci_infra_run_count": 0,
		"post_good_run_count": filtered, "post_good_failed_provision_run_count": provision,
		"post_good_failed_e2e_jobs": e2e, "post_good_failed_ci_infra_run_count": other,
	}
	rows := make([]storecontracts.MetricDailyRecord, 0, len(metrics))
	for key, value := range metrics {
		rows = append(rows, storecontracts.MetricDailyRecord{Environment: "dev", Date: date, Metric: key, Value: float64(value)})
	}
	return rows
}

func build(t *testing.T, rows []storecontracts.MetricDailyRecord, query TrendsQuery) TrendsData {
	t.Helper()
	if query.GeneratedAt.IsZero() {
		query.GeneratedAt = time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	}
	data, err := BuildTrends(context.Background(), &fakeStore{rows: rows}, query)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func closeTo(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > .0001 {
		t.Fatalf("got %f, want %f", got, want)
	}
}

func TestWeightedWeeklyComparisonAndDailyToggle(t *testing.T) {
	rows := append(dailyRows("2026-03-16", 10, 10, 5, 1, 1, 0), dailyRows("2026-03-17", 90, 0, 45, 0, 0, 0)...)
	weekly := build(t, rows, TrendsQuery{})
	if weekly.Meta.Granularity != Weekly || len(weekly.Buckets) != 1 {
		t.Fatalf("unexpected weekly response: %+v", weekly)
	}
	bucket := weekly.Buckets[0]
	closeTo(t, bucket.Raw.Rate, 90)
	closeTo(t, bucket.Filtered.Rate, 96)
	closeTo(t, *bucket.Coverage, 50)
	if bucket.Raw.Runs != 100 || bucket.Filtered.Runs != 50 || bucket.Filtered.LowSample {
		t.Fatalf("unexpected counts: %+v", bucket)
	}
	if bucket.Filtered.Provision != 1 || bucket.Filtered.E2E != 1 || bucket.Filtered.Other != 0 {
		t.Fatalf("missing failure lanes: %+v", bucket.Filtered)
	}
	if len(weekly.Environments) != 1 || weekly.Environments[0].Environment != "dev" {
		t.Fatal("comparison must only include DEV")
	}
	chart := weekly.Environments[0].Charts[0]
	if len(chart.Series) != 2 || chart.Series[0].Metric != "raw_success_rate" || chart.Series[1].Metric != "filtered_success_rate" {
		t.Fatalf("missing paired rates: %+v", chart)
	}
	daily := build(t, rows, TrendsQuery{Granularity: Daily})
	if len(daily.Buckets) != 2 {
		t.Fatalf("expected daily points: %+v", daily.Buckets)
	}
	closeTo(t, daily.Buckets[0].Raw.Rate, 0)
	closeTo(t, daily.Buckets[1].Raw.Rate, 100)
	if !daily.Buckets[0].Filtered.LowSample || daily.Buckets[1].Filtered.LowSample {
		t.Fatal("low-sample classification incorrect")
	}
}

func TestContinuousAxisGapsAndPartialWindows(t *testing.T) {
	rows := append(dailyRows("2026-03-16", 10, 3, 5, 1, 0, 0), dailyRows("2026-03-30", 20, 5, 8, 0, 1, 0)...)
	data := build(t, rows, TrendsQuery{})
	if len(data.Buckets) != 3 || data.Buckets[1].Raw.Defined || data.Buckets[1].Filtered.Defined {
		t.Fatalf("empty week must be a gap: %+v", data.Buckets)
	}
	daily := build(t, rows, TrendsQuery{StartDate: "2026-03-15", EndDate: "2026-03-18", Granularity: Daily})
	if len(daily.Buckets) != 4 || daily.Buckets[0].Raw.Defined || daily.Buckets[2].Raw.Defined {
		t.Fatal("daily axis must include requested empty dates")
	}
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	current := build(t, rows, TrendsQuery{Mode: TrendsModeWeekly, GeneratedAt: now})
	if !current.Buckets[0].Partial {
		t.Fatal("current week should be partial")
	}
	complete := build(t, rows, TrendsQuery{StartDate: "2026-03-16", EndDate: "2026-03-22"})
	if complete.Buckets[0].Partial {
		t.Fatal("completed requested week should not be partial")
	}
	clipped := build(t, rows, TrendsQuery{StartDate: "2026-03-17", EndDate: "2026-03-30"})
	if len(clipped.Buckets) != 3 || !clipped.Buckets[0].Partial || !clipped.Buckets[2].Partial {
		t.Fatal("clipped boundary weeks should be partial")
	}
}

func TestUnknownMetricsAreNotSuccess(t *testing.T) {
	for _, key := range []string{"run_count", "failure_count", "post_good_run_count", "post_good_failed_e2e_jobs"} {
		t.Run(key, func(t *testing.T) {
			rows := dailyRows("2026-03-16", 10, 2, 5, 1, 0, 0)
			for i, row := range rows {
				if row.Metric == key {
					rows = append(rows[:i], rows[i+1:]...)
					break
				}
			}
			data := build(t, rows, TrendsQuery{})
			if data.Buckets[0].Filtered.Defined {
				t.Fatal("missing counts should produce a filtered gap")
			}
			if !strings.HasPrefix(key, "post_good") && data.Buckets[0].Raw.Defined {
				t.Fatal("missing raw counts should produce a raw gap")
			}
		})
	}
	invalid := build(t, dailyRows("2026-03-16", 10, 2, 3, 4, 0, 0), TrendsQuery{})
	if invalid.Buckets[0].Filtered.Defined {
		t.Fatal("failures > runs must not silently inflate denominator")
	}
	zero := build(t, dailyRows("2026-03-16", 10, 2, 0, 0, 0, 0), TrendsQuery{})
	if zero.Buckets[0].Filtered.Defined || zero.Buckets[0].Coverage == nil || *zero.Buckets[0].Coverage != 0 {
		t.Fatal("zero filtered samples must be a rate gap with zero coverage")
	}
}

func TestWilsonIntervalsAndSampleBoundary(t *testing.T) {
	out := Outcomes{Runs: 10, Failed: 0, Defined: true}
	finishOutcomes(&out)
	closeTo(t, out.Lower, 72.24672)
	closeTo(t, out.Upper, 100)
	out = Outcomes{Runs: 10, Failed: 10, Defined: true}
	finishOutcomes(&out)
	closeTo(t, out.Lower, 0)
	closeTo(t, out.Upper, 27.75328)
	for _, n := range []int{29, 30} {
		out = Outcomes{Runs: n, Defined: true}
		finishOutcomes(&out)
		if out.LowSample != (n < 30) {
			t.Fatalf("wrong low sample boundary for %d", n)
		}
	}
}

func TestDiagnosticRatesUseStageDenominators(t *testing.T) {
	rows := dailyRows("2026-03-16", 100, 40, 50, 5, 5, 5)
	for i := range rows {
		switch rows[i].Metric {
		case "failed_provision_run_count", "failed_ci_infra_run_count":
			rows[i].Value = 10
		case "failed_e2e_run_count":
			rows[i].Value = 20
		}
	}
	data := build(t, rows, TrendsQuery{Granularity: Daily})
	charts := data.Environments[0].Charts
	if len(charts) != 3 {
		t.Fatalf("expected comparison plus two diagnostics, got %d", len(charts))
	}
	for population, want := range [][]float64{{100 * 80.0 / 90, 75, 60, 10}, {100 * 40.0 / 45, 87.5, 70, 10}} {
		chart := charts[population+1]
		for i, series := range chart.Series {
			closeTo(t, series.Points[0], want[i])
			if !series.Defined[0] || !series.HideIntervals || series.Color != charts[1].Series[i].Color {
				t.Fatalf("incorrect diagnostic series configuration: %+v", series)
			}
		}
		closeTo(t, chart.Other.Points[0], want[3])
		if !chart.Series[2].Emphasize || !chart.Other.Defined[0] {
			t.Fatal("overall should be emphasized and Other should be defined")
		}
	}
	for _, want := range []string{"35/50 runs", "2026-03-16 UTC", "filtered share 50.0%", "filtered minus raw +10.0 pp", "provision 5 / E2E 5 / other 5"} {
		if !strings.Contains(charts[0].Series[1].Notes[0], want) {
			t.Fatalf("comparison point lost table context %q: %s", want, charts[0].Series[1].Notes[0])
		}
	}
}

func TestDiagnosticWeeklyRatesUseSummedCounts(t *testing.T) {
	rows := append(dailyRows("2026-03-16", 10, 4, 5, 1, 1, 0), dailyRows("2026-03-17", 90, 20, 45, 4, 4, 5)...)
	data := build(t, rows, TrendsQuery{})
	chart := data.Environments[0].Charts[2]
	for i, want := range []float64{100 * 40.0 / 45, 87.5, 70} {
		closeTo(t, chart.Series[i].Points[0], want)
	}
	closeTo(t, chart.Other.Points[0], 10)
	if !strings.Contains(chart.Series[0].Notes[0], "40/45 runs") {
		t.Fatal("provision tooltip must report the summed eligible denominator")
	}
}

func TestOtherFailureStripsUseDistinctPopulations(t *testing.T) {
	rows := dailyRows("2026-03-16", 100, 30, 20, 1, 2, 3)
	for i := range rows {
		switch rows[i].Metric {
		case "failed_provision_run_count", "failed_e2e_run_count", "failed_ci_infra_run_count":
			rows[i].Value = 10
		}
	}
	charts := build(t, rows, TrendsQuery{}).Environments[0].Charts
	if charts[1].Title != "All runs" || charts[2].Title != "Post-good + batches" {
		t.Fatal("diagnostic titles must identify their populations")
	}
	for i, tc := range []struct {
		label, counts string
		rate          float64
	}{
		{"Other failures (% of all runs)", "10/100 runs", 10},
		{"Other failures (% of post-good + batch runs)", "3/20 runs", 15},
	} {
		other := charts[i+1].Other
		if other.Label != tc.label || !strings.Contains(other.Notes[0], tc.counts) {
			t.Fatalf("Other strip lost its population or counts: %+v", other)
		}
		closeTo(t, other.Points[0], tc.rate)
	}
}

func TestDiagnosticMissingLanesPreserveOverall(t *testing.T) {
	rows := dailyRows("2026-03-16", 100, 20, 50, 5, 5, 0)
	for i, row := range rows {
		if row.Metric == "failed_e2e_run_count" {
			rows = append(rows[:i], rows[i+1:]...)
			break
		}
	}
	chart := build(t, rows, TrendsQuery{}).Environments[0].Charts[1]
	if chart.Series[0].Defined[0] || chart.Series[1].Defined[0] || chart.Other.Defined[0] || !chart.Series[2].Defined[0] {
		t.Fatal("unknown lanes must be gaps without losing the independently known overall rate")
	}
}

func TestDiagnosticEmptyStagesAndOwnSampleSizes(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		out                            Outcomes
		provision, e2e, overall, other bool
	}{
		{"empty", Outcomes{}, false, false, false, false},
		{"all other", Outcomes{Runs: 10, Failed: 10, Other: 10, Defined: true, LanesDefined: true}, false, false, true, true},
		{"all provision", Outcomes{Runs: 10, Failed: 10, Provision: 10, Defined: true, LanesDefined: true}, true, false, true, true},
		{"mixed", Outcomes{Runs: 100, Failed: 90, Provision: 70, E2E: 10, Other: 10, Defined: true, LanesDefined: true}, true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chart := diagnosticChart([]Bucket{{StartDate: "2026-03-16", EndDate: "2026-03-16", Raw: tc.out}}, false)
			for i, want := range []bool{tc.provision, tc.e2e, tc.overall} {
				if chart.Series[i].Defined[0] != want {
					t.Fatalf("wrong availability for series %d", i)
				}
			}
			if chart.Other.Defined[0] != tc.other {
				t.Fatal("wrong Other availability")
			}
			if tc.name == "mixed" {
				if chart.Series[0].Caution[0] || !chart.Series[1].Caution[0] || chart.Series[2].Caution[0] {
					t.Fatal("low sample must use each stage's own denominator")
				}
				closeTo(t, chart.Series[1].Lower[0], 29.9298)
				if !strings.Contains(chart.Series[1].Notes[0], "10/20 runs") {
					t.Fatal("E2E details must use the E2E denominator")
				}
			}
		})
	}
}

func TestDiagnosticPartialPeriodsAndZeroFailures(t *testing.T) {
	out := Outcomes{Runs: 30, Defined: true, LanesDefined: true}
	chart := diagnosticChart([]Bucket{
		{StartDate: "2026-03-16", EndDate: "2026-03-16", Raw: out},
		{StartDate: "2026-03-17", EndDate: "2026-03-17", Raw: out, Partial: true},
	}, false)
	for _, series := range append(chart.Series, *chart.Other) {
		if !series.Defined[0] || !series.Defined[1] || series.Caution[0] || !series.Caution[1] {
			t.Fatalf("partial periods must flag each series independently of low samples: %+v", series)
		}
	}
	for _, series := range chart.Series {
		closeTo(t, series.Points[0], 100)
	}
	closeTo(t, chart.Other.Points[0], 0)
	closeTo(t, chart.Other.Lower[0], 0)
	closeTo(t, chart.Other.Upper[0], 11.35134)
}

func TestRangeModesAndInputValidation(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ mode, start, end string }{
		{TrendsModeRolling, "2026-03-12", "2026-03-18"},
		{TrendsModeWeekly, "2026-03-16", "2026-03-22"},
		{TrendsModeSprint, "2026-03-16", "2026-03-29"},
		{TrendsModeAll, "", ""},
	} {
		data := build(t, nil, TrendsQuery{Mode: tc.mode, GeneratedAt: now})
		if data.Meta.StartDate != tc.start || data.Meta.EndDate != tc.end || data.Meta.HasData {
			t.Fatalf("unexpected window: %+v", data.Meta)
		}
	}
	for _, query := range []TrendsQuery{
		{StartDate: "2026-03-16"}, {StartDate: "2026-03-18", EndDate: "2026-03-16"},
		{Environments: []string{"int"}}, {Environments: []string{"bogus"}}, {Granularity: "hourly"},
	} {
		if _, err := BuildTrends(context.Background(), &fakeStore{}, query); err == nil {
			t.Fatalf("expected error: %+v", query)
		}
	}
	_, err := BuildTrends(context.Background(), &fakeStore{err: errors.New("unavailable")}, TrendsQuery{})
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal("store errors must propagate")
	}
}

func TestRelativeWindowsUseUTCDaysIncludingToday(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ days, start string }{
		{"1", "2026-10-06"}, {"2", "2026-10-05"}, {"5", "2026-10-02"},
		{"7", "2026-09-30"}, {"14", "2026-09-23"}, {"30", "2026-09-07"}, {"90", "2026-07-09"},
	} {
		for _, granularity := range []string{Daily, Weekly} {
			data := build(t, nil, TrendsQuery{Mode: TrendsModeRelative, Days: tc.days, GeneratedAt: now, Granularity: granularity})
			if data.Meta.StartDate != tc.start || data.Meta.EndDate != "2026-10-06" || data.Meta.Granularity != granularity {
				t.Fatalf("days=%s granularity=%s: %+v", tc.days, granularity, data.Meta)
			}
		}
	}
	local := time.Date(2026, 10, 6, 0, 30, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	data := build(t, nil, TrendsQuery{Mode: TrendsModeRelative, Days: "1", GeneratedAt: local})
	if data.Meta.StartDate != "2026-10-05" || data.Meta.EndDate != "2026-10-05" {
		t.Fatalf("relative window must use UTC date: %+v", data.Meta)
	}
	legacy := build(t, nil, TrendsQuery{Mode: TrendsModeRolling, GeneratedAt: now})
	if legacy.Meta.RelativeDays != 7 || legacy.Meta.StartDate != "2026-09-30" {
		t.Fatalf("legacy rolling window changed: %+v", legacy.Meta)
	}
}

func TestRelativeWindowKeepsCalendarWeekBuckets(t *testing.T) {
	rows := append(dailyRows("2026-09-23", 10, 3, 5, 1, 0, 0), dailyRows("2026-10-06", 20, 5, 8, 0, 1, 0)...)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	query := TrendsQuery{Mode: TrendsModeRelative, Days: "14", GeneratedAt: now}
	data := build(t, rows, query)
	if len(data.Buckets) != 3 || data.Buckets[0].StartDate != "2026-09-21" ||
		!data.Buckets[0].Partial || data.Buckets[1].Partial || !data.Buckets[2].Partial {
		t.Fatalf("expected calendar weeks with partial boundaries: %+v", data.Buckets)
	}
	query.Granularity = Daily
	daily := build(t, rows, query)
	if len(daily.Buckets) != 14 || daily.Buckets[0].Partial || !daily.Buckets[13].Partial {
		t.Fatal("daily relative view must contain 14 calendar days, with today partial")
	}
	query.GeneratedAt = now.AddDate(0, 0, 1)
	later := build(t, rows, query)
	if later.Meta.StartDate != "2026-09-24" || later.Meta.EndDate != "2026-10-07" {
		t.Fatal("relative bookmarks must resolve at request time")
	}
}

func TestRelativeDaysValidation(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "3", "91", "abc", "1.5", "999999999999999999999999"} {
		if _, err := BuildTrends(context.Background(), &fakeStore{}, TrendsQuery{Mode: TrendsModeRelative, Days: value}); err == nil {
			t.Fatalf("expected validation error for days=%q", value)
		}
	}
	for _, mode := range []string{"", TrendsModeAll, TrendsModeWeekly, TrendsModeRolling} {
		if _, err := BuildTrends(context.Background(), &fakeStore{}, TrendsQuery{Mode: mode, Days: "14"}); err == nil {
			t.Fatalf("days must not be silently ignored for mode=%q", mode)
		}
	}
}

func TestDEVHistoryIgnoresOtherEnvironmentDates(t *testing.T) {
	rows := append(dailyRows("2026-03-16", 30, 3, 20, 0, 1, 0),
		storecontracts.MetricDailyRecord{Environment: "int", Date: "2025-01-01", Metric: "run_count", Value: 1})
	data := build(t, rows, TrendsQuery{})
	if len(data.Meta.Dates) != 1 || len(data.Buckets) != 1 || data.Buckets[0].StartDate != "2026-03-16" {
		t.Fatalf("non-DEV data extended axis: %+v", data.Meta)
	}
}
