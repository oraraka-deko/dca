package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
)

// GitManager errors.
var (
	ErrNotARepository  = errors.New("not a git repository")
	ErrNoHEAD          = errors.New("no HEAD reference")
	ErrNoRemote        = errors.New("no remote configured")
	ErrAlreadyUpToDate = errors.New("already up to date")
)

// GitManager manages a git repository, wrapping go-git's Repository and Worktree.
type GitManager struct {
	repo     *git.Repository
	worktree *git.Worktree
	repoPath string
}

// CloneOptions contains options for cloning a repository.
type CloneOptions struct {
	URL   string
	Path  string
	Depth int
	Bare  bool
}

// NewGitManager opens an existing git repository at the given path.
func NewGitManager(path string) (*GitManager, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository at %q: %w", absPath, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	return &GitManager{
		repo:     repo,
		worktree: wt,
		repoPath: absPath,
	}, nil
}

// InitGitManager initializes a new git repository.
func InitGitManager(path string, bare bool) (*GitManager, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	repo, err := git.PlainInit(absPath, bare)
	if err != nil {
		return nil, fmt.Errorf("failed to init repository: %w", err)
	}

	if bare {
		return &GitManager{
			repo:     repo,
			repoPath: absPath,
		}, nil
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	return &GitManager{
		repo:     repo,
		worktree: wt,
		repoPath: absPath,
	}, nil
}

// CloneGitManager clones a remote repository into the given path.
func CloneGitManager(ctx context.Context, opts *CloneOptions) (*GitManager, error) {
	absPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	cloneOpts := &git.CloneOptions{
		URL:   opts.URL,
		Depth: opts.Depth,
		Bare:  opts.Bare,
	}

	repo, err := git.PlainCloneContext(ctx, absPath, cloneOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to clone %q: %w", opts.URL, err)
	}

	if opts.Bare {
		return &GitManager{
			repo:     repo,
			repoPath: absPath,
		}, nil
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	return &GitManager{
		repo:     repo,
		worktree: wt,
		repoPath: absPath,
	}, nil
}

// Repository returns the underlying go-git Repository for advanced operations.
func (gm *GitManager) Repository() *git.Repository {
	return gm.repo
}

// Worktree returns the underlying go-git Worktree for advanced operations.
func (gm *GitManager) Worktree() *git.Worktree {
	return gm.worktree
}

// Path returns the absolute path to the repository root.
func (gm *GitManager) Path() string {
	return gm.repoPath
}

// IsBare returns true if the repository is bare.
func (gm *GitManager) IsBare() bool {
	return gm.worktree == nil
}

// --- Reference / HEAD operations ---

// Head returns the current HEAD reference.
func (gm *GitManager) Head() (*plumbing.Reference, error) {
	ref, err := gm.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, ErrNoHEAD
		}
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}
	return ref, nil
}

// Hash returns the current HEAD commit hash.
func (gm *GitManager) Hash() (plumbing.Hash, error) {
	ref, err := gm.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return ref.Hash(), nil
}

// Branch returns the current branch name, or empty string if detached.
func (gm *GitManager) Branch() (string, error) {
	ref, err := gm.Head()
	if err != nil {
		return "", err
	}
	if ref.Name().IsBranch() {
		return ref.Name().Short(), nil
	}
	return "", nil
}

// Detached returns true if HEAD is in detached state.
func (gm *GitManager) Detached() (bool, error) {
	ref, err := gm.Head()
	if err != nil {
		return false, err
	}
	return ref.Name() == plumbing.HEAD && ref.Type() == plumbing.HashReference, nil
}

// Reference resolves a reference by name.
func (gm *GitManager) Reference(name string, resolved bool) (*plumbing.Reference, error) {
	ref, err := gm.repo.Reference(plumbing.ReferenceName(name), resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve reference %q: %w", name, err)
	}
	return ref, nil
}

// --- Worktree operations ---

// Pull fetches from and integrates with the remote branch.
func (gm *GitManager) Pull(ctx context.Context) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot pull in a bare repository")
	}

	err := gm.worktree.PullContext(ctx, &git.PullOptions{})
	if err != nil {
		if errors.Is(err, git.NoErrAlreadyUpToDate) {
			return ErrAlreadyUpToDate
		}
		return fmt.Errorf("failed to pull: %w", err)
	}
	return nil
}

