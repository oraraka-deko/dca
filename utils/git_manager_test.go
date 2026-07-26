package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func createTestRepo(t *testing.T) (*GitManager, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "git_manager_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	gm, err := InitGitManager(tempDir, false)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to init git repo: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return gm, cleanup
}

func writeRepoFile(t *testing.T, gm *GitManager, name, content string) {
	t.Helper()
	path := filepath.Join(gm.repoPath, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
}

func TestInitGitManager(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	if gm.repoPath == "" {
		t.Error("expected non-empty repo path")
	}
	if gm.IsBare() {
		t.Error("expected non-bare repo")
	}
	if gm.repo == nil {
		t.Error("expected non-nil repository")
	}
	if gm.worktree == nil {
		t.Error("expected non-nil worktree")
	}
}

func TestInitGitManager_Bare(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "git_manager_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gm, err := InitGitManager(tempDir, true)
	if err != nil {
		t.Fatalf("InitGitManager bare failed: %v", err)
	}

	if !gm.IsBare() {
		t.Error("expected bare repo")
	}
}

func TestPath(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	if gm.Path() != gm.repoPath {
		t.Errorf("Path mismatch: %q vs %q", gm.Path(), gm.repoPath)
	}
}

func TestHead_EmptyRepo(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	_, err := gm.Head()
	if err == nil {
		t.Error("expected ErrNoHEAD for empty repo")
	}
}

func TestStatus_EmptyRepo(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	status, err := gm.Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status == nil {
		t.Error("expected non-nil status")
	}
}

func TestAddAndCommit(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "README.md", "# Test Repo")
	writeRepoFile(t, gm, "main.go", "package main\n")

	if err := gm.Add("README.md", "main.go"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	hash, err := gm.Commit("initial commit", "Test User", "test@example.com")
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	if hash.IsZero() {
		t.Error("expected non-zero commit hash")
	}
}

func TestCommit_EmptyMessage(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	_ = gm.Add("file.txt")

	_, err := gm.Commit("", "User", "user@example.com")
	if err == nil {
		t.Error("expected error for empty commit message")
	}
}

func TestIsClean(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	clean, err := gm.IsClean()
	if err != nil {
		t.Fatalf("IsClean failed: %v", err)
	}
	if !clean {
		t.Error("empty repo should be clean")
	}

	writeRepoFile(t, gm, "dirty.txt", "content")
	clean, err = gm.IsClean()
	if err != nil {
		t.Fatalf("IsClean failed: %v", err)
	}
	if clean {
		t.Error("repo with untracked file should not be clean")
	}
}

func TestCommit_ThenLog(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "a.txt", "a")
	if err := gm.Add("a.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("first", "User", "user@test.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writeRepoFile(t, gm, "b.txt", "b")
	if err := gm.Add("b.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("second", "User", "user@test.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	commits, err := gm.Log(0)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	if len(commits) != 2 {
		t.Errorf("expected 2 commits, got %d", len(commits))
	}

	if commits[0].Message != "second" {
		t.Errorf("expected 'second', got %q", commits[0].Message)
	}
	if commits[1].Message != "first" {
		t.Errorf("expected 'first', got %q", commits[1].Message)
	}
}

func TestCommit_ThenBranch(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	if err := gm.Add("file.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "User", "user@test.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	branch, err := gm.Branch()
	if err != nil {
		t.Fatalf("Branch failed: %v", err)
	}
	if branch == "" {
		t.Fatal("expected branch name after initial commit")
	}
}

func TestLog_Limit(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		fileName := fmt.Sprintf("file%d.txt", i)
		writeRepoFile(t, gm, fileName, "data")
		if err := gm.Add(fileName); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
		if _, err := gm.Commit(fmt.Sprintf("commit %d", i), "U", "u@t.com"); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
	}

	commits, err := gm.Log(2)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	if len(commits) != 2 {
		t.Errorf("expected 2 commits with limit, got %d", len(commits))
	}
}

func TestCreateBranch(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	if err := gm.Add("file.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := gm.CreateBranch("feature"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	branches, err := gm.Branches()
	if err != nil {
		t.Fatalf("Branches failed: %v", err)
	}

	found := false
	for _, ref := range branches {
		if ref.Name().Short() == "feature" {
			found = true
			break
		}
	}
	if !found {
		t.Error("feature branch not found in branch list")
	}
}

func TestDeleteBranch(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	if err := gm.Add("file.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := gm.CreateBranch("to-delete"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
	if err := gm.DeleteBranch("to-delete"); err != nil {
		t.Fatalf("DeleteBranch failed: %v", err)
	}

	branches, _ := gm.Branches()
	for _, ref := range branches {
		if ref.Name().Short() == "to-delete" {
			t.Error("deleted branch still present")
		}
	}
}

func TestCreateTag(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	if err := gm.Add("file.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := gm.CreateTag("v1.0"); err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	tags, err := gm.Tags()
	if err != nil {
		t.Fatalf("Tags failed: %v", err)
	}
	found := false
	for _, ref := range tags {
		if ref.Name().Short() == "v1.0" {
			found = true
			break
		}
	}
	if !found {
		t.Error("v1.0 tag not found in tag list")
	}
}

func TestCreateAnnotatedTag(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	if err := gm.Add("file.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ref, err := gm.CreateAnnotatedTag("v1.1", "Release v1.1", "User", "user@test.com")
	if err != nil {
		t.Fatalf("CreateAnnotatedTag failed: %v", err)
	}
	if ref == nil {
		t.Error("expected non-nil tag reference")
	}
	if ref.Name().Short() != "v1.1" {
		t.Errorf("expected tag name 'v1.1', got %s", ref.Name().Short())
	}
}

func TestDeleteTag(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	if err := gm.Add("file.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := gm.CreateTag("to-remove"); err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}
	if err := gm.DeleteTag("to-remove"); err != nil {
		t.Fatalf("DeleteTag failed: %v", err)
	}

	tags, _ := gm.Tags()
	for _, ref := range tags {
		if ref.Name().Short() == "to-remove" {
			t.Error("deleted tag still present")
		}
	}
}

func TestReadFileFromHEAD(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "data.txt", "hello world")
	if err := gm.Add("data.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("add data", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	content, err := gm.ReadFileFromHEAD("data.txt")
	if err != nil {
		t.Fatalf("ReadFileFromHEAD failed: %v", err)
	}
	if content != "hello world" {
		t.Errorf("expected 'hello world', got %q", content)
	}
}

func TestReadFileFromCommit(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "data.txt", "version 1")
	if err := gm.Add("data.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	h1, err := gm.Commit("first", "U", "u@t.com")
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writeRepoFile(t, gm, "data.txt", "version 2")
	if err := gm.Add("data.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("second", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	content, err := gm.ReadFileFromCommit(h1, "data.txt")
	if err != nil {
		t.Fatalf("ReadFileFromCommit failed: %v", err)
	}
	if content != "version 1" {
		t.Errorf("expected 'version 1', got %q", content)
	}
}

func TestReadFileFromBranch(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "data.txt", "main content")
	if err := gm.Add("data.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := gm.CreateBranch("dev"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	content, err := gm.ReadFileFromBranch("dev", "data.txt")
	if err != nil {
		t.Fatalf("ReadFileFromBranch failed: %v", err)
	}
	if content != "main content" {
		t.Errorf("expected 'main content', got %q", content)
	}
}

func TestCommitCount(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		writeRepoFile(t, gm, name, "x")
		if err := gm.Add(name); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
		if _, err := gm.Commit(fmt.Sprintf("c%d", i), "U", "u@t.com"); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
	}

	count, err := gm.CommitCount()
	if err != nil {
		t.Fatalf("CommitCount failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 commits, got %d", count)
	}
}

func TestReferences(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "file.txt", "content")
	if err := gm.Add("file.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	refs, err := gm.References()
	if err != nil {
		t.Fatalf("References failed: %v", err)
	}
	if len(refs) == 0 {
		t.Error("expected non-empty reference list")
	}

	hasHead := false
	for _, ref := range refs {
		if ref.Name() == "HEAD" {
			hasHead = true
			break
		}
	}
	if !hasHead {
		t.Error("HEAD reference not found")
	}
}

func TestDiff(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "a")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	h1, err := gm.Commit("first", "U", "u@t.com")
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writeRepoFile(t, gm, "f.txt", "b")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	h2, err := gm.Commit("second", "U", "u@t.com")
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	changes, err := gm.Diff(h1, h2)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	action, err := changes[0].Action()
	if err != nil {
		t.Fatalf("Action failed: %v", err)
	}
	if action.String() != "Modify" {
		t.Errorf("expected Modify action, got %s", action.String())
	}
}

func TestConfig(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	cfg, err := gm.Config()
	if err != nil {
		t.Fatalf("Config failed: %v", err)
	}
	if cfg == nil {
		t.Error("expected non-nil config")
	}
}

func TestSetConfig(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	cfg, err := gm.Config()
	if err != nil {
		t.Fatalf("Config failed: %v", err)
	}

	cfg.User.Name = "test-user"
	cfg.User.Email = "test@example.com"

	if err := gm.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	reloaded, err := gm.Config()
	if err != nil {
		t.Fatalf("Config reload failed: %v", err)
	}
	if reloaded.User.Name != "test-user" {
		t.Errorf("expected User.Name 'test-user', got %q", reloaded.User.Name)
	}
	if reloaded.User.Email != "test@example.com" {
		t.Errorf("expected User.Email 'test@example.com', got %q", reloaded.User.Email)
	}
}

func TestRemove(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "remove-me.txt", "delete me")
	if err := gm.Add("remove-me.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("add file", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := gm.Remove("remove-me.txt"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	status, err := gm.Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.IsClean() {
		t.Error("expected status not clean after remove")
	}
}

func TestCheckout(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "main")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := gm.CreateBranch("alt"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	branch, err := gm.Branch()
	if err != nil {
		t.Fatalf("Branch failed: %v", err)
	}
	if branch != "master" {
		t.Fatalf("expected on master, got %q", branch)
	}

	if err := gm.Checkout("alt", nil); err != nil {
		t.Fatalf("Checkout failed: %v", err)
	}

	branch, err = gm.Branch()
	if err != nil {
		t.Fatalf("Branch failed: %v", err)
	}
	if branch != "alt" {
		t.Errorf("expected on alt branch, got %q", branch)
	}
}

func TestRemote_AddListDelete(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	if err := gm.AddRemote("origin", "https://example.com/repo.git"); err != nil {
		t.Fatalf("AddRemote failed: %v", err)
	}

	remotes, err := gm.Remotes()
	if err != nil {
		t.Fatalf("Remotes failed: %v", err)
	}
	if len(remotes) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(remotes))
	}
	if remotes[0].Config().Name != "origin" {
		t.Errorf("expected remote name 'origin', got %q", remotes[0].Config().Name)
	}

	remote, err := gm.Remote("origin")
	if err != nil {
		t.Fatalf("Remote failed: %v", err)
	}
	if remote.Config().URLs[0] != "https://example.com/repo.git" {
		t.Errorf("unexpected URL: %q", remote.Config().URLs[0])
	}

	url, err := gm.RemoteURL()
	if err != nil {
		t.Fatalf("RemoteURL failed: %v", err)
	}
	if url != "https://example.com/repo.git" {
		t.Errorf("unexpected default remote URL: %q", url)
	}

	if err := gm.DeleteRemote("origin"); err != nil {
		t.Fatalf("DeleteRemote failed: %v", err)
	}

	remotes, err = gm.Remotes()
	if err != nil {
		t.Fatalf("Remotes failed: %v", err)
	}
	if len(remotes) != 0 {
		t.Errorf("expected 0 remotes after delete, got %d", len(remotes))
	}
}
