package main

import (
	"fmt"
	"sort"
	"time"

	sdk "github.com/opensourceways/go-gitee/gitee"
	"github.com/opensourceways/repo-owners-cache/grpc/client"
	"github.com/opensourceways/repo-owners-cache/repoowners"
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

func (c *ghClient) ListIssueComments(org, repo string, number int) ([]issueComment, error) {
	var r []issueComment

	v, err := c.cli.ListPRComments(org, repo, int32(number))
	if err != nil {
		return r, err
	}

	for _, i := range v {
		r = append(r, convertPRComment(i))
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

func (c *ghClient) GetSingleCommit(org, repo, SHA string) (string, error) {
	v, err := c.cli.GetPRCommit(org, repo, SHA)
	if err != nil {
		return "", err
	}

	if v.Commit == nil {
		return "", fmt.Errorf("single commit(%s/%s/%s) data is abnormal: %+v", org, repo, SHA, v)
	}

	return v.Commit.Tree.GetSha(), nil
}

func (c *ghClient) IsMember(_, _ string) (bool, error) {
	return false, nil
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

func (ro *RepoOwnersClient) LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error) {
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

func getChangedFiles(gc *ghClient, org, repo string, number int) ([]string, error) {
	changes, err := gc.cli.GetPullRequestChanges(org, repo, int32(number))
	if err != nil {
		return nil, fmt.Errorf("cannot get PR changes for %s/%s#%d", org, repo, number)
	}
	var filenames []string
	for _, change := range changes {
		filenames = append(filenames, change.Filename)
	}
	return filenames, nil
}

func normalizeLogin(s string) string {
	return ""
}

type issueComment struct {
	ID        int
	Body      string
	User      string
	CreatedAt time.Time
}

func convertPRComment(i sdk.PullRequestComments) issueComment {
	ct, _ := time.Parse(time.RFC3339, i.CreatedAt)

	return issueComment{
		ID:        int(i.Id),
		Body:      i.Body,
		User:      i.User.GetLogin(),
		CreatedAt: ct,
	}
}
