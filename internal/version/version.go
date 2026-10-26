package version

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v6"
)

var (
	Version       = "dev"
	Commit        = ""
	Branch        = ""
	BuildDate     = ""
	RepoURL       = ""
	CommitURL     = ""
	ShowBranchTag = false

	// AllowRemoteLookup permits an outbound GitHub API call to resolve a missing commit SHA.
	AllowRemoteLookup = true

	gitInfoOnce sync.Once
)

// ResetGitInfo resets the sync.Once for testing purposes.
func ResetGitInfo() {
	gitInfoOnce = sync.Once{}
}

// EnsureGitInfo populates git metadata (commit, branch, repo/commit URLs) if not already set.
func EnsureGitInfo() {
	gitInfoOnce.Do(initGitInfo)
}

// initGitInfo populates git metadata from build info, environment variables, or the local git repository.
func initGitInfo() {
	// 1. Read environment variables if not already baked in via ldflags
	if Commit == "" || Commit == "unknown" {
		for _, key := range []string{"TRONBYT_COMMIT", "GIT_COMMIT", "COMMIT"} {
			if val := strings.TrimSpace(os.Getenv(key)); val != "" && val != "unknown" {
				Commit = val
				break
			}
		}
	}
	if Branch == "" || Branch == "unknown" {
		for _, key := range []string{"TRONBYT_BRANCH", "GIT_BRANCH", "BRANCH"} {
			if val := strings.TrimSpace(os.Getenv(key)); val != "" && val != "unknown" {
				Branch = val
				break
			}
		}
	}
	if RepoURL == "" || RepoURL == "unknown" {
		for _, key := range []string{"TRONBYT_REPO_URL", "GIT_REPO_URL", "REPO_URL"} {
			if val := strings.TrimSpace(os.Getenv(key)); val != "" && val != "unknown" {
				RepoURL = NormalizeGitURL(val)
				break
			}
		}
	}
	if Version == "" || Version == "dev" {
		for _, key := range []string{"TRONBYT_VERSION", "VERSION"} {
			if val := strings.TrimSpace(os.Getenv(key)); val != "" {
				Version = val
				break
			}
		}
	}

	// 2. Try reading from .git repository if present in cwd or executable directory
	searchDirs := []string{"."}
	if execPath, err := os.Executable(); err == nil && execPath != "" {
		dir := filepath.Dir(execPath)
		if dir != "." {
			searchDirs = append(searchDirs, dir)
		}
	}

	for _, dir := range searchDirs {
		if r, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true}); err == nil {
			if head, err := r.Head(); err == nil {
				if Commit == "" || Commit == "unknown" {
					Commit = head.Hash().String()
				}
				if (Branch == "" || Branch == "unknown") && head.Name().IsBranch() {
					Branch = head.Name().Short()
				}
			}

			if RepoURL == "" || RepoURL == "unknown" {
				if cfg, err := r.Config(); err == nil && cfg != nil {
					remoteName := ""
					if Branch != "" && cfg.Branches[Branch] != nil {
						remoteName = cfg.Branches[Branch].Remote
					}
					if remoteName == "" || cfg.Remotes[remoteName] == nil {
						if cfg.Remotes["fork"] != nil {
							remoteName = "fork"
						} else if cfg.Remotes["origin"] != nil {
							remoteName = "origin"
						}
					}
					if remoteName != "" && cfg.Remotes[remoteName] != nil && len(cfg.Remotes[remoteName].URLs) > 0 {
						RepoURL = NormalizeGitURL(cfg.Remotes[remoteName].URLs[0])
					}
				}
			}
			break
		}
	}

	// 3. Fallback to runtime/debug build info if Commit still empty or unknown
	if Commit == "" || Commit == "unknown" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" && s.Value != "" {
					Commit = s.Value
					break
				}
			}
		}
	}

	if RepoURL != "" {
		RepoURL = NormalizeGitURL(RepoURL)
	} else {
		RepoURL = "https://github.com/tronbyt/server"
	}

	// 4. If Branch and RepoURL are known, but Commit is still missing, query GitHub API
	if AllowRemoteLookup && (Commit == "" || Commit == "unknown") && Branch != "" && Branch != "HEAD" {
		if sha := fetchGitHubCommitSHA(RepoURL, Branch); sha != "" {
			Commit = sha
		}
	}

	// 5. Construct CommitURL
	if CommitURL == "" {
		if Branch != "" && Branch != "HEAD" {
			CommitURL = fmt.Sprintf("%s/tree/%s", RepoURL, Branch)
		} else if Commit != "" && Commit != "unknown" {
			CommitURL = fmt.Sprintf("%s/tree/%s", RepoURL, Commit)
		} else {
			CommitURL = RepoURL
		}
	}

	ShowBranchTag = ShouldShowBranchTag(RepoURL, Branch)
}

// UpdateInstructionsURL returns the repository README anchor for upgrade steps.
func UpdateInstructionsURL() string {
	EnsureGitInfo()
	return RepoURL + "#updating"
}

// fetchGitHubCommitSHA queries the GitHub API to resolve the latest commit SHA for a repository branch.
func fetchGitHubCommitSHA(repoURL, branch string) string {
	u, err := url.Parse(repoURL)
	if err != nil || !strings.Contains(u.Host, "github.com") {
		return ""
	}
	trimmedPath := strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/")
	parts := strings.Split(trimmedPath, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, repo := parts[0], parts[1]
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, url.PathEscape(branch))

	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github.sha")
	req.Header.Set("User-Agent", "Tronbyt-Server")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(body))
	if len(sha) == 40 {
		return sha
	}
	return ""
}

// NormalizeGitURL converts git remote URLs (SSH, SCP-like, or HTTPS) into a standard HTTPS web URL.
func NormalizeGitURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// git@github.com:owner/repo.git
	if rem, ok := strings.CutPrefix(raw, "git@"); ok {
		parts := strings.SplitN(rem, ":", 2)
		if len(parts) == 2 {
			host := parts[0]
			path := strings.TrimSuffix(parts[1], ".git")
			return fmt.Sprintf("https://%s/%s", host, path)
		}
	}
	// ssh://git@github.com/owner/repo.git
	if rem, ok := strings.CutPrefix(raw, "ssh://"); ok {
		if idx := strings.Index(rem, "@"); idx != -1 {
			rem = rem[idx+1:]
		}
		parts := strings.SplitN(rem, "/", 2)
		if len(parts) == 2 {
			host := parts[0]
			path := strings.TrimSuffix(parts[1], ".git")
			return fmt.Sprintf("https://%s/%s", host, path)
		}
	}
	// https://github.com/owner/repo.git
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		u.Path = strings.TrimSuffix(u.Path, ".git")
		return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)
	}
	return strings.TrimSuffix(raw, ".git")
}

// IsUpstreamRepo returns true if repoURL points to the official upstream tronbyt/server repository.
func IsUpstreamRepo(repoURL string) bool {
	normalized := strings.ToLower(NormalizeGitURL(repoURL))
	normalized = strings.TrimSuffix(normalized, ".git")
	normalized = strings.TrimPrefix(normalized, "https://")
	normalized = strings.TrimPrefix(normalized, "http://")
	return normalized == "" || normalized == "github.com/tronbyt/server"
}

// ShouldShowBranchTag returns true if the checkout is on a fork or on a non-main branch.
func ShouldShowBranchTag(repoURL, branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	if b == "" || b == "unknown" || b == "head" {
		return false
	}
	isFork := !IsUpstreamRepo(repoURL)
	notMain := b != "main" && b != "master"
	return isFork || notMain
}
