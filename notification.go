package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	consentientDesc = "**LGTM**"
	opposedDesc     = "**NOT LGTM**"
	separator       = ", "
	dirSepa         = "\n- "
)

var (
	notificationStr   = "LGTM NOTIFIER: This PR is %s.\n\nReviewers added `/lgtm` are: %s.\n\nReviewers added `/lgtm cancel` are: %s.\n\nIt still needs review for the codes in each of these directoris:%s\n<details>Git tree hash: %s</details>"
	notificationStrRe = regexp.MustCompile(fmt.Sprintf(notificationStr, "(.*)", "(.*)", "(.*)", "([\\s\\S]*)", "(.*)"))
)

type notification struct {
	consentors map[string]bool
	opponents  map[string]bool
	dirs       []string
	treeHash   string
	commentID  int32
}

func (n *notification) GetConsentors() map[string]bool {
	return n.consentors
}

func (n *notification) GetOpponents() map[string]bool {
	return n.opponents
}

func (n *notification) ResetConsentor() {
	n.consentors = map[string]bool{}
}

func (n *notification) ResetOpponents() {
	n.opponents = map[string]bool{}
}

func (n *notification) AddConsentor(consentor string, isReviewer bool) {
	n.consentors[consentor] = isReviewer
	if _, ok := n.opponents[consentor]; ok {
		delete(n.opponents, consentor)
	}
}

func (n *notification) AddOpponent(opponent string, isReviewer bool) {
	n.opponents[opponent] = isReviewer
	if _, ok := n.consentors[opponent]; ok {
		delete(n.consentors, opponent)
	}
}

func (n *notification) ResetDirs(s []string) {
	n.dirs = s
}

func (n *notification) GetDirs() []string {
	return n.dirs
}

func (n *notification) WriteComment(gc *ghClient, org, repo string, prNumber int32, ok bool) error {
	r := consentientDesc
	if !ok {
		r = opposedDesc
	}

	s := ""
	if n.dirs != nil && len(n.dirs) > 0 {
		s = fmt.Sprintf("%s%s", dirSepa, strings.Join(n.dirs, dirSepa))
	}

	comment := fmt.Sprintf(
		notificationStr, r,
		reviewerToComment(n.consentors, separator),
		reviewerToComment(n.opponents, separator),
		s,
		n.treeHash,
	)

	if n.commentID == 0 {
		return gc.CreatePRComment(org, repo, prNumber, comment)
	}

	return gc.UpdatePRComment(org, repo, n.commentID, comment)
}

func (bot *robot) loadLGTMnotification(org, repo string, prNumber int32, sha string) (*notification, bool, error) {
	comments, err := bot.cli.listIssueComments(org, repo, prNumber)
	if err != nil {
		return nil, false, err
	}

	split := func(s, sep string) []string {
		if s != "" {
			return strings.Split(s, sep)
		}
		return nil
	}

	n := &notification{treeHash: sha}
	botname := bot.botName

	for _, comment := range comments {
		if comment.User != botname {
			continue
		}

		m := notificationStrRe.FindStringSubmatch(comment.Body)
		if m != nil {
			n.commentID = comment.ID

			if m[5] == sha {
				n.consentors = commentToReviewer(m[2], separator)
				n.opponents = commentToReviewer(m[3], separator)
				n.dirs = split(m[4], dirSepa)

				return n, false, nil
			}
			break
		}
	}

	filenames, err := bot.cli.getChangedFiles(org, repo, prNumber)
	if err != nil {
		return nil, false, err
	}

	n.ResetDirs(genDirs(filenames))
	n.ResetConsentor()
	n.ResetOpponents()
	return n, true, nil
}

func reviewerToComment(r map[string]bool, sep string) string {
	if r == nil || len(r) == 0 {
		return ""
	}

	s := make([]string, 0, len(r))
	for k, v := range r {
		if v {
			s = append(s, fmt.Sprintf("**%s**", k))
		} else {
			s = append(s, k)
		}
	}
	return strings.Join(s, sep)
}

func commentToReviewer(s, sep string) map[string]bool {
	if s != "" {
		a := strings.Split(s, sep)
		m := make(map[string]bool, len(a))

		for _, item := range a {
			r := strings.Trim(item, "**")
			m[r] = (item != r)
		}
		return m
	}

	return map[string]bool{}
}

func genDirs(filenames []string) []string {
	m := map[string]bool{}
	for _, n := range filenames {
		m[filepath.Dir(n)] = true
	}

	if m["."] {
		m["root directory"] = true
		delete(m, ".")
	}

	return mapKeys(m)
}

func mapKeys(m map[string]bool) []string {
	s := make([]string, 0, len(m))
	for k := range m {
		s = append(s, k)
	}
	return s
}
