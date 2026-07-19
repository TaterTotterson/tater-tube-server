package api

import "testing"

func TestTaterNzbMediaType(t *testing.T) {
	tests := []struct {
		name       string
		categoryID string
		category   string
		want       string
	}{
		{name: "movie category id", categoryID: "2040", want: "movie"},
		{name: "movie category label", category: "Movies > HD", want: "movie"},
		{name: "television category id", categoryID: "5040", want: "episode"},
		{name: "television category label", category: "TV / HD", want: "episode"},
		{name: "audio category id", categoryID: "3040", want: "audio"},
		{name: "unknown category", categoryID: "7000", category: "Other", want: "nzb"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := taterNzbMediaType(test.categoryID, test.category); got != test.want {
				t.Fatalf("taterNzbMediaType(%q, %q) = %q, want %q",
					test.categoryID, test.category, got, test.want)
			}
		})
	}
}

func TestTaterAnnotateNzbMediaTypesUsesExplicitFeedCategory(t *testing.T) {
	items := []taterUsenetItem{{Title: "Release", Category: "HD"}}

	taterAnnotateNzbMediaTypes(items, "", "movie")

	if items[0].MediaType != "movie" {
		t.Fatalf("expected movie media type, got %#v", items[0])
	}
}
