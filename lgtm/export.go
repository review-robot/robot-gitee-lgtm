package lgtm

import (
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/github"
)

func NewReviewCtx(author, issueAuthor, body, htmlURL string, repo github.Repo, assignees []github.User, number int) reviewCtx {
	return reviewCtx{
		author:      author,
		issueAuthor: issueAuthor,
		body:        body,
		htmlURL:     htmlURL,
		repo:        repo,
		assignees:   assignees,
		number:      number,
	}
}

var (
	Handle            = handle
	HandlePullRequest = handlePullRequest
	SkipCollaborators = skipCollaborators
	LoadRepoOwners    = loadRepoOwners
	GetChangedFiles   = getChangedFiles
)

type RepoOwners interface {
	LoadRepoOwners(org, repo, base string) (Repo, error)
}

type Repo interface {
	Approvers(path string) sets.String
	Reviewers(path string) sets.String
}
