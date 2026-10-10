package gitutils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Local repo for EnsureRepo to clone over the file transport.
type sourceRepo struct {
	t             *testing.T
	path          string
	repo          *git.Repository
	defaultBranch string
}

// Default branch holds default.txt; "feature" branch adds feature.txt.
func newSourceRepo(t *testing.T) *sourceRepo {
	t.Helper()
	path := t.TempDir()
	r, err := git.PlainInit(path, false)
	require.NoError(t, err)
	s := &sourceRepo{t: t, path: path, repo: r}
	s.commitFile("default.txt", "initial")

	head, err := r.Head()
	require.NoError(t, err)
	s.defaultBranch = head.Name().Short()

	s.checkout("feature", true)
	s.commitFile("feature.txt", "feature")
	s.checkout(s.defaultBranch, false)
	return s
}

func (s *sourceRepo) checkout(branch string, create bool) {
	s.t.Helper()
	w, err := s.repo.Worktree()
	require.NoError(s.t, err)
	require.NoError(s.t, w.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch), Create: create}))
}

func (s *sourceRepo) commitFile(name, content string) {
	s.t.Helper()
	require.NoError(s.t, os.WriteFile(filepath.Join(s.path, name), []byte(content), 0o644))
	w, err := s.repo.Worktree()
	require.NoError(s.t, err)
	_, err = w.Add(name)
	require.NoError(s.t, err)
	sig := &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}
	_, err = w.Commit("add "+name, &git.CommitOptions{Author: sig})
	require.NoError(s.t, err)
}

func headBranch(t *testing.T, path string) string {
	t.Helper()
	r, err := git.PlainOpen(path)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	head, err := r.Head()
	require.NoError(t, err)
	return head.Name().Short()
}

// File inside .git: kept by an in-place update, removed by a re-clone.
func markClone(t *testing.T, path string) string {
	t.Helper()
	marker := filepath.Join(path, ".git", "test-marker")
	require.NoError(t, os.WriteFile(marker, nil, 0o644))
	return marker
}

func TestEnsureRepoClonesDefaultBranch(t *testing.T) {
	src := newSourceRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")

	require.NoError(t, EnsureRepo(dst, src.path, "", true, 0))

	assert.Equal(t, src.defaultBranch, headBranch(t, dst))
	assert.NoFileExists(t, filepath.Join(dst, "feature.txt"))
}

func TestEnsureRepoClonesBranchFromSuffix(t *testing.T) {
	src := newSourceRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")

	require.NoError(t, EnsureRepo(dst, src.path+"#feature", "", true, 0))

	assert.Equal(t, "feature", headBranch(t, dst))
	assert.FileExists(t, filepath.Join(dst, "feature.txt"))
}

func TestEnsureRepoUpdatesDefaultCloneInPlace(t *testing.T) {
	src := newSourceRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, EnsureRepo(dst, src.path, "", true, 0))
	marker := markClone(t, dst)
	src.commitFile("default2.txt", "more")

	require.NoError(t, EnsureRepo(dst, src.path, "", true, 0))

	assert.FileExists(t, filepath.Join(dst, "default2.txt"), "new commit on default branch not pulled")
	assert.FileExists(t, marker, "clone re-created instead of updated in place")
}

func TestEnsureRepoUpdatesBranchCloneInPlace(t *testing.T) {
	src := newSourceRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")
	url := src.path + "#feature"
	require.NoError(t, EnsureRepo(dst, url, "", true, 0))
	marker := markClone(t, dst)
	src.checkout("feature", false)
	src.commitFile("feature2.txt", "more")

	require.NoError(t, EnsureRepo(dst, url, "", true, 0))

	assert.FileExists(t, filepath.Join(dst, "feature2.txt"), "new commit on feature branch not pulled")
	assert.FileExists(t, marker, "clone re-created instead of updated in place")
}

func TestEnsureRepoSwitchesToSuffixBranch(t *testing.T) {
	for name, update := range map[string]bool{"update": true, "no update": false} {
		t.Run(name, func(t *testing.T) {
			src := newSourceRepo(t)
			dst := filepath.Join(t.TempDir(), "clone")
			require.NoError(t, EnsureRepo(dst, src.path, "", true, 0))

			require.NoError(t, EnsureRepo(dst, src.path+"#feature", "", update, 0))

			assert.Equal(t, "feature", headBranch(t, dst))
		})
	}
}

func TestEnsureRepoReturnsToDefaultBranchWhenSuffixRemoved(t *testing.T) {
	src := newSourceRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, EnsureRepo(dst, src.path+"#feature", "", true, 0))

	require.NoError(t, EnsureRepo(dst, src.path, "", true, 0))

	assert.Equal(t, src.defaultBranch, headBranch(t, dst))
}

func TestEnsureRepoKeepsCloneWhenSwitchFails(t *testing.T) {
	src := newSourceRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, EnsureRepo(dst, src.path, "", true, 0))

	require.Error(t, EnsureRepo(dst, src.path+"#does-not-exist", "", true, 0))

	assert.Equal(t, src.defaultBranch, headBranch(t, dst))
	assert.FileExists(t, filepath.Join(dst, "default.txt"))
}

func TestEnsureRepoSizeRecloneKeepsSuffixBranch(t *testing.T) {
	src := newSourceRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")
	url := src.path + "#feature"
	require.NoError(t, EnsureRepo(dst, url, "", true, 0))
	marker := markClone(t, dst)

	require.NoError(t, EnsureRepo(dst, url, "", true, 1))

	require.NoFileExists(t, marker, "clone not re-created despite exceeding size limit")
	assert.Equal(t, "feature", headBranch(t, dst))
	// Follow-up call finds the re-clone already on the suffix branch
	require.NoError(t, EnsureRepo(dst, url, "", false, 0))
	assert.Equal(t, "feature", headBranch(t, dst))
}

func TestGetRepoInfoStripsBranchSuffixFromURL(t *testing.T) {
	src := newSourceRepo(t)
	head, err := src.repo.Head()
	require.NoError(t, err)

	info, err := GetRepoInfo(src.path, "https://github.com/example/apps.git#feature")
	require.NoError(t, err)

	assert.Equal(t, "https://github.com/example/apps.git", info.URL)
	assert.Equal(t, "https://github.com/example/apps/commit/"+head.Hash().String(), info.CommitURL)
}

func TestReplaceRepoDirReplacesDestination(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	dst := filepath.Join(parent, "dst")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "new.txt"), nil, 0o644))
	require.NoError(t, os.MkdirAll(dst, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "old.txt"), nil, 0o644))

	require.NoError(t, replaceRepoDir(src, dst))

	assert.FileExists(t, filepath.Join(dst, "new.txt"))
	assert.NoFileExists(t, filepath.Join(dst, "old.txt"))
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Len(t, entries, 1, "src and any backup should be gone")
	assert.Equal(t, "dst", entries[0].Name())
}

func TestReplaceRepoDirKeepsDestinationWhenMoveFails(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "dst")
	require.NoError(t, os.MkdirAll(dst, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "old.txt"), nil, 0o644))

	require.Error(t, replaceRepoDir(filepath.Join(parent, "missing"), dst))

	assert.FileExists(t, filepath.Join(dst, "old.txt"))
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no backup should be left behind")
}
