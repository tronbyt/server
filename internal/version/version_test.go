package version

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeGitURL verifies the expected behavior under test.
func TestNormalizeGitURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "git@github.com:brombomb/tronbyt-server.git",
			want:  "https://github.com/brombomb/tronbyt-server",
		},
		{
			input: "git@github.com:brombomb/tronbyt-server",
			want:  "https://github.com/brombomb/tronbyt-server",
		},
		{
			input: "ssh://git@github.com/brombomb/tronbyt-server.git",
			want:  "https://github.com/brombomb/tronbyt-server",
		},
		{
			input: "https://github.com/brombomb/tronbyt-server.git",
			want:  "https://github.com/brombomb/tronbyt-server",
		},
		{
			input: "https://github.com/brombomb/tronbyt-server",
			want:  "https://github.com/brombomb/tronbyt-server",
		},
		{
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeGitURL(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestInitGitInfo verifies the expected behavior under test.
func TestInitGitInfo(t *testing.T) {
	if _, err := os.Stat("../../.git"); os.IsNotExist(err) {
		t.Setenv("TRONBYT_COMMIT", "abcdef1234567890")
		t.Setenv("TRONBYT_BRANCH", "main")
		t.Setenv("TRONBYT_REPO_URL", "https://github.com/tronbyt/server")
		ResetGitInfo()
	}
	EnsureGitInfo()

	assert.NotEmpty(t, Commit)
	assert.NotEqual(t, "unknown", Commit)
	assert.NotEmpty(t, RepoURL)
	assert.NotEmpty(t, CommitURL)
	assert.Contains(t, CommitURL, RepoURL)
}

// TestEnvOverrides verifies the expected behavior under test.
func TestEnvOverrides(t *testing.T) {
	// Backup original state
	origCommit, origBranch, origRepo, origURL := Commit, Branch, RepoURL, CommitURL
	defer func() {
		Commit, Branch, RepoURL, CommitURL = origCommit, origBranch, origRepo, origURL
		ResetGitInfo()
	}()

	t.Setenv("TRONBYT_COMMIT", "abcdef1234567890abcdef1234567890abcdef12")
	t.Setenv("TRONBYT_BRANCH", "custom-branch")
	t.Setenv("TRONBYT_REPO_URL", "https://github.com/myfork/tronbyt-server")

	Commit = ""
	Branch = ""
	RepoURL = ""
	CommitURL = ""
	ResetGitInfo()

	EnsureGitInfo()

	assert.Equal(t, "abcdef1234567890abcdef1234567890abcdef12", Commit)
	assert.Equal(t, "custom-branch", Branch)
	assert.Equal(t, "https://github.com/myfork/tronbyt-server", RepoURL)
	assert.Equal(t, "https://github.com/myfork/tronbyt-server/tree/custom-branch", CommitURL)
}

// TestBranchFallbackWhenCommitUnknown verifies the expected behavior under test.
func TestBranchFallbackWhenCommitUnknown(t *testing.T) {
	origCommit, origBranch, origRepo, origURL := Commit, Branch, RepoURL, CommitURL
	origAllow := AllowRemoteLookup
	defer func() {
		Commit, Branch, RepoURL, CommitURL = origCommit, origBranch, origRepo, origURL
		AllowRemoteLookup = origAllow
		ResetGitInfo()
	}()
	AllowRemoteLookup = false

	t.Setenv("TRONBYT_COMMIT", "unknown")
	t.Setenv("TRONBYT_BRANCH", "nonexistent-branch-xyz")
	t.Setenv("TRONBYT_REPO_URL", "https://github.com/myfork/tronbyt-server")

	Commit = "unknown"
	Branch = ""
	RepoURL = ""
	CommitURL = ""
	ResetGitInfo()

	EnsureGitInfo()

	assert.Equal(t, "nonexistent-branch-xyz", Branch)
	assert.Equal(t, "https://github.com/myfork/tronbyt-server/tree/nonexistent-branch-xyz", CommitURL)
}

// TestAllowRemoteLookupDisabled verifies the expected behavior under test.
func TestAllowRemoteLookupDisabled(t *testing.T) {
	origAllow := AllowRemoteLookup
	defer func() {
		AllowRemoteLookup = origAllow
	}()

	assert.True(t, AllowRemoteLookup)
	AllowRemoteLookup = false
	assert.False(t, AllowRemoteLookup)
}

// TestIsUpstreamRepo verifies the expected behavior under test.
func TestIsUpstreamRepo(t *testing.T) {
	assert.True(t, IsUpstreamRepo(""))
	assert.True(t, IsUpstreamRepo("https://github.com/tronbyt/server"))
	assert.True(t, IsUpstreamRepo("https://github.com/tronbyt/server.git"))
	assert.True(t, IsUpstreamRepo("git@github.com:tronbyt/server.git"))

	assert.False(t, IsUpstreamRepo("https://github.com/brombomb/tronbyt-server"))
	assert.False(t, IsUpstreamRepo("https://github.com/brombomb/tronbyt-server.git"))
	assert.False(t, IsUpstreamRepo("git@github.com:brombomb/tronbyt-server.git"))
	assert.False(t, IsUpstreamRepo("https://github.com/otheruser/server"))
}

// TestShouldShowBranchTag verifies the expected behavior under test.
func TestShouldShowBranchTag(t *testing.T) {
	upstream := "https://github.com/tronbyt/server"
	fork := "https://github.com/brombomb/tronbyt-server"

	// Upstream repo on main/master should NOT show branch tag
	assert.False(t, ShouldShowBranchTag(upstream, "main"))
	assert.False(t, ShouldShowBranchTag(upstream, "master"))
	assert.False(t, ShouldShowBranchTag(upstream, ""))
	assert.False(t, ShouldShowBranchTag(upstream, "HEAD"))

	// Upstream repo on a non-main branch SHOULD show branch tag
	assert.True(t, ShouldShowBranchTag(upstream, "dev"))
	assert.True(t, ShouldShowBranchTag(upstream, "feat/new-ui"))

	// Fork repo on main branch SHOULD show branch tag (since it's a fork)
	assert.True(t, ShouldShowBranchTag(fork, "main"))
	assert.True(t, ShouldShowBranchTag(fork, "master"))

	// Fork repo on a feature branch SHOULD show branch tag
	assert.True(t, ShouldShowBranchTag(fork, "feat/notification-sounds"))
}
