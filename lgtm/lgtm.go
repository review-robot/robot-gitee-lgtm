/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package lgtm

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
)

var (
	addLGTMLabelNotification   = "LGTM label has been added.  <details>Git tree hash: %s</details>"
	addLGTMLabelNotificationRe = regexp.MustCompile(fmt.Sprintf(addLGTMLabelNotification, "(.*)"))
	configInfoReviewActsAsLgtm = `Reviews of "approve" or "request changes" act as adding or removing LGTM.`
	configInfoStoreTreeHash    = `Squashing commits does not remove LGTM.`
	// LGTMLabel is the name of the lgtm label applied by the lgtm plugin
	LGTMLabel = "lgtm"
	// LGTMRe is the regex that matches lgtm comments
	LGTMRe = regexp.MustCompile(`(?mi)^/lgtm(?: no-issue)?\s*$`)
	// LGTMCancelRe is the regex that matches lgtm cancel comments
	LGTMCancelRe        = regexp.MustCompile(`(?mi)^/lgtm cancel\s*$`)
	removeLGTMLabelNoti = "New changes are detected. LGTM label has been removed."
)

type githubClient interface {
	IsCollaborator(owner, repo, login string) (bool, error)
	AddLabel(owner, repo string, number int, label string) error
	AssignIssue(owner, repo string, number int, assignees []string) error
	CreateComment(owner, repo string, number int, comment string) error
	RemoveLabel(owner, repo string, number int, label string) error
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetPullRequestChanges(org, repo string, number int) ([]github.PullRequestChange, error)
	ListIssueComments(org, repo string, number int) ([]github.IssueComment, error)
	DeleteComment(org, repo string, ID int) error
	BotName() (string, error)
	GetSingleCommit(org, repo, SHA string) (github.SingleCommit, error)
	IsMember(org, user string) (bool, error)
	ListTeams(org string) ([]github.Team, error)
	ListTeamMembers(id int, role string) ([]github.TeamMember, error)
}

// reviewCtx contains information about each review event
type reviewCtx struct {
	author, issueAuthor, body, htmlURL string
	repo                               github.Repo
	assignees                          []github.User
	number                             int
}

type commentPruner interface {
	PruneComments(shouldPrune func(github.IssueComment) bool)
}

