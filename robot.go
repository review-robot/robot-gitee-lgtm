package main

import (
	"fmt"
	"time"

	"github.com/opensourceways/community-robot-lib/config"
	"github.com/opensourceways/community-robot-lib/robot-gitee-framework"
	sdk "github.com/opensourceways/go-gitee/gitee"
	"github.com/opensourceways/repo-owners-cache/grpc/client"
	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/commentpruner"
	"k8s.io/test-infra/prow/github"

	"github.com/opensourceways/robot-gitee-lgtm/lgtm"
)

const botName = "lgtm"

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

func newRobot(cli iClient, cacheCli *client.Client) *robot {
	return &robot{cli: cli, cacheCli: cacheCli}
}

type robot struct {
	cli      iClient
	cacheCli *client.Client
}

func (bot *robot) NewConfig() config.Config {
	return &configuration{}
}

func (bot *robot) getConfig(cfg config.Config, org, repo string) (*botConfig, error) {
	c, ok := cfg.(*configuration)
	if !ok {
		return nil, fmt.Errorf("can't convert to configuration")
	}

	if bc := c.configFor(org, repo); bc != nil {
		return bc, nil
	}

	return nil, fmt.Errorf("no config for this repo:%s/%s", org, repo)
}

func (bot *robot) RegisterEventHandler(f framework.HandlerRegitster) {
	f.RegisterPullRequestHandler(bot.handlePREvent)
	f.RegisterNoteEventHandler(bot.handleNoteEvent)
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
	bcfg, err := bot.getConfig(c, org, repo)
	if err != nil {
		return err
	}

	cfg := convertLgtmConfig(bcfg)
	pe := convertPullRequestEvent(e)
	prBase := e.GetPullRequest().GetBase().GetRepo()
	skipCollaborators := lgtm.SkipCollaborators(cfg, prBase.GetNameSpace(), prBase.GetPath())
	ghc := newGHClient(bot.cli)

	if !skipCollaborators || !bcfg.StrictReview {
		return lgtm.HandlePullRequest(log, ghc, cfg, &pe)
	}

	return HandleStrictLGTMPREvent(ghc, &pe)
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

	toAdd, toRemove := doWhat(e.Comment.Body)
	if !(toAdd || toRemove) {
		return nil
	}

	org, repo := e.GetOrgRepo()
	bcfg, err := bot.getConfig(c, org, repo)
	if err != nil {
		return err
	}

	cfg := convertLgtmConfig(bcfg)
	pr := e.GetPullRequest()
	assignees := make([]github.User, len(pr.GetAssignees()))
	for i, v := range pr.GetAssignees() {
		assignees[i] = github.User{Login: v.Login}
	}

	repos := github.Repo{}
	repos.Owner.Login = org
	repos.Name = repo

	comment := e.GetComment()
	rc := lgtm.NewReviewCtx(
		comment.GetUser().GetLogin(), pr.GetUser().GetLogin(),
		comment.GetBody(), comment.GetHtmlUrl(),
		repos, assignees, int(pr.GetNumber()),
	)
	ghc := newGHClient(bot.cli)
	cp := commentpruner.NewEventClient(ghc, log, org, repo, int(pr.GetNumber()))
	oc := newRepoOwnersClient(bot.cacheCli)
	skipCollaborators := lgtm.SkipCollaborators(cfg, org, repo)

	if !skipCollaborators || !bcfg.StrictReview {
		return lgtm.Handle(toAdd, cfg, oc, rc, ghc, log, cp)
	}

	return HandleStrictLGTMComment(ghc, oc, log, toAdd, e)
}

func doWhat(comment string) (bool, bool) {
	if lgtm.LGTMRe.MatchString(comment) {
		return true, false
	}

	if lgtm.LGTMCancelRe.MatchString(comment) {
		return false, true
	}

	return false, false
}
