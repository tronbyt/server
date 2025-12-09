package gitutils

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// RepoInfo holds details about a Git repository.
type RepoInfo struct {
	URL           string
	Branch        string
	CommitHash    string
	CommitURL     string
	CommitMessage string
	CommitDate    string // Formatted date string
}

// GetRepoInfo retrieves detailed information about a local Git repository.
func GetRepoInfo(path string, remoteURL string) (*RepoInfo, error) {
	r, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open repo at %s: %w", path, err)
	}

	headRef, err := r.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference: %w", err)
	}

	commit, err := r.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object: %w", err)
	}

	branchName := ""
	iter, err := r.Branches()
	if err == nil {
		err = iter.ForEach(func(ref *plumbing.Reference) error {
			if ref.Hash() == headRef.Hash() {
				branchName = ref.Name().Short()
				return fmt.Errorf("found") // Hack to exit ForEach
			}
			return nil
		})
		if err != nil && err.Error() != "found" {
			slog.Debug("Failed to find branch for HEAD", "error", err)
		}
	}

	// Try to determine a GitHub commit URL
	commitURL := ""
	if remoteURL != "" && strings.Contains(remoteURL, "github.com") {
		// Assuming GitHub URL format
		repoPath := strings.TrimPrefix(remoteURL, "https://github.com/")
		repoPath = strings.TrimSuffix(repoPath, ".git")
		commitURL = fmt.Sprintf("https://github.com/%s/commit/%s", repoPath, commit.Hash.String())
	}

	return &RepoInfo{
		URL:           remoteURL,
		Branch:        branchName,
		CommitHash:    commit.Hash.String(),
		CommitURL:     commitURL,
		CommitMessage: strings.SplitN(commit.Message, "\n", 2)[0], // First line of message
		CommitDate:    commit.Author.When.Format("2006-01-02 15:04"),
	}, nil
}

// EnsureRepo clones a repo if it doesn't exist, or pulls if it does and update is true.
func EnsureRepo(path string, url string, update bool) error {
	slog.Info("Checking git repo", "path", path, "url", url)

	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		slog.Info("Cloning repo", "url", url)
		_, err := git.PlainClone(path, false, &git.CloneOptions{
			URL:      url,
			Progress: os.Stdout,
			Depth:    1,
		})
		return err
	}

	// Repo exists, open it
	r, err := git.PlainOpen(path)
	if err != nil {
		// If not a git repo, maybe remove and re-clone?
		// For safety, error out.
		return fmt.Errorf("failed to open repo: %w", err)
	}

	// Check remote URL
	rem, err := r.Remote("origin")
	if err == nil {
		urls := rem.Config().URLs
		if len(urls) > 0 && urls[0] != url {
			slog.Warn("Repo remote URL mismatch, re-cloning", "current", urls[0], "new", url)
			// Remove and re-clone
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("failed to remove old repo: %w", err)
			}
			return EnsureRepo(path, url, update)
		}
	}

	if !update {
		slog.Info("Skipping repo update (update=false)")
		return nil
	}

	// Pull
	w, err := r.Worktree()
	if err != nil {
		return err
	}

	// Reset to HEAD (Hard) to discard local changes
	if err := w.Reset(&git.ResetOptions{Mode: git.HardReset}); err != nil {
		slog.Warn("Failed to hard reset repo", "error", err)
	}
	// Clean untracked files
	if err := w.Clean(&git.CleanOptions{Dir: true}); err != nil {
		slog.Warn("Failed to clean repo", "error", err)
	}

	slog.Info("Pulling repo")
	err = w.Pull(&git.PullOptions{
		RemoteName: "origin",
		Progress:   os.Stdout,
	})

	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		slog.Info("Repo already up to date")
		return nil
	}

	return err
}
