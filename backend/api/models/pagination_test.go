package models

import "testing"

// TestParseModelsListPageSize covers ci#1149: a page_size above the old hard 100
// row cap was discarded entirely, so requests like
// /api/v1/models?page=1&page_size=300 silently fell back to the 12 row default
// and the model dropdown was truncated to the first page of the catalogue.
func TestParseModelsListPageSize(t *testing.T) {
	cases := []struct {
		name  string
		param string
		want  int
	}{
		{name: "empty falls back to default", param: "", want: defaultModelsListPageSize},
		{name: "whitespace falls back to default", param: "   ", want: defaultModelsListPageSize},
		{name: "non numeric falls back to default", param: "abc", want: defaultModelsListPageSize},
		{name: "zero falls back to default", param: "0", want: defaultModelsListPageSize},
		{name: "negative falls back to default", param: "-5", want: defaultModelsListPageSize},
		{name: "small value is honoured", param: "25", want: 25},
		{name: "previously discarded 100 is honoured", param: "100", want: 100},
		{name: "previously discarded 300 is honoured", param: "300", want: 300},
		{name: "surrounding whitespace is trimmed", param: " 300 ", want: 300},
		{name: "value at the cap is honoured", param: "1000", want: maxModelsListPageSize},
		{name: "value above the cap is clamped", param: "5000", want: maxModelsListPageSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseModelsListPageSize(tc.param); got != tc.want {
				t.Fatalf("parseModelsListPageSize(%q) = %d, want %d", tc.param, got, tc.want)
			}
		})
	}
}

// TestParseModelsListPageSizeKeepsCatalogueVisible guards the regression that
// motivated the fix: the seeded catalogue plus user-defined models must fit in a
// single page when the bot settings pages ask for 300 rows.
func TestParseModelsListPageSizeKeepsCatalogueVisible(t *testing.T) {
	pageSize := parseModelsListPageSize("300")
	if pageSize < 300 {
		t.Fatalf("page_size=300 resolved to %d, the model dropdown would be truncated", pageSize)
	}
}
