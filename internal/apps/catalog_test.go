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

func TestUsesPublicAppsCatalog(t *testing.T) {
	cases := map[string]bool{
		PublicSystemAppsRepoURL:                    true,
		PublicSystemAppsRepoURL + "#main":          true,
		"git@github.com:tronbyt/apps.git":          true,
		"https://github.com/tronbyt/apps":          true,
		"https://github.com/someone/apps-fork.git": false,
		"file:///tmp/my-apps":                      false,
	}
	for repo, want := range cases {
		if got := UsesPublicAppsCatalog(repo); got != want {
			t.Fatalf("%q: got %v want %v", repo, got, want)
		}
	}
}

func TestPublicCatalogDetailsURL(t *testing.T) {
	meta := AppMetadata{ID: "clock", PackageName: "clock", Provider: "screeny"}
	if got := meta.PublicCatalogDetailsURL(true); got != "" {
		t.Fatalf("external provider: got %q", got)
	}
	meta.Provider = ""
	if got := meta.PublicCatalogDetailsURL(false); got != "" {
		t.Fatalf("private repo: got %q", got)
	}
	if got := meta.PublicCatalogDetailsURL(true); got != "https://apps.tronbyt.com/details/clock" {
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
