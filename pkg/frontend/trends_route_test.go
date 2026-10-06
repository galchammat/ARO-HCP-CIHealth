package frontend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	readmodeltrends "github.com/roivaz/ARO-HCP-CIHealth/pkg/frontend/readmodel/trends"
	storecontracts "github.com/roivaz/ARO-HCP-CIHealth/pkg/store/contracts"
)

func TestHandleTrendsPageRendersHTML(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newHandlerFixture(t)
	store := fixture.openWeekStore(t, "2026-03-16")
	if err := store.UpsertMetricsDaily(ctx, []storecontracts.MetricDailyRecord{
		{Environment: "dev", Date: "2026-03-16", Metric: "run_count", Value: 10},
		{Environment: "dev", Date: "2026-03-17", Metric: "run_count", Value: 12},
		{Environment: "dev", Date: "2026-03-16", Metric: "failure_count", Value: 3},
		{Environment: "dev", Date: "2026-03-17", Metric: "failure_count", Value: 2},
	}); err != nil {
		t.Fatalf("seed metrics daily: %v", err)
	}

	handler, err := NewHandler(HandlerOptions{PostgresPool: fixture.pool})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/trends?mode=all&granularity=daily", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("unexpected content type: %q", got)
	}
	body := recorder.Body.String()
	for _, snippet := range []string{"CIHealth Trends", "Environment: DEV", "trend-line-svg", "<polyline"} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("expected trends HTML to contain %q, body=%s", snippet, body)
		}
	}
	// The time-range selector should mirror the failure-patterns presets.
	for _, snippet := range []string{"time-selector", "All history", "Relative", "Includes today; UTC", "Last 7 days", "Weekly:", "Sprint:", "mode=relative", "granularity=daily", `value="daily" selected`, `value="weekly"`, "Tide batches", "All runs", "Post-good + batches", "Provision success", "E2E success", "Other failures (% of all runs)", "Other failures (% of post-good + batch runs)"} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("expected trends time selector to contain %q, body=%s", snippet, body)
		}
		for _, unwanted := range []string{`name="failed_at"`, `name="env"`, `id="tz-toggle"`, "Env: ALL", "CI health trends", "Raw (including batches)", "Sample sizes and outcomes", "<table", "table below"} {
			if strings.Contains(body, unwanted) {
				t.Fatalf("trends must not expose irrelevant control %q", unwanted)
			}
		}
		if !strings.Contains(body, "Env: DEV") {
			t.Fatal("trends must show its fixed DEV scope")
		}
	}
}

func TestHandleTrendsPageRollingModeSelectsWindow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newHandlerFixture(t)
	store := fixture.openWeekStore(t, "2026-03-16")
	if err := store.UpsertMetricsDaily(ctx, []storecontracts.MetricDailyRecord{
		{Environment: "dev", Date: "2026-03-16", Metric: "run_count", Value: 10},
	}); err != nil {
		t.Fatalf("seed metrics daily: %v", err)
	}

	handler, err := NewHandler(HandlerOptions{PostgresPool: fixture.pool})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	// Rolling resolves relative to "now"; the March fixture data is far in the
	// past, so the rolling window is empty and the empty-state renders. The page
	// must still return 200 and mark the rolling preset active.
	req := httptest.NewRequest(http.MethodGet, "/trends?mode=rolling", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "Last 7 days") {
		t.Fatalf("expected rolling label in trends HTML, body=%s", body)
	}
}

