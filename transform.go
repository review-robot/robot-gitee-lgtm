package main

import (
	"strings"
	"time"

	sdk "github.com/opensourceways/go-gitee/gitee"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
)

func convertGiteePR(v *sdk.PullRequest) *github.PullRequest {
	r := github.PullRequest{
		Head: github.PullRequestBranch{
			SHA: v.Head.Sha,
			Ref: v.Head.Ref,
		},
		Base: github.PullRequestBranch{
			Ref: v.Base.Ref,
			SHA: v.Base.Sha,
			Repo: github.Repo{
				Name: v.Base.Repo.Path,
				Owner: github.User{
					Login: v.Base.Repo.Namespace.Path,
				},
				HTMLURL:  v.Base.Repo.HtmlUrl,
				FullName: v.Base.Repo.FullName,
			},
		},
		User: github.User{
			Login:   v.User.Login,
			HTMLURL: v.User.HtmlUrl,
		},

		Number:  int(v.Number),
		HTMLURL: v.HtmlUrl,
		State:   v.State,
		Body:    v.Body,
		Title:   v.Title,
		ID:      int(v.Id),
	}
	return &r
}

func convertGiteePRComment(i sdk.PullRequestComments) github.IssueComment {
	ct, _ := time.Parse(time.RFC3339, i.CreatedAt)
	ut, _ := time.Parse(time.RFC3339, i.UpdatedAt)

	return github.IssueComment{
		ID:        int(i.Id),
		Body:      i.Body,
		User:      github.User{Login: i.User.Login},
		HTMLURL:   i.HtmlUrl,
		CreatedAt: ct,
		UpdatedAt: ut,
	}
}

func convertPullRequestEvent(e *sdk.PullRequestEvent) github.PullRequestEvent {
	var pe github.PullRequestEvent

	pr := e.GetPullRequest()
	pe.Action = convertPullRequestAction(e)
	pe.PullRequest.Base.Repo.Owner.Login = pr.GetBase().GetRepo().GetNameSpace()
	pe.PullRequest.Base.Repo.Owner.Name = pr.GetBase().GetRepo().GetPath()
	pe.PullRequest.User.Login = e.GetPRAuthor()
	pe.PullRequest.Number = int(e.GetPRNumber())
	pe.PullRequest.Head.SHA = pr.GetHead().GetSha()

	return pe
}

func convertPullRequestAction(e *sdk.PullRequestEvent) github.PullRequestEventAction {
	var a github.PullRequestEventAction

	switch strings.ToLower(e.GetAction()) {
	case "open":
		a = github.PullRequestActionOpened
	case "update":
		switch strings.ToLower(e.GetActionDesc()) {
		case "source_branch_changed": // change the pr's commits
			a = github.PullRequestActionSynchronize
		case "target_branch_changed": // change the branch to which this pr will be merged
			a = github.PullRequestActionEdited
		case "update_label":
			a = github.PullRequestActionLabeled
		}
	case "close":
		a = github.PullRequestActionClosed
	}

	return a
}

func convertLgtmConfig(c *botConfig) *plugins.Configuration {
	return &plugins.Configuration{
		Lgtm: []plugins.Lgtm{
			{
				Repos:            c.Repos,
				ReviewActsAsLgtm: c.ReviewActsAsLgtm,
				StoreTreeHash:    c.StoreTreeHash,
				StickyLgtmTeam:   c.StickyLgtmTeam,
			},
		},
		Owners: c.Owners,
	}
}