// PullWithOptions pulls with custom options.
func (gm *GitManager) PullWithOptions(ctx context.Context, opts *git.PullOptions) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot pull in a bare repository")
	}

	if opts == nil {
		opts = &git.PullOptions{}
	}

	err := gm.worktree.PullContext(ctx, opts)
	if err != nil {
		if errors.Is(err, git.NoErrAlreadyUpToDate) {
			return ErrAlreadyUpToDate
		}
		return fmt.Errorf("failed to pull: %w", err)
	}
	return nil
}

// Fetch fetches references from a remote without merging.
func (gm *GitManager) Fetch(ctx context.Context) error {
	err := gm.repo.FetchContext(ctx, &git.FetchOptions{})
	if err != nil {
		if errors.Is(err, git.NoErrAlreadyUpToDate) {
			return ErrAlreadyUpToDate
		}
		return fmt.Errorf("failed to fetch: %w", err)
	}
	return nil
}

// FetchWithOptions fetches with custom options.
func (gm *GitManager) FetchWithOptions(ctx context.Context, opts *git.FetchOptions) error {
	if opts == nil {
		opts = &git.FetchOptions{}
	}

	err := gm.repo.FetchContext(ctx, opts)
	if err != nil {
		if errors.Is(err, git.NoErrAlreadyUpToDate) {
			return ErrAlreadyUpToDate
		}
		return fmt.Errorf("failed to fetch: %w", err)
	}
	return nil
}

// Push pushes local commits to the remote.
func (gm *GitManager) Push(ctx context.Context) error {
	err := gm.repo.PushContext(ctx, &git.PushOptions{})
	if err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}
	return nil
}

// PushWithOptions pushes with custom options.
func (gm *GitManager) PushWithOptions(ctx context.Context, opts *git.PushOptions) error {
	if opts == nil {
		opts = &git.PushOptions{}
	}

	err := gm.repo.PushContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}
	return nil
}

// Checkout switches to the given branch.
func (gm *GitManager) Checkout(branch string, opts *git.CheckoutOptions) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot checkout in a bare repository")
	}

	if opts == nil {
		opts = &git.CheckoutOptions{}
	}

	if branch != "" {
		opts.Branch = plumbing.NewBranchReferenceName(branch)
	}

	if err := gm.worktree.Checkout(opts); err != nil {
		return fmt.Errorf("failed to checkout: %w", err)
	}
	return nil
}

// CheckoutHash checks out a specific commit hash (detached HEAD).
func (gm *GitManager) CheckoutHash(hash plumbing.Hash) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot checkout in a bare repository")
	}

	opts := &git.CheckoutOptions{Hash: hash}
	if err := gm.worktree.Checkout(opts); err != nil {
		return fmt.Errorf("failed to checkout hash %s: %w", hash.String(), err)
	}
	return nil
}

// Add stages one or more files.
func (gm *GitManager) Add(files ...string) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot add in a bare repository")
	}

	for _, file := range files {
		if _, err := gm.worktree.Add(file); err != nil {
			return fmt.Errorf("failed to add %q: %w", file, err)
		}
	}
	return nil
}

// Remove removes files from the worktree and stages the removal.
func (gm *GitManager) Remove(files ...string) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot remove in a bare repository")
	}

	for _, file := range files {
		if _, err := gm.worktree.Remove(file); err != nil {
			return fmt.Errorf("failed to remove %q: %w", file, err)
		}
	}
	return nil
}

// Commit creates a new commit with the given message and author info.
func (gm *GitManager) Commit(msg, author, email string) (plumbing.Hash, error) {
	if gm.worktree == nil {
		return plumbing.ZeroHash, fmt.Errorf("cannot commit in a bare repository")
	}

	if msg == "" {
		return plumbing.ZeroHash, fmt.Errorf("commit message is required")
	}

	if author == "" {
		author = "User"
	}
	if email == "" {
		email = "user@localhost"
	}

	opts := &git.CommitOptions{
		Author: &object.Signature{
			Name:  author,
			Email: email,
			When:  time.Now(),
		},
	}

	hash, err := gm.worktree.Commit(msg, opts)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to commit: %w", err)
	}
	return hash, nil
}

// Status returns the worktree status.
func (gm *GitManager) Status() (git.Status, error) {
	if gm.worktree == nil {
		return nil, fmt.Errorf("cannot get status in a bare repository")
	}

	status, err := gm.worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}
	return status, nil
}

