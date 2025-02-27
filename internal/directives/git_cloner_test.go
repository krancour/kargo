package directives

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sosedoff/gitkit"
	"github.com/stretchr/testify/require"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/internal/controller/git"
	"github.com/akuity/kargo/internal/credentials"
	dirsdk "github.com/akuity/kargo/pkg/directives"
	builtins "github.com/akuity/kargo/pkg/x/directives/builtins"
)

func Test_gitCloner_validate(t *testing.T) {
	testCases := []struct {
		name             string
		config           dirsdk.Config
		expectedProblems []string
	}{
		{
			name:   "repoURL not specified",
			config: dirsdk.Config{},
			expectedProblems: []string{
				"(root): repoURL is required",
			},
		},
		{
			name: "repoURL is empty string",
			config: dirsdk.Config{
				"repoURL": "",
			},
			expectedProblems: []string{
				"repoURL: String length must be greater than or equal to 1",
			},
		},
		{
			name:   "no checkout specified",
			config: dirsdk.Config{},
			expectedProblems: []string{
				"(root): checkout is required",
			},
		},
		{
			name: "checkout is an empty array",
			config: dirsdk.Config{
				"checkout": []dirsdk.Config{},
			},
			expectedProblems: []string{
				"checkout: Array must have at least 1 items",
			},
		},
		{
			name: "checkout path is not specified",
			config: dirsdk.Config{
				"checkout": []dirsdk.Config{{}},
			},
			expectedProblems: []string{
				"checkout.0: path is required",
			},
		},
		{
			name: "checkout path is empty string",
			config: dirsdk.Config{
				"checkout": []dirsdk.Config{{
					"path": "",
				}},
			},
			expectedProblems: []string{
				"checkout.0.path: String length must be greater than or equal to 1",
			},
		},
		{
			name: "branch and commit are both specified",
			// These are meant to be mutually exclusive.
			config: dirsdk.Config{
				"checkout": []dirsdk.Config{{
					"branch": "fake-branch",
					"commit": "fake-commit",
				}},
			},
			expectedProblems: []string{
				"checkout.0: Must validate one and only one schema",
			},
		},
		{
			name: "branch and tag are both specified",
			// These are meant to be mutually exclusive.
			config: dirsdk.Config{
				"checkout": []dirsdk.Config{{
					"branch": "fake-branch",
					"tag":    "fake-tag",
				}},
			},
			expectedProblems: []string{
				"checkout.0: Must validate one and only one schema",
			},
		},
		{
			name: "commit and tag are both specified",
			// These are meant to be mutually exclusive.
			config: dirsdk.Config{
				"checkout": []dirsdk.Config{{
					"commit": "fake-commit",
					"tag":    "fake-tag",
				}},
			},
			expectedProblems: []string{
				"checkout.0: Must validate one and only one schema",
			},
		},
		{
			name: "valid kitchen sink",
			config: dirsdk.Config{
				"repoURL": "https://github.com/example/repo.git",
				"checkout": []dirsdk.Config{
					{
						"path": "/fake/path/0",
					},
					{
						"branch": "",
						"commit": "",
						"tag":    "",
						"path":   "/fake/path/1",
					},
					{
						"branch": "fake-branch",
						"path":   "/fake/path/2",
					},
					{
						"branch": "fake-branch",
						"commit": "",
						"tag":    "",
						"path":   "/fake/path/3",
					},
					{
						"commit": "fake-commit",
						"path":   "/fake/path/4",
					},
					{
						"branch": "",
						"commit": "fake-commit",
						"tag":    "",
						"path":   "/fake/path/5",
					},
					{
						"tag":  "fake-tag",
						"path": "/fake/path/6",
					},
					{
						"branch": "",
						"commit": "",
						"tag":    "fake-tag",
						"path":   "/fake/path/7",
					},
					{
						"path": "/fake/path/8",
					},
					{
						"branch": "",
						"commit": "",
						"tag":    "",
						"path":   "/fake/path/9",
					},
					{
						"path": "/fake/path/10",
					},
				},
			},
		},
	}

	cloner := newGitCloner()

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := cloner.validate(testCase.config)
			if len(testCase.expectedProblems) == 0 {
				require.NoError(t, err)
			} else {
				for _, problem := range testCase.expectedProblems {
					require.ErrorContains(t, err, problem)
				}
			}
		})
	}
}

func Test_gitCloner_clone(t *testing.T) {
	// Set up a test Git server in-process
	service := gitkit.New(
		gitkit.Config{
			Dir:        t.TempDir(),
			AutoCreate: true,
		},
	)
	require.NoError(t, service.Setup())
	server := httptest.NewServer(service)
	defer server.Close()

	// This is the URL of the "remote" repository
	testRepoURL := fmt.Sprintf("%s/test.git", server.URL)

	// Create some content and push it to the remote repository's default branch
	repo, err := git.Clone(testRepoURL, nil, nil)
	require.NoError(t, err)
	defer repo.Close()
	err = os.WriteFile(filepath.Join(repo.Dir(), "test.txt"), []byte("foo"), 0600)
	require.NoError(t, err)
	err = repo.AddAllAndCommit("Initial commit")
	require.NoError(t, err)
	err = repo.Push(nil)
	require.NoError(t, err)

	commitID, err := repo.LastCommitID()
	require.NoError(t, err)

	// Now we can proceed to test gitCloner...

	cloner := newGitCloner()

	stepCtx := &dirsdk.PromotionStepContext{
		WorkDir: t.TempDir(),
	}

	res, err := cloner.clone(
		context.WithValue(context.Background(), credentialsDBContextKey{}, &credentials.FakeDB{}),
		stepCtx,
		builtins.GitCloneConfig{
			RepoURL: fmt.Sprintf("%s/test.git", server.URL),
			Checkout: []builtins.Checkout{
				{
					Commit: commitID,
					Path:   "src",
				},
				{
					Branch: "stage/dev",
					Path:   "out",
					Create: true,
				},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, kargoapi.PromotionPhaseSucceeded, res.Status)
	require.DirExists(t, filepath.Join(stepCtx.WorkDir, "src"))
	// The checked out master branch should have the content we know is in the
	// test remote's master branch.
	require.FileExists(t, filepath.Join(stepCtx.WorkDir, "src", "test.txt"))
	require.DirExists(t, filepath.Join(stepCtx.WorkDir, "out"))
	// The stage/dev branch is a new orphan branch with a single empty commit.
	// It should lack any content.
	dirEntries, err := os.ReadDir(filepath.Join(stepCtx.WorkDir, "out"))
	require.NoError(t, err)
	require.Len(t, dirEntries, 1) // Just the .git file
	require.FileExists(t, filepath.Join(stepCtx.WorkDir, "out", ".git"))
}
