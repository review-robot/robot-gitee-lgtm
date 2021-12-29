package main

import (
	"github.com/opensourceways/community-robot-lib/giteeclient"
	sdk "github.com/opensourceways/go-gitee/gitee"
	"github.com/opensourceways/repo-owners-cache/repoowners"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

const LGTMLabel = "lgtm"

func (bot *robot) handleStrictLGTMPREvent(e *sdk.PullRequestEvent) error {
	org, repo := e.GetOrgRepo()
	prNumber := e.GetPRNumber()
	gc := &bot.cli

	sha, err := gc.getCommitHashTree(org, repo, e.GetPRHeadSha())
	if err != nil {
		return err
	}

	var n *notification
	needRemoveLabel := false

	switch sdk.GetPullRequestAction(e) {
	case sdk.ActionOpen:
		n = &notification{
			treeHash: sha,
		}

		filenames, err := gc.getChangedFiles(org, repo, prNumber)
		if err != nil {
			return err
		}

		n.ResetDirs(genDirs(filenames))

	case sdk.PRActionChangedSourceBranch:
		v, prChanged, err := bot.loadLGTMnotification(org, repo, prNumber, sha)
		if err != nil {
			return err
		}

		if !prChanged {
			return nil
		}

		n = v
		needRemoveLabel = true

	default:
		return nil
	}

	if err := n.WriteComment(gc, org, repo, prNumber, false); err != nil {
		return err
	}

	if needRemoveLabel {
		return gc.RemovePRLabel(org, repo, prNumber, LGTMLabel)
	}
	return nil
}

func (bot *robot) handleStrictLGTMComment(oc repoowners.RepoOwner, log *logrus.Entry, wantLGTM bool, e *sdk.NoteEvent) error {
	org, repo := e.GetOrgRepo()
	gc := &bot.cli

	s := &strictReview{
		gc:  gc,
		oc:  oc,
		log: log,
		pr:  prInfoOnNoteEvent{e},

		org:      org,
		repo:     repo,
		prNumber: e.GetPRNumber(),
	}

	sha, err := gc.getCommitHashTree(org, repo, e.GetPRHeadSha())
	if err != nil {
		return err
	}
	s.treeHash = sha

	noti, _, err := bot.loadLGTMnotification(org, repo, s.prNumber, s.treeHash)
	if err != nil {
		return err
	}

	validReviewers, err := s.fileReviewers()
	if err != nil {
		return err
	}

	if !wantLGTM {
		return s.handleLGTMCancel(noti, validReviewers, e)
	}

	return s.handleLGTM(noti, validReviewers, e)
}

type iPRInfo interface {
	hasLabel(string) bool
	getPRAuthor() string
}

type prInfoOnNoteEvent struct {
	e *sdk.NoteEvent
}

func (p prInfoOnNoteEvent) hasLabel(l string) bool {
	return p.e.GetIssueLabelSet().Has(l)
}

func (p prInfoOnNoteEvent) getPRAuthor() string {
	return p.e.GetPRAuthor()
}

type strictReview struct {
	log *logrus.Entry
	gc  *ghClient
	oc  repoowners.RepoOwner
	pr  iPRInfo

	org      string
	repo     string
	treeHash string
	prNumber int32
}

func (sr *strictReview) handleLGTMCancel(noti *notification, validReviewers map[string]sets.String, e *sdk.NoteEvent) error {
	commenter := e.GetCommenter()
	prAuthor := sr.pr.getPRAuthor()

	if commenter != prAuthor && !isReviewer(validReviewers, commenter) {
		noti.AddOpponent(commenter, false)

		return sr.writeComment(noti, sr.hasLGTMLabel())
	}

	if commenter == prAuthor {
		noti.ResetConsentor()
		noti.ResetOpponents()
	} else {
		// commenter is not pr author, but is reviewr
		// I don't know which part of code commenter thought it is not good
		// Maybe it is directory of which he is reviewer, maybe other parts.
		// So, it simply sets all the codes need review again. Because the
		// lgtm label needs no reviewer say `/lgtm cancel`
		noti.AddOpponent(commenter, true)
	}

	filenames := make([]string, 0, len(validReviewers))
	for k := range validReviewers {
		filenames = append(filenames, k)
	}
	noti.ResetDirs(genDirs(filenames))

	err := sr.writeComment(noti, false)
	if err != nil {
		return err
	}

	if sr.hasLGTMLabel() {
		return sr.removeLabel()
	}
	return nil
}

func (sr *strictReview) handleLGTM(noti *notification, validReviewers map[string]sets.String, e *sdk.NoteEvent) error {
	commenter := e.GetCommenter()

	if commenter == sr.pr.getPRAuthor() {
		resp := "you cannot LGTM your own PR."

		return sr.gc.CreatePRComment(
			sr.org, sr.repo, sr.prNumber,
			giteeclient.GenResponseWithReference(e, resp))
	}

	consentors := noti.GetConsentors()
	if _, ok := consentors[commenter]; ok {
		// add /lgtm repeatedly
		return nil
	}

	ok := isReviewer(validReviewers, commenter)
	noti.AddConsentor(commenter, ok)

	if !ok {
		return sr.writeComment(noti, sr.hasLGTMLabel())
	}

	resetReviewDir(validReviewers, noti)

	ok = canAddLgtmLabel(noti)
	if err := sr.writeComment(noti, ok); err != nil {
		return err
	}

	hasLabel := sr.hasLGTMLabel()

	if ok && !hasLabel {
		return sr.addLabel()
	}

	if !ok && hasLabel {
		return sr.removeLabel()
	}

	return nil
}

func (sr *strictReview) fileReviewers() (map[string]sets.String, error) {
	filenames, err := sr.gc.getChangedFiles(sr.org, sr.repo, sr.prNumber)
	if err != nil {
		return nil, err
	}

	ro := sr.oc
	m := map[string]sets.String{}

	for _, filename := range filenames {
		s := sets.NewString()
		v := ro.Approvers(filename).Union(ro.Reviewers(filename))
		for k := range v {
			s.Insert(normalizeLogin(k))
		}

		m[filename] = s
	}

	return m, nil
}

func (sr *strictReview) writeComment(noti *notification, ok bool) error {
	return noti.WriteComment(sr.gc, sr.org, sr.repo, sr.prNumber, ok)
}

func (sr *strictReview) hasLGTMLabel() bool {
	return sr.pr.hasLabel(LGTMLabel)
}

func (sr *strictReview) removeLabel() error {
	return sr.gc.RemovePRLabel(sr.org, sr.repo, sr.prNumber, LGTMLabel)
}

func (sr *strictReview) addLabel() error {
	return sr.gc.AddPRLabel(sr.org, sr.repo, sr.prNumber, LGTMLabel)
}

func canAddLgtmLabel(noti *notification) bool {
	for _, v := range noti.GetOpponents() {
		if v {
			// there are reviewers said `/lgtm cancel`
			return false
		}
	}

	d := noti.GetDirs()
	return d == nil || len(d) == 0
}

func isReviewer(validReviewers map[string]sets.String, commenter string) bool {
	commenter = normalizeLogin(commenter)

	for _, rs := range validReviewers {
		if rs.Has(commenter) {
			return true
		}
	}

	return false
}

func resetReviewDir(validReviewers map[string]sets.String, noti *notification) {
	consentors := noti.GetConsentors()
	reviewers := make([]string, 0, len(consentors))
	for k, v := range consentors {
		if v {
			reviewers = append(reviewers, normalizeLogin(k))
		}
	}

	needReview := map[string]bool{}
	for filename, rs := range validReviewers {
		if !rs.HasAny(reviewers...) {
			needReview[filename] = true
		}
	}

	if len(needReview) != 0 {
		noti.ResetDirs(genDirs(mapKeys(needReview)))
	} else {
		noti.ResetDirs(nil)
	}
}