// IsClean returns true if there are no uncommitted changes.
func (gm *GitManager) IsClean() (bool, error) {
	status, err := gm.Status()
	if err != nil {
		return false, err
	}
	return status.IsClean(), nil
}

// Reset resets the worktree to a given state.
func (gm *GitManager) Reset(opts *git.ResetOptions) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot reset in a bare repository")
	}

	if opts == nil {
		return fmt.Errorf("reset options are required")
	}

	if err := gm.worktree.Reset(opts); err != nil {
		return fmt.Errorf("failed to reset: %w", err)
	}
	return nil
}

// Clean removes untracked files from the worktree.
func (gm *GitManager) Clean(opts *git.CleanOptions) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot clean in a bare repository")
	}

	if opts == nil {
		opts = &git.CleanOptions{Dir: true}
	}

	if err := gm.worktree.Clean(opts); err != nil {
		return fmt.Errorf("failed to clean: %w", err)
	}
	return nil
}

// Restore restores files from the index or HEAD.
func (gm *GitManager) Restore(opts *git.RestoreOptions) error {
	if gm.worktree == nil {
		return fmt.Errorf("cannot restore in a bare repository")
	}

	if err := gm.worktree.Restore(opts); err != nil {
		return fmt.Errorf("failed to restore: %w", err)
	}
	return nil
}

// --- Branch operations ---

// Branches returns all local branches.
func (gm *GitManager) Branches() ([]*plumbing.Reference, error) {
	refIter, err := gm.repo.Branches()
	if err != nil {
		return nil, fmt.Errorf("failed to get branches: %w", err)
	}
	defer refIter.Close()

	var refs []*plumbing.Reference
	if err := refIter.ForEach(func(r *plumbing.Reference) error {
		refs = append(refs, r)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to iterate branches: %w", err)
	}

	return refs, nil
}

// CreateBranch creates a new branch at the current HEAD.
func (gm *GitManager) CreateBranch(name string) error {
	ref, err := gm.Head()
	if err != nil {
		return err
	}

	branchRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), ref.Hash())
	if err := gm.repo.Storer.SetReference(branchRef); err != nil {
		return fmt.Errorf("failed to create branch %q: %w", name, err)
	}
	return nil
}

// CreateBranchFrom creates a new branch from the given target.
func (gm *GitManager) CreateBranchFrom(name, target string) error {
	var hash plumbing.Hash

	if target == "" {
		ref, err := gm.Head()
		if err != nil {
			return err
		}
		hash = ref.Hash()
	} else {
		ref, err := gm.repo.Reference(plumbing.ReferenceName(target), true)
		if err != nil {
			h := plumbing.NewHash(target)
			if h.IsZero() {
				return fmt.Errorf("invalid target %q: %w", target, err)
			}
			hash = h
		} else {
			hash = ref.Hash()
		}
	}

	branchRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), hash)
	if err := gm.repo.Storer.SetReference(branchRef); err != nil {
		return fmt.Errorf("failed to create branch %q: %w", name, err)
	}
	return nil
}

// DeleteBranch removes a branch.
func (gm *GitManager) DeleteBranch(name string) error {
	refName := plumbing.NewBranchReferenceName(name)
	if err := gm.repo.Storer.RemoveReference(refName); err != nil {
		return fmt.Errorf("failed to delete branch %q: %w", name, err)
	}
	return nil
}

// --- Tag operations ---

