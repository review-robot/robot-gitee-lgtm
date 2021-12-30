package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type issueComment struct {
	ID        int32
	Body      string
	User      string
	CreatedAt time.Time
}

type ghClient struct {
	iClient
}

func (c *ghClient) listIssueComments(org, repo string, number int32) ([]issueComment, error) {
	var r []issueComment

	v, err := c.ListPRComments(org, repo, number)
	if err != nil {
		return r, err
	}

	for _, i := range v {
		ct, _ := time.Parse(time.RFC3339, i.CreatedAt)

		r = append(r, issueComment{
			ID:        i.Id,
			Body:      i.Body,
			User:      i.User.GetLogin(),
			CreatedAt: ct,
		})
	}

	sort.SliceStable(r, func(i, j int) bool {
		return r[i].CreatedAt.Before(r[j].CreatedAt)
	})

	return r, nil
}

func (c *ghClient) getCommitHashTree(org, repo, SHA string) (string, error) {
	v, err := c.GetPRCommit(org, repo, SHA)
	if err != nil {
		return "", err
	}

	if v.Commit == nil {
		return "", fmt.Errorf("single commit(%s/%s/%s) data is abnormal: %+v", org, repo, SHA, v)
	}

	return v.Commit.Tree.GetSha(), nil
}

func (gc *ghClient) getChangedFiles(org, repo string, number int32) ([]string, error) {
	changes, err := gc.GetPullRequestChanges(org, repo, number)
	if err != nil {
		return nil, fmt.Errorf("cannot get PR changes for %s/%s#%d", org, repo, number)
	}

	var v []string
	for _, change := range changes {
		v = append(v, change.Filename)
	}
	return v, nil
}

func normalizeLogin(s string) string {
	return strings.TrimPrefix(strings.ToLower(s), "@")
}
