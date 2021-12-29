package main

import (
	"github.com/opensourceways/community-robot-lib/giteeclient"
	sdk "github.com/opensourceways/go-gitee/gitee"
	"github.com/opensourceways/repo-owners-cache/repoowners"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

const LGTMLabel = "lgtm"

func HandleStrictLGTMPREvent(gc *ghClient, e *sdk.PullRequestEvent) error {
	org, repo := e.GetOrgRepo()
	prNumber := int(e.GetPRNumber())

	sha, err := getHashTree(gc, org, repo, e.GetPRHeadSha())
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

		filenames, err := getChangedFiles(gc, org, repo, prNumber)
		if err != nil {
			return err
		}

		n.ResetDirs(genDirs(filenames))

	case sdk.PRActionChangedSourceBranch:
		v, prChanged, err := LoadLGTMnotification(gc, org, repo, prNumber, sha)
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
		return gc.RemoveLabel(org, repo, prNumber, LGTMLabel)
	}
	return nil
}

// skipCollaborators && strictReviewer
func HandleStrictLGTMComment(gc *ghClient, oc repoowners.RepoOwner, log *logrus.Entry, wantLGTM bool, e *sdk.NoteEvent) error {
	org, repo := e.GetOrgRepo()

	s := &strictReview{
		gc:  gc,
		oc:  oc,
		log: log,

		org:      org,
		repo:     repo,
		prAuthor: e.GetPRAuthor(),
		prNumber: int(e.GetPRNumber()),
	}

	sha, err := getHashTree(gc, org, repo, e.GetPRHeadSha())
	if err != nil {
		return err
	}
	s.treeHash = sha

	noti, _, err := LoadLGTMnotification(gc, org, repo, s.prNumber, s.treeHash)
	if err != nil {
		return err
	}

	validReviewers, err := s.fileReviewers()
	if err != nil {
		return err
	}

	hasLGTM, err := s.hasLGTMLabel()
	if err != nil {
		return err
	}

	if !wantLGTM {
		return s.handleLGTMCancel(noti, validReviewers, e, hasLGTM)
	}

	return s.handleLGTM(noti, validReviewers, e, hasLGTM)
}

type iPRInfo interface {
	hasLabel(string) bool
}

type strictReview struct {
	log *logrus.Entry
	gc  *ghClient
	oc  repoowners.RepoOwner

	pr iPRInfo

	org      string
	repo     string
	treeHash string
	prAuthor string
	prNumber int
}

func (sr *strictReview) handleLGTMCancel(noti *notification, validReviewers map[string]sets.String, e *sdk.NoteEvent, hasLabel bool) error {
	commenter := e.Comment.User.Login

	if commenter != sr.prAuthor && !isReviewer(validReviewers, commenter) {
		noti.AddOpponent(commenter, false)

		return sr.writeComment(noti, hasLabel)
	}

	if commenter == sr.prAuthor {
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

	if hasLabel {
		return sr.removeLabel()
	}
	return nil
}

func (sr *strictReview) handleLGTM(noti *notification, validReviewers map[string]sets.String, e *sdk.NoteEvent, hasLabel bool) error {
	comment := e.Comment
	commenter := comment.User.Login

	if commenter == sr.prAuthor {
		resp := "you cannot LGTM your own PR."

		return sr.gc.CreateComment(
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
		return sr.writeComment(noti, hasLabel)
	}

	resetReviewDir(validReviewers, noti)

	ok = canAddLgtmLabel(noti)
	if err := sr.writeComment(noti, ok); err != nil {
		return err
	}

	if ok && !hasLabel {
		return sr.addLabel()
	}

	if !ok && hasLabel {
		return sr.removeLabel()
	}

	return nil
}

func (sr *strictReview) fileReviewers() (map[string]sets.String, error) {
	filenames, err := getChangedFiles(sr.gc, sr.org, sr.repo, sr.prNumber)
	if err != nil {
		return nil, err
	}

	ro := sr.oc
	m := map[string]sets.String{}

	for _, filename := range filenames {
		m[filename] = ro.Approvers(filename).Union(ro.Reviewers(filename))
	}

	return m, nil
}

func (sr *strictReview) writeComment(noti *notification, ok bool) error {
	return noti.WriteComment(sr.gc, sr.org, sr.repo, sr.prNumber, ok)
}

func (sr *strictReview) hasLGTMLabel() (bool, error) {
	return sr.pr.hasLabel(LGTMLabel), nil
}

func (sr *strictReview) removeLabel() error {
	return sr.gc.RemoveLabel(sr.org, sr.repo, sr.prNumber, LGTMLabel)
}

func (sr *strictReview) addLabel() error {
	return sr.gc.AddLabel(sr.org, sr.repo, sr.prNumber, LGTMLabel)
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

func getHashTree(gc *ghClient, org, repo, headSHA string) (string, error) {
	return gc.GetSingleCommit(org, repo, headSHA)
}