// Tags returns all tags.
func (gm *GitManager) Tags() ([]*plumbing.Reference, error) {
	refIter, err := gm.repo.Tags()
	if err != nil {
		return nil, fmt.Errorf("failed to get tags: %w", err)
	}
	defer refIter.Close()

	var refs []*plumbing.Reference
	if err := refIter.ForEach(func(r *plumbing.Reference) error {
		refs = append(refs, r)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to iterate tags: %w", err)
	}

	return refs, nil
}

// CreateTag creates a lightweight tag at the current HEAD.
func (gm *GitManager) CreateTag(name string) error {
	ref, err := gm.Head()
	if err != nil {
		return err
	}

	_, err = gm.repo.CreateTag(name, ref.Hash(), nil)
	if err != nil {
		return fmt.Errorf("failed to create tag %q: %w", name, err)
	}
	return nil
}

// CreateAnnotatedTag creates an annotated tag.
func (gm *GitManager) CreateAnnotatedTag(name, message, author, email string) (*plumbing.Reference, error) {
	ref, err := gm.Head()
	if err != nil {
		return nil, err
	}

	if author == "" {
		author = "User"
	}
	if email == "" {
		email = "user@localhost"
	}

	return gm.repo.CreateTag(name, ref.Hash(), &git.CreateTagOptions{
		Tagger: &object.Signature{
			Name:  author,
			Email: email,
			When:  time.Now(),
		},
		Message: message,
	})
}

// DeleteTag removes a tag.
func (gm *GitManager) DeleteTag(name string) error {
	refName := plumbing.NewTagReferenceName(name)
	if err := gm.repo.Storer.RemoveReference(refName); err != nil {
		return fmt.Errorf("failed to delete tag %q: %w", name, err)
	}
	return nil
}

// --- Remote operations ---

// Remotes returns all configured remotes.
func (gm *GitManager) Remotes() ([]*git.Remote, error) {
	remotes, err := gm.repo.Remotes()
	if err != nil {
		return nil, fmt.Errorf("failed to get remotes: %w", err)
	}
	return remotes, nil
}

// Remote returns the remote by name.
func (gm *GitManager) Remote(name string) (*git.Remote, error) {
	remote, err := gm.repo.Remote(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote %q: %w", name, err)
	}
	return remote, nil
}

// RemoteURL returns the URL of the default remote (origin).
func (gm *GitManager) RemoteURL() (string, error) {
	remote, err := gm.repo.Remote(git.DefaultRemoteName)
	if err != nil {
		return "", ErrNoRemote
	}

	cfg := remote.Config()
	if cfg == nil || len(cfg.URLs) == 0 {
		return "", ErrNoRemote
	}
	return cfg.URLs[0], nil
}

// AddRemote adds a new remote.
func (gm *GitManager) AddRemote(name, url string) error {
	_, err := gm.repo.CreateRemote(&config.RemoteConfig{
		Name: name,
		URLs: []string{url},
	})
	if err != nil {
		return fmt.Errorf("failed to add remote %q: %w", name, err)
	}
	return nil
}

// DeleteRemote removes a remote.
func (gm *GitManager) DeleteRemote(name string) error {
	if err := gm.repo.DeleteRemote(name); err != nil {
		return fmt.Errorf("failed to delete remote %q: %w", name, err)
	}
	return nil
}

// ListRemoteReferences lists the references on the remote (like git ls-remote).
func (gm *GitManager) ListRemoteReferences(ctx context.Context, name string, opts *git.ListOptions) ([]*plumbing.Reference, error) {
	remote, err := gm.Remote(name)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &git.ListOptions{}
	}
	return remote.ListContext(ctx, opts)
}

// --- Log / Commit history ---

// Log returns the commit history, up to count entries (0 = all).
func (gm *GitManager) Log(count int) ([]*object.Commit, error) {
	ref, err := gm.Head()
	if err != nil {
		return nil, err
	}

	iter, err := gm.repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, fmt.Errorf("failed to get log: %w", err)
	}
	defer iter.Close()

	var commits []*object.Commit
	if err := iter.ForEach(func(c *object.Commit) error {
		if count > 0 && len(commits) >= count {
			return storer.ErrStop
		}
		commits = append(commits, c)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}

	return commits, nil
}

// LogFrom returns commit history starting from a specific hash.
func (gm *GitManager) LogFrom(hash plumbing.Hash, count int) ([]*object.Commit, error) {
	iter, err := gm.repo.Log(&git.LogOptions{From: hash})
	if err != nil {
		return nil, fmt.Errorf("failed to get log: %w", err)
	}
	defer iter.Close()

	var commits []*object.Commit
	if err := iter.ForEach(func(c *object.Commit) error {
		if count > 0 && len(commits) >= count {
			return storer.ErrStop
		}
		commits = append(commits, c)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}

	return commits, nil
}

// --- Diff operations ---

// Diff returns the changes between two commits.
func (gm *GitManager) Diff(from, to plumbing.Hash) (object.Changes, error) {
	fromCommit, err := gm.repo.CommitObject(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get from commit: %w", err)
	}

	toCommit, err := gm.repo.CommitObject(to)
	if err != nil {
		return nil, fmt.Errorf("failed to get to commit: %w", err)
	}

	fromTree, err := fromCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get from tree: %w", err)
	}

	toTree, err := toCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get to tree: %w", err)
	}

	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return nil, fmt.Errorf("failed to diff trees: %w", err)
	}

	return changes, nil
}

