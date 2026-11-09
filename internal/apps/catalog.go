package apps

import "strings"

const CatalogBaseURL = "https://apps.tronbyt.com"

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

// IsSystemCatalogPath reports whether an installation path refers to a system apps repo app.
func IsSystemCatalogPath(appPath string) bool {
	return strings.HasPrefix(appPath, "system-apps/")
}