func TestTrendsDefaultWindowControlsAndAPI(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixture(t)
	handler, err := NewHandler(HandlerOptions{PostgresPool: fixture.pool})
	if err != nil {
		t.Fatal(err)
	}
	for _, granularity := range []string{"daily", "weekly"} {
		path := "/trends"
		if granularity == "weekly" {
			path += "?granularity=weekly"
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
		}
		for _, snippet := range []string{
			`time-selector-summary-text">Last 30 days</span>`,
			`value="` + granularity + `" selected`,
			`href="/api/trends?days=30&amp;granularity=` + granularity + `&amp;mode=relative"`,
			`href="/trends">Reset</a>`,
			`href="/trends?granularity=` + granularity + `&amp;mode=all"`,
		} {
			if !strings.Contains(recorder.Body.String(), snippet) {
				t.Fatalf("missing default-window control %q", snippet)
			}
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/trends", nil))
	var data readmodeltrends.TrendsData
	if err := json.Unmarshal(recorder.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || data.Meta.RelativeDays != 30 || data.Meta.Granularity != "daily" {
		t.Fatalf("API and page defaults must match: %+v", data.Meta)
	}
}

func TestHandleAPITrendsReturnsJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newHandlerFixture(t)
	store := fixture.openWeekStore(t, "2026-03-16")
	if err := store.UpsertMetricsDaily(ctx, []storecontracts.MetricDailyRecord{
		{Environment: "dev", Date: "2026-03-16", Metric: "run_count", Value: 10},
		{Environment: "dev", Date: "2026-03-16", Metric: "failure_count", Value: 4},
		{Environment: "dev", Date: "2026-03-16", Metric: "post_good_run_count", Value: 5},
		{Environment: "dev", Date: "2026-03-16", Metric: "post_good_failed_provision_run_count", Value: 1},
		{Environment: "dev", Date: "2026-03-16", Metric: "post_good_failed_e2e_jobs", Value: 0},
		{Environment: "dev", Date: "2026-03-16", Metric: "post_good_failed_ci_infra_run_count", Value: 0},
		{Environment: "int", Date: "2026-03-16", Metric: "run_count", Value: 4},
	}); err != nil {
		t.Fatalf("seed metrics daily: %v", err)
	}

	handler, err := NewHandler(HandlerOptions{PostgresPool: fixture.pool})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/trends?mode=all&env=dev&granularity=daily", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("unexpected content type: %q", got)
	}

	var payload readmodeltrends.TrendsData
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Meta.HasData {
		t.Fatalf("expected has_data true, body=%s", recorder.Body.String())
	}
	if got, want := len(payload.Meta.Environments), 1; got != want {
		t.Fatalf("expected env filter to yield %d environment, got %d", want, got)
	}
	if payload.Meta.Environments[0] != "dev" {
		t.Fatalf("expected dev environment, got %q", payload.Meta.Environments[0])
	}
	if payload.Meta.Granularity != "daily" || len(payload.Buckets) != 1 ||
		payload.Buckets[0].Raw.Rate != 60 || payload.Buckets[0].Filtered.Rate != 80 {
		t.Fatalf("incorrect comparison: %+v", payload)
	}
}

func TestHandleAPITrendsReturnsJSONError(t *testing.T) {
	t.Parallel()

	fixture := newHandlerFixture(t)
	handler, err := NewHandler(HandlerOptions{PostgresPool: fixture.pool})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	for _, query := range []string{
		"start_date=2026-03-18&end_date=2026-03-16", "env=int", "granularity=hourly",
		"mode=relative", "mode=relative&days=3", "mode=relative&days=abc", "days=14",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/trends?"+query, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if got, want := recorder.Code, http.StatusBadRequest; got != want {
			t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, recorder.Body.String())
		}
	}
}

func TestTrendsLinksPreserveGranularity(t *testing.T) {
	h := &handler{}
	for _, link := range h.trendsTimeSelectorLinks("2026-03-18", "daily", "", true) {
		if !strings.Contains(link.Href, "granularity=daily") {
			t.Fatalf("preset lost granularity: %+v", link)
		}

	}
	href := h.shiftedTrendsHref("2026-03-16", "2026-03-22", "weekly", 7, "daily")
	for _, part := range []string{"start_date=2026-03-23", "end_date=2026-03-29", "granularity=daily"} {
		if !strings.Contains(href, part) {
			t.Fatalf("shifted link lost %s: %s", part, href)
		}
	}
}

func TestRelativePresetLinksAndDisabledNavigation(t *testing.T) {
	links := trendsRelativeTimeSelectorLinks("daily", 14)
	if len(links) != 7 {
		t.Fatalf("expected seven relative presets: %+v", links)
	}
	for i, link := range links {
		u, err := url.Parse(link.Href)
		if err != nil {
			t.Fatal(err)
		}
		q := trendsQueryFromRequest(httptest.NewRequest(http.MethodGet, u.String(), nil))
		if q.Mode != "relative" || q.Granularity != "daily" || q.StartDate != "" || q.EndDate != "" ||
			q.Days != []string{"1", "2", "5", "7", "14", "30", "90"}[i] || link.Active != (q.Days == "14") {
			t.Fatalf("invalid relative link: %+v query=%+v", link, q)
		}
	}
	h := &handler{}
	mode := trendsTimeSelectorMode("relative", "2026-09-23", "2026-10-06")
	if h.shiftedTrendsHref("2026-09-23", "2026-10-06", mode, -14, "daily") != "" {
		t.Fatal("relative navigation must be disabled")
	}
}

func TestRelativeTrendsHTMLAndAPIKeepWindow(t *testing.T) {
	fixture := newHandlerFixture(t)
	handler, err := NewHandler(HandlerOptions{PostgresPool: fixture.pool})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/trends?mode=relative&days=14&granularity=daily", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, body)
	}
	for _, snippet := range []string{
		"Last 14 days", "/api/trends?days=14&amp;granularity=daily&amp;mode=relative",
		`name="start_date"`, `name="end_date"`, `name="granularity"`,
	} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("missing %q", snippet)
		}
	}
	// Relative presets are links, not submitted fields: applying custom dates
	// must not retain parameters that override those dates.
	for _, snippet := range []string{`name="mode"`, `name="days"`} {
		if strings.Contains(body, snippet) {
			t.Fatalf("custom-range form retains relative parameter %s", snippet)
		}
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/trends?mode=relative&days=14&granularity=daily", nil))
	var data readmodeltrends.TrendsData
	if err := json.Unmarshal(recorder.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Meta.RelativeDays != 14 || data.Meta.Granularity != "daily" {
		t.Fatalf("API lost relative window: %+v", data.Meta)
	}
}