// DiffHEAD returns changes between HEAD and the given commit.
func (gm *GitManager) DiffHEAD(to plumbing.Hash) (object.Changes, error) {
	ref, err := gm.Head()
	if err != nil {
		return nil, err
	}
	return gm.Diff(ref.Hash(), to)
}

// --- Read operations ---

// ReadFileFromCommit reads a file's content from a specific commit.
func (gm *GitManager) ReadFileFromCommit(commitHash plumbing.Hash, filePath string) (string, error) {
	commit, err := gm.repo.CommitObject(commitHash)
	if err != nil {
		return "", fmt.Errorf("failed to get commit: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("failed to get tree: %w", err)
	}

	file, err := tree.File(filePath)
	if err != nil {
		return "", fmt.Errorf("file %q not found: %w", filePath, err)
	}

	content, err := file.Contents()
	if err != nil {
		return "", fmt.Errorf("failed to read file contents: %w", err)
	}

	return content, nil
}

// ReadFileFromHEAD reads a file's content from HEAD.
func (gm *GitManager) ReadFileFromHEAD(filePath string) (string, error) {
	ref, err := gm.Head()
	if err != nil {
		return "", err
	}
	return gm.ReadFileFromCommit(ref.Hash(), filePath)
}

// ReadFileFromBranch reads a file from the tip of a branch.
func (gm *GitManager) ReadFileFromBranch(branch, filePath string) (string, error) {
	refName := plumbing.NewBranchReferenceName(branch)
	ref, err := gm.repo.Reference(refName, true)
	if err != nil {
		return "", fmt.Errorf("branch %q not found: %w", branch, err)
	}

	return gm.ReadFileFromCommit(ref.Hash(), filePath)
}

// CommitCount returns the total number of commits in the current branch.
func (gm *GitManager) CommitCount() (int, error) {
	ref, err := gm.Head()
	if err != nil {
		return 0, err
	}

	iter, err := gm.repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return 0, fmt.Errorf("failed to get log: %w", err)
	}
	defer iter.Close()

	count := 0
	if err := iter.ForEach(func(*object.Commit) error {
		count++
		return nil
	}); err != nil {
		return 0, fmt.Errorf("failed to count commits: %w", err)
	}

	return count, nil
}

// References returns all references in the repository.
func (gm *GitManager) References() ([]*plumbing.Reference, error) {
	refIter, err := gm.repo.References()
	if err != nil {
		return nil, fmt.Errorf("failed to get references: %w", err)
	}
	defer refIter.Close()

	var refs []*plumbing.Reference
	if err := refIter.ForEach(func(r *plumbing.Reference) error {
		refs = append(refs, r)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to iterate references: %w", err)
	}

	return refs, nil
}

// Merge performs a fast-forward merge of the given reference into the current branch.
func (gm *GitManager) Merge(ref plumbing.Reference, opts git.MergeOptions) error {
	if err := gm.repo.Merge(ref, opts); err != nil {
		return fmt.Errorf("failed to merge %s: %w", ref.Name(), err)
	}
	return nil
}

// ResolveRevision resolves a revision string (e.g. "HEAD~1", "main", "v1.0") to a commit hash.
func (gm *GitManager) ResolveRevision(rev plumbing.Revision) (*plumbing.Hash, error) {
	h, err := gm.repo.ResolveRevision(rev)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve revision %q: %w", rev.String(), err)
	}
	return h, nil
}

// Archive creates an archive (tar/zip) from a tree-ish reference.
func (gm *GitManager) Archive(ctx context.Context, opts *git.ArchiveOptions) (io.ReadCloser, error) {
	return gm.repo.ArchiveContext(ctx, opts)
}

// RepackObjects repacks all objects in the repository into a single packfile.
func (gm *GitManager) RepackObjects(cfg *git.RepackConfig) error {
	if err := gm.repo.RepackObjects(cfg); err != nil {
		return fmt.Errorf("failed to repack objects: %w", err)
	}
	return nil
}

