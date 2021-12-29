package main

import (
	"fmt"
	"regexp"
	"time"

	"github.com/opensourceways/community-robot-lib/config"
	"github.com/opensourceways/community-robot-lib/robot-gitee-framework"
	sdk "github.com/opensourceways/go-gitee/gitee"
	"github.com/opensourceways/repo-owners-cache/grpc/client"
	"github.com/opensourceways/repo-owners-cache/repoowners"
	"github.com/sirupsen/logrus"
)

const botName = "lgtm"

var (
	// LGTMRe is the regex that matches lgtm comments
	LGTMRe = regexp.MustCompile(`(?mi)^/lgtm(?: no-issue)?\s*$`)

	// LGTMCancelRe is the regex that matches lgtm cancel comments
	LGTMCancelRe = regexp.MustCompile(`(?mi)^/lgtm cancel\s*$`)
)

type iClient interface {
	ListCollaborators(org, repo string) ([]sdk.ProjectMember, error)
	AssignPR(owner, repo string, number int32, logins []string) error
	IsCollaborator(owner, repo, login string) (bool, error)
	AddPRLabel(org, repo string, number int32, label string) error
	CreatePRComment(org, repo string, number int32, comment string) error
	UpdatePRComment(org, repo string, commentID int32, comment string) error
	RemovePRLabel(org, repo string, number int32, label string) error
	GetPRLabels(org, repo string, number int32) ([]sdk.Label, error)
	GetGiteePullRequest(org, repo string, number int32) (sdk.PullRequest, error)
	GetPullRequestChanges(org, repo string, number int32) ([]sdk.PullRequestFiles, error)
	ListPRComments(org, repo string, number int32) ([]sdk.PullRequestComments, error)
	DeletePRComment(org, repo string, ID int32) error
	GetBot() (sdk.User, error)
	GetPRCommit(org, repo, SHA string) (sdk.RepoCommit, error)
}

func newRobot(cli iClient, cacheCli *client.Client, botName string) *robot {
	return &robot{cli: ghClient{cli}, cacheCli: cacheCli, botName: botName}
}

type robot struct {
	cacheCli *client.Client
	cli      ghClient
	botName  string
}

func (bot *robot) NewConfig() config.Config {
	return &configuration{}
}

func (bot *robot) canApply(cfg config.Config, org, repo string) error {
	c, ok := cfg.(*configuration)
	if !ok {
		return fmt.Errorf("can't convert to configuration")
	}

	if bc := c.configFor(org, repo); bc != nil {
		return nil
	}

	return fmt.Errorf("no config for this repo:%s/%s", org, repo)
}

func (bot *robot) RegisterEventHandler(f framework.HandlerRegitster) {
	f.RegisterPullRequestHandler(bot.handlePREvent)
	f.RegisterNoteEventHandler(bot.handleNoteEvent)
}

func (bot *robot) handleNoteEvent(e *sdk.NoteEvent, c config.Config, log *logrus.Entry) error {
	funcStart := time.Now()
	defer func() {
		log.WithField("duration", time.Since(funcStart).String()).Debug("Completed handleNoteEvent")
	}()

	if !e.IsPullRequest() {
		log.Debug("Event is not a creation of a comment on a PR, skipping.")
		return nil
	}

	if !e.IsCreatingCommentEvent() {
		log.Debug("Event is not a creation of a comment on an open PR, skipping.")
		return nil
	}

	org, repo := e.GetOrgRepo()

	if err := bot.canApply(c, org, repo); err != nil {
		return err
	}

	toAdd, toRemove := doWhat(e.Comment.Body)
	if !(toAdd || toRemove) {
		return nil
	}

	owner, err := bot.loadRepoOwners(org, repo, e.GetPRBaseRef())
	if err != nil {
		return err
	}

	return bot.handleStrictLGTMComment(owner, log, toAdd, e)
}

func (bot *robot) handlePREvent(e *sdk.PullRequestEvent, c config.Config, log *logrus.Entry) error {
	funcStart := time.Now()
	defer func() {
		log.WithField("duration", time.Since(funcStart).String()).Debug("Completed handlePullRequest")
	}()

	if e.GetState() != sdk.StatusOpen {
		log.Debug("Pull request state is not open, skipping...")
		return nil
	}

	org, repo := e.GetOrgRepo()

	if err := bot.canApply(c, org, repo); err != nil {
		return err
	}

	return bot.handleStrictLGTMPREvent(e)
}

func (bot *robot) loadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error) {
	return repoowners.NewRepoOwners(
		repoowners.RepoBranch{
			Platform: "gitee",
			Org:      org,
			Repo:     repo,
			Branch:   base,
		},
		bot.cacheCli,
	)
}

func doWhat(comment string) (bool, bool) {
	if LGTMRe.MatchString(comment) {
		return true, false
	}

	if LGTMCancelRe.MatchString(comment) {
		return false, true
	}

	return false, false
}