func handle(wantLGTM bool, config *plugins.Configuration, ownersClient RepoOwners, rc reviewCtx, gc githubClient, log *logrus.Entry, cp commentPruner) error {
	author := rc.author
	issueAuthor := rc.issueAuthor
	assignees := rc.assignees
	number := rc.number
	body := rc.body
	htmlURL := rc.htmlURL
	org := rc.repo.Owner.Login
	repoName := rc.repo.Name

	// Author cannot LGTM own PR, comment and abort
	isAuthor := author == issueAuthor
	if isAuthor && wantLGTM {
		resp := "you cannot LGTM your own PR."
		log.Infof("Commenting with \"%s\".", resp)
		return gc.CreateComment(rc.repo.Owner.Login, rc.repo.Name, rc.number, plugins.FormatResponseRaw(rc.body, rc.htmlURL, rc.author, resp))
	}

	// Determine if reviewer is already assigned
	isAssignee := false
	for _, assignee := range assignees {
		if assignee.Login == author {
			isAssignee = true
			break
		}
	}

	// check if skip collaborators is enabled for this org/repo
	skipCollaborators := skipCollaborators(config, org, repoName)

	// check if the commentor is a collaborator
	isCollaborator, err := gc.IsCollaborator(org, repoName, author)
	if err != nil {
		log.WithError(err).Error("Failed to check if author is a collaborator.")
		return err // abort if we can't determine if commentor is a collaborator
	}

	// if commentor isn't a collaborator, and we care about collaborators, abort
	if !isAuthor && !skipCollaborators && !isCollaborator {
		resp := "changing LGTM is restricted to collaborators"
		log.Infof("Reply to /lgtm request with comment: \"%s\"", resp)
		return gc.CreateComment(org, repoName, number, plugins.FormatResponseRaw(body, htmlURL, author, resp))
	}

	// either ensure that the commentor is a collaborator or an approver/reviwer
	if !isAuthor && !isAssignee && !skipCollaborators {
		// in this case we need to ensure the commentor is assignable to the PR
		// by assigning them
		log.Infof("Assigning %s/%s#%d to %s", org, repoName, number, author)
		if err := gc.AssignIssue(org, repoName, number, []string{author}); err != nil {
			log.WithError(err).Errorf("Failed to assign %s/%s#%d to %s", org, repoName, number, author)
		}
	} else if !isAuthor && skipCollaborators {
		// in this case we depend on OWNERS files instead to check if the author
		// is an approver or reviwer of the changed files
		log.Debugf("Skipping collaborator checks and loading OWNERS for %s/%s#%d", org, repoName, number)
		ro, err := loadRepoOwners(gc, ownersClient, org, repoName, number)
		if err != nil {
			return err
		}
		filenames, err := getChangedFiles(gc, org, repoName, number)
		if err != nil {
			return err
		}
		if !loadReviewers(ro, filenames).Has(github.NormLogin(author)) {
			resp := "adding LGTM is restricted to approvers and reviewers in OWNERS files."
			log.Infof("Reply to /lgtm request with comment: \"%s\"", resp)
			return gc.CreateComment(org, repoName, number, plugins.FormatResponseRaw(body, htmlURL, author, resp))
		}
	}

	// now we update the LGTM labels, having checked all cases where changing
	// LGTM was not allowed for the commentor

	// Only add the label if it doesn't have it, and vice versa.
	labels, err := gc.GetIssueLabels(org, repoName, number)
	if err != nil {
		log.WithError(err).Error("Failed to get issue labels.")
	}
	hasLGTM := github.HasLabel(LGTMLabel, labels)

	// remove the label if necessary, we're done after this
	opts := config.LgtmFor(rc.repo.Owner.Login, rc.repo.Name)
	if hasLGTM && !wantLGTM {
		log.Info("Removing LGTM label.")
		if err := gc.RemoveLabel(org, repoName, number, LGTMLabel); err != nil {
			return err
		}
		if opts.StoreTreeHash {
			cp.PruneComments(func(comment github.IssueComment) bool {
				return addLGTMLabelNotificationRe.MatchString(comment.Body)
			})
		}
	} else if !hasLGTM && wantLGTM {
		log.Info("Adding LGTM label.")
		if err := gc.AddLabel(org, repoName, number, LGTMLabel); err != nil {
			return err
		}
		if !stickyLgtm(log, gc, config, opts, issueAuthor, org, repoName) {
			if opts.StoreTreeHash {
				pr, err := gc.GetPullRequest(org, repoName, number)
				if err != nil {
					log.WithError(err).Error("Failed to get pull request.")
				}
				commit, err := gc.GetSingleCommit(org, repoName, pr.Head.SHA)
				if err != nil {
					log.WithField("sha", pr.Head.SHA).WithError(err).Error("Failed to get commit.")
				}
				treeHash := commit.Commit.Tree.SHA
				log.WithField("tree", treeHash).Info("Adding comment to store tree-hash.")
				if err := gc.CreateComment(org, repoName, number, fmt.Sprintf(addLGTMLabelNotification, treeHash)); err != nil {
					log.WithError(err).Error("Failed to add comment.")
				}
			}
			// Delete the LGTM removed noti after the LGTM label is added.
			cp.PruneComments(func(comment github.IssueComment) bool {
				return strings.Contains(comment.Body, removeLGTMLabelNoti)
			})
		}
	}

	return nil
}

func skipCollaborators(config *plugins.Configuration, org, repo string) bool {
	full := fmt.Sprintf("%s/%s", org, repo)
	for _, elem := range config.Owners.SkipCollaborators {
		if elem == org || elem == full {
			return true
		}
	}
	return false
}

func loadRepoOwners(gc githubClient, ownersClient RepoOwners, org, repo string, number int) (Repo, error) {
	pr, err := gc.GetPullRequest(org, repo, number)
	if err != nil {
		return nil, err
	}
	return ownersClient.LoadRepoOwners(org, repo, pr.Base.Ref)
}

// getChangedFiles returns all the changed files for the provided pull request.
func getChangedFiles(gc githubClient, org, repo string, number int) ([]string, error) {
	changes, err := gc.GetPullRequestChanges(org, repo, number)
	if err != nil {
		return nil, fmt.Errorf("cannot get PR changes for %s/%s#%d", org, repo, number)
	}
	var filenames []string
	for _, change := range changes {
		filenames = append(filenames, change.Filename)
	}
	return filenames, nil
}