// Notes returns all note references.
func (gm *GitManager) Notes() ([]*plumbing.Reference, error) {
	refIter, err := gm.repo.Notes()
	if err != nil {
		return nil, fmt.Errorf("failed to get notes: %w", err)
	}
	defer refIter.Close()

	var refs []*plumbing.Reference
	if err := refIter.ForEach(func(r *plumbing.Reference) error {
		refs = append(refs, r)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to iterate notes: %w", err)
	}

	return refs, nil
}

// CommitObject returns a commit with the given hash.
func (gm *GitManager) CommitObject(h plumbing.Hash) (*object.Commit, error) {
	return gm.repo.CommitObject(h)
}

// TreeObject returns a tree with the given hash.
func (gm *GitManager) TreeObject(h plumbing.Hash) (*object.Tree, error) {
	return gm.repo.TreeObject(h)
}

// TagObject returns an annotated tag with the given hash.
func (gm *GitManager) TagObject(h plumbing.Hash) (*object.Tag, error) {
	return gm.repo.TagObject(h)
}

// BlobObject returns a blob with the given hash.
func (gm *GitManager) BlobObject(h plumbing.Hash) (*object.Blob, error) {
	return gm.repo.BlobObject(h)
}

// DeleteObject deletes a loose object from the repository.
func (gm *GitManager) DeleteObject(hash plumbing.Hash) error {
	if err := gm.repo.DeleteObject(hash); err != nil {
		return fmt.Errorf("failed to delete object %s: %w", hash.String(), err)
	}
	return nil
}

// Prune removes unreferenced loose objects from the repository.
func (gm *GitManager) Prune(opts git.PruneOptions) error {
	if err := gm.repo.Prune(opts); err != nil {
		return fmt.Errorf("failed to prune objects: %w", err)
	}
	return nil
}

// --- Submodule operations ---

// Submodule returns the submodule with the given name.
func (gm *GitManager) Submodule(name string) (*git.Submodule, error) {
	if gm.worktree == nil {
		return nil, fmt.Errorf("cannot access submodules in a bare repository")
	}

	sub, err := gm.worktree.Submodule(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get submodule %q: %w", name, err)
	}
	return sub, nil
}

// Submodules returns all submodules.
func (gm *GitManager) Submodules() (git.Submodules, error) {
	if gm.worktree == nil {
		return nil, fmt.Errorf("cannot access submodules in a bare repository")
	}

	subs, err := gm.worktree.Submodules()
	if err != nil {
		return nil, fmt.Errorf("failed to get submodules: %w", err)
	}
	return subs, nil
}

// InitSubmodule initializes a submodule by name.
func (gm *GitManager) InitSubmodule(name string) error {
	sub, err := gm.Submodule(name)
	if err != nil {
		return err
	}
	return sub.Init()
}

// InitSubmodules initializes all submodules.
func (gm *GitManager) InitSubmodules() error {
	subs, err := gm.Submodules()
	if err != nil {
		return err
	}
	return subs.Init()
}

// UpdateSubmodule updates a submodule to the commit recorded in the superproject.
func (gm *GitManager) UpdateSubmodule(ctx context.Context, name string, opts *git.SubmoduleUpdateOptions) error {
	sub, err := gm.Submodule(name)
	if err != nil {
		return err
	}
	if opts == nil {
		opts = &git.SubmoduleUpdateOptions{}
	}
	return sub.UpdateContext(ctx, opts)
}

// UpdateSubmodules updates all submodules.
func (gm *GitManager) UpdateSubmodules(ctx context.Context, opts *git.SubmoduleUpdateOptions) error {
	subs, err := gm.Submodules()
	if err != nil {
		return err
	}
	if opts == nil {
		opts = &git.SubmoduleUpdateOptions{}
	}
	return subs.UpdateContext(ctx, opts)
}

// SubmoduleStatus returns the status of all submodules.
func (gm *GitManager) SubmoduleStatus() (git.SubmodulesStatus, error) {
	subs, err := gm.Submodules()
	if err != nil {
		return nil, err
	}
	return subs.Status()
}

// Config returns the repository configuration.
func (gm *GitManager) Config() (*config.Config, error) {
	cfg, err := gm.repo.Config()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	return cfg, nil
}

// SetConfig sets the repository configuration.
func (gm *GitManager) SetConfig(cfg *config.Config) error {
	if err := gm.repo.SetConfig(cfg); err != nil {
		return fmt.Errorf("failed to set config: %w", err)
	}
	return nil
}
