package apps

import "testing"

func TestCatalogDetailsURL(t *testing.T) {
	if got := CatalogDetailsURL("streamgauge"); got != "https://apps.tronbyt.com/details/streamgauge" {
		t.Fatalf("got %q", got)
	}
	if CatalogDetailsURL("") != "" {
		t.Fatal("expected empty URL for empty id")
	}
}

func TestCatalogSlugPrefersPackageName(t *testing.T) {
	meta := AppMetadata{ID: "zotero-reading", PackageName: "zoteroreading"}
	if got := meta.CatalogSlug(); got != "zoteroreading" {
		t.Fatalf("got %q", got)
	}
	if got := meta.CatalogDetailsURL(); got != "https://apps.tronbyt.com/details/zoteroreading" {
		t.Fatalf("got %q", got)
	}
}

func TestIsSystemCatalogPath(t *testing.T) {
	if !IsSystemCatalogPath("system-apps/apps/streamgauge/streamgauge.star") {
		t.Fatal("expected system path")
	}
	if IsSystemCatalogPath("users/alice/apps/foo") {
		t.Fatal("expected false for user path")
	}
}