// loadReviewers returns all reviewers and approvers from all OWNERS files that
// cover the provided filenames.
func loadReviewers(ro Repo, filenames []string) sets.String {
	reviewers := sets.String{}
	for _, filename := range filenames {
		reviewers = reviewers.Union(ro.Approvers(filename)).Union(ro.Reviewers(filename))
	}
	return reviewers
}

func stickyLgtm(log *logrus.Entry, gc githubClient, config *plugins.Configuration, lgtm *plugins.Lgtm, author, org, repo string) bool {
	if len(lgtm.StickyLgtmTeam) > 0 {
		if teams, err := gc.ListTeams(org); err == nil {
			for _, teamInOrg := range teams {
				// lgtm.TrustedAuthorTeams is supposed to be a very short list.
				if strings.Compare(teamInOrg.Name, lgtm.StickyLgtmTeam) == 0 {
					if members, err := gc.ListTeamMembers(teamInOrg.ID, github.RoleAll); err == nil {
						for _, member := range members {
							if strings.Compare(member.Login, author) == 0 {
								// The author is in a trusted team
								return true
							}
						}
					} else {
						log.WithError(err).Errorf("Failed to list members in %s:%s.", org, teamInOrg.Name)
					}
				}
			}
		} else {
			log.WithError(err).Errorf("Failed to list teams in org %s.", org)
		}
	}
	return false
}

func handlePullRequest(log *logrus.Entry, gc githubClient, config *plugins.Configuration, pe *github.PullRequestEvent) error {
	if pe.PullRequest.Merged {
		return nil
	}

	if pe.Action != github.PullRequestActionSynchronize {
		return nil
	}

	org := pe.PullRequest.Base.Repo.Owner.Login
	repo := pe.PullRequest.Base.Repo.Name
	number := pe.PullRequest.Number

	opts := config.LgtmFor(org, repo)
	if stickyLgtm(log, gc, config, opts, pe.PullRequest.User.Login, org, repo) {
		// If the author is trusted, skip tree hash verification and LGTM removal.
		return nil
	}

	// If we don't have the lgtm label, we don't need to check anything
	labels, err := gc.GetIssueLabels(org, repo, number)
	if err != nil {
		log.WithError(err).Error("Failed to get labels.")
	}
	if !github.HasLabel(LGTMLabel, labels) {
		return nil
	}

	if opts.StoreTreeHash {
		// Check if we have a tree-hash comment
		var lastLgtmTreeHash string
		botname, err := gc.BotName()
		if err != nil {
			return err
		}
		comments, err := gc.ListIssueComments(org, repo, number)
		if err != nil {
			log.WithError(err).Error("Failed to get issue comments.")
		}
		// older comments are still present
		// iterate backwards to find the last LGTM tree-hash
		for i := len(comments) - 1; i >= 0; i-- {
			comment := comments[i]
			m := addLGTMLabelNotificationRe.FindStringSubmatch(comment.Body)
			if comment.User.Login == botname && m != nil && comment.UpdatedAt.Equal(comment.CreatedAt) {
				lastLgtmTreeHash = m[1]
				break
			}
		}
		if lastLgtmTreeHash != "" {
			// Get the current tree-hash
			commit, err := gc.GetSingleCommit(org, repo, pe.PullRequest.Head.SHA)
			if err != nil {
				log.WithField("sha", pe.PullRequest.Head.SHA).WithError(err).Error("Failed to get commit.")
			}
			treeHash := commit.Commit.Tree.SHA
			if treeHash == lastLgtmTreeHash {
				// Don't remove the label, PR code hasn't changed
				log.Infof("Keeping LGTM label as the tree-hash remained the same: %s", treeHash)
				return nil
			}
		}
	}

	if err := gc.RemoveLabel(org, repo, number, LGTMLabel); err != nil {
		return fmt.Errorf("failed removing lgtm label: %v", err)
	}

	// Create a comment to inform participants that LGTM label is removed due to new
	// pull request changes.
	log.Infof("Commenting with an LGTM removed notification to %s/%s#%d with a message: %s", org, repo, number, removeLGTMLabelNoti)
	return gc.CreateComment(org, repo, number, removeLGTMLabelNoti)
}
