package main

import (
	"fmt"
	"github.com/opensourceways/repo-owners-cache/repoowners"
	"sort"

	"github.com/opensourceways/repo-owners-cache/grpc/client"
	"k8s.io/test-infra/prow/github"

	"github.com/opensourceways/robot-gitee-lgtm/lgtm"
)

type ghClient struct {
	cli iClient
}

func (c *ghClient) IsCollaborator(owner, repo, login string) (bool, error) {
	return c.cli.IsCollaborator(owner, repo, login)
}

func (c *ghClient) AddLabel(owner, repo string, number int, label string) error {
	return c.cli.AddPRLabel(owner, repo, int32(number), label)
}

func (c *ghClient) AssignIssue(owner, repo string, number int, assignees []string) error {
	return c.cli.AssignPR(owner, repo, int32(number), assignees)
}

func (c *ghClient) CreateComment(owner, repo string, number int, comment string) error {
	return c.cli.CreatePRComment(owner, repo, int32(number), comment)
}

func (c *ghClient) RemoveLabel(owner, repo string, number int, label string) error {
	return c.cli.RemovePRLabel(owner, repo, int32(number), label)
}

func (c *ghClient) GetIssueLabels(org, repo string, number int) ([]github.Label, error) {
	var r []github.Label

	lbs, err := c.cli.GetPRLabels(org, repo, int32(number))
	if err != nil {
		return nil, err
	}

	for _, v := range lbs {
		r = append(r, github.Label{Name: v.Name})
	}

	return r, nil
}

func (c *ghClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	v, err := c.cli.GetGiteePullRequest(org, repo, int32(number))
	if err != nil {
		return nil, err
	}

	return convertGiteePR(&v), nil
}

func (c *ghClient) GetPullRequestChanges(org, repo string, number int) ([]github.PullRequestChange, error) {
	changes, err := c.cli.GetPullRequestChanges(org, repo, int32(number))
	if err != nil {
		return nil, err
	}

	var r []github.PullRequestChange

	for _, f := range changes {
		r = append(r, github.PullRequestChange{Filename: f.Filename})
	}

	return r, nil
}

func (c *ghClient) ListIssueComments(org, repo string, number int) ([]github.IssueComment, error) {
	var r []github.IssueComment

	v, err := c.cli.ListPRComments(org, repo, int32(number))
	if err != nil {
		return r, err
	}

	for _, i := range v {
		r = append(r, convertGiteePRComment(i))
	}

	sort.SliceStable(r, func(i, j int) bool {
		return r[i].CreatedAt.Before(r[j].CreatedAt)
	})

	return r, nil
}

func (c *ghClient) DeleteComment(org, repo string, ID int) error {
	return c.cli.DeletePRComment(org, repo, int32(ID))
}

func (c *ghClient) BotName() (string, error) {
	bot, err := c.cli.GetBot()
	if err != nil {
		return "", err
	}

	return bot.Login, nil
}

func (c *ghClient) GetSingleCommit(org, repo, SHA string) (github.SingleCommit, error) {
	var r github.SingleCommit

	v, err := c.cli.GetPRCommit(org, repo, SHA)
	if err != nil {
		return r, err
	}

	if v.Commit == nil || v.Commit.Tree == nil {
		return r, fmt.Errorf("single commit(%s/%s/%s) data is abnormal: %+v", org, repo, SHA, v)
	}

	r.Commit.Tree.SHA = v.Commit.Tree.Sha
	return r, nil
}

func (c *ghClient) IsMember(_, _ string) (bool, error) {
	return false, nil
}

func (c *ghClient) ListTeams(_ string) ([]github.Team, error) {
	return []github.Team{}, nil
}

func (c *ghClient) ListTeamMembers(_ int, _ string) ([]github.TeamMember, error) {
	return []github.TeamMember{}, nil
}

func (c *ghClient) UpdatePRComment(org, repo string, commentID int, comment string) error {
	return c.cli.UpdatePRComment(org, repo, int32(commentID), comment)
}

func newGHClient(cli iClient) *ghClient {
	return &ghClient{cli: cli}
}

type RepoOwnersClient struct {
	cli *client.Client
}

func (ro *RepoOwnersClient) LoadRepoOwners(org, repo, base string) (lgtm.Repo, error) {
	return repoowners.NewRepoOwners(
		repoowners.RepoBranch{
			Platform: "gitee",
			Org:      org,
			Repo:     repo,
			Branch:   base,
		}, ro.cli,
	)
}

func newRepoOwnersClient(cli *client.Client) *RepoOwnersClient {
	return &RepoOwnersClient{
		cli: cli,
	}
}
