package apps

import "strings"

const (
	CatalogBaseURL          = "https://apps.tronbyt.com"
	PublicSystemAppsRepoURL = "https://github.com/tronbyt/apps.git"
)

// CatalogSlug returns the slug used by apps.tronbyt.com, which matches packageName
// rather than the manifest id when they differ (e.g. id "zotero-reading", packageName "zoteroreading").
func (a AppMetadata) CatalogSlug() string {
	if a.PackageName != "" {
		return a.PackageName
	}
	return a.ID
}

// CatalogDetailsURL returns the public app info page URL for a catalog slug.
func CatalogDetailsURL(slug string) string {
	if slug == "" {
		return ""
	}
	return CatalogBaseURL + "/details/" + slug
}

// CatalogDetailsURL returns the public info page URL for this app, if it has a catalog slug.
func (a AppMetadata) CatalogDetailsURL() string {
	return CatalogDetailsURL(a.CatalogSlug())
}

// UsesPublicAppsCatalog reports whether the configured system apps git repo is the
// official tronbyt/apps catalog (apps.tronbyt.com only applies there).
func UsesPublicAppsCatalog(systemAppsRepo string) bool {
	return normalizeSystemAppsRepo(systemAppsRepo) == normalizeSystemAppsRepo(PublicSystemAppsRepoURL)
}

func normalizeSystemAppsRepo(repo string) string {
	repo = strings.TrimSpace(strings.ToLower(repo))
	if i := strings.Index(repo, "#"); i >= 0 {
		repo = repo[:i]
	}
	repo = strings.TrimSuffix(repo, "/")
	if rest, ok := strings.CutPrefix(repo, "git@github.com:"); ok {
		repo = "https://github.com/" + rest
	}
	if strings.Contains(repo, "github.com/") && !strings.HasSuffix(repo, ".git") {
		repo += ".git"
	}
	return repo
}

// PublicCatalogDetailsURL returns the apps.tronbyt.com details URL when the server
// uses the public system apps repo and this app is a native catalog entry.
func (a AppMetadata) PublicCatalogDetailsURL(publicRepo bool) string {
	if !publicRepo || a.CatalogSlug() == "" {
		return ""
	}
	if p := strings.ToLower(strings.TrimSpace(a.Provider)); p != "" && p != "tronbyt" {
		return ""
	}
	return a.CatalogDetailsURL()
}

// IsSystemCatalogPath reports whether an installation path refers to a system apps repo app.
func IsSystemCatalogPath(appPath string) bool {
	return strings.HasPrefix(appPath, "system-apps/")
}
