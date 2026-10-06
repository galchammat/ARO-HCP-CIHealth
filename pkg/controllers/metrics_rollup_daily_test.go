package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/roivaz/ARO-HCP-CIHealth/pkg/store/contracts"
)

type comparisonRollupStore struct {
	fakeProwRunsStore
	input   []contracts.RunRecord
	metrics []contracts.MetricDailyRecord
}

func (s *comparisonRollupStore) ListRunsByDateRange(context.Context, string, time.Time, time.Time) ([]contracts.RunRecord, error) {
	return s.input, nil
}

func (s *comparisonRollupStore) UpsertMetricsDaily(_ context.Context, rows []contracts.MetricDailyRecord) error {
	s.metrics = rows
	return nil
}

func TestRollupPostGoodAndBatchUnionCountsEachRunOnce(t *testing.T) {
	store := &comparisonRollupStore{input: []contracts.RunRecord{
		{Environment: "dev", RunURL: "gs://test-platform-results/pr-logs/pull/batch/job/1", Failed: true},
		{Environment: "dev", RunURL: "gs://test-platform-results/pr-logs/pull/Azure_ARO-HCP/1/job/2", PostGoodCommit: true},
		{Environment: "dev", RunURL: "gs://test-platform-results/pr-logs/pull/Azure_ARO-HCP/2/job/3", Failed: true},
		{Environment: "dev", RunURL: "gs://test-platform-results/pr-logs/pull/batch/job/4", PostGoodCommit: true},
	}}
	controller := &metricsRollupDailyController{logger: logr.Discard(), store: store, envs: []string{"dev"}}
	if err := controller.processKey(context.Background(), "2026-03-16"); err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, row := range store.metrics {
		got[row.Metric] = row.Value
	}
	for key, want := range map[string]float64{
		metricRunCount: 4, metricFailureCount: 2,
		metricPostGoodRunCount: 3, metricPostGoodFailedCIInfraRunCount: 1,
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
}

func TestIsMetricPostGoodRunCountsBatchRuns(t *testing.T) {
	t.Parallel()

	const (
		batchRunURL   = "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2029578186907455498"
		prCheckRunURL = "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/4062/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2029578186907455488"
	)

	store := &fakeProwRunsStore{
		runs:        map[string]contracts.RunRecord{},
		checkpoints: map[string]contracts.CheckpointRecord{},
	}
	// Batch run whose PR-based signal is NOT post-good; it should still count.
	store.runs[store.runKey("dev", batchRunURL)] = contracts.RunRecord{
		Environment:    "dev",
		RunURL:         batchRunURL,
		PostGoodCommit: false,
	}
	// PR-check run that is not post-good stays not-post-good.
	store.runs[store.runKey("dev", prCheckRunURL)] = contracts.RunRecord{
		Environment:    "dev",
		RunURL:         prCheckRunURL,
		PostGoodCommit: false,
	}

	tests := []struct {
		name   string
		runURL string
		want   bool
	}{
		{name: "batch run overrides non-post-good stored signal", runURL: batchRunURL, want: true},
		{name: "pr-check run without post-good stays false", runURL: prCheckRunURL, want: false},
		{name: "batch run not present in store still counts", runURL: "gs://test-platform-results/pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2074433538186285056", want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runCache := map[string]contracts.RunRecord{}
			runFoundCache := map[string]bool{}
			got, err := isMetricPostGoodRun(context.Background(), store, "dev", tt.runURL, runCache, runFoundCache)
			if err != nil {
				t.Fatalf("isMetricPostGoodRun(%q): %v", tt.runURL, err)
			}
			if got != tt.want {
				t.Fatalf("isMetricPostGoodRun(%q): got=%v want=%v", tt.runURL, got, tt.want)
			}
			// Second call exercises the cache path and must be stable.
			got2, err := isMetricPostGoodRun(context.Background(), store, "dev", tt.runURL, runCache, runFoundCache)
			if err != nil {
				t.Fatalf("isMetricPostGoodRun(%q) cached: %v", tt.runURL, err)
			}
			if got2 != tt.want {
				t.Fatalf("isMetricPostGoodRun(%q) cached: got=%v want=%v", tt.runURL, got2, tt.want)
			}
		})
	}
}
