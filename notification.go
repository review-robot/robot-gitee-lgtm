package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/opensourceways/robot-gitee-lgtm/lgtm"
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
	commentID  int
}

func (notify *notification) GetConsentors() map[string]bool {
	return notify.consentors
}

func (notify *notification) GetOpponents() map[string]bool {
	return notify.opponents
}

func (notify *notification) ResetConsentor() {
	notify.consentors = map[string]bool{}
}

func (notify *notification) ResetOpponents() {
	notify.opponents = map[string]bool{}
}

func (notify *notification) AddConsentor(consentor string, isReviewer bool) {
	notify.consentors[consentor] = isReviewer
	if _, ok := notify.opponents[consentor]; ok {
		delete(notify.opponents, consentor)
	}
}

func (notify *notification) AddOpponent(opponent string, isReviewer bool) {
	notify.opponents[opponent] = isReviewer
	if _, ok := notify.consentors[opponent]; ok {
		delete(notify.consentors, opponent)
	}
}

func (notify *notification) ResetDirs(s []string) {
	notify.dirs = s
}

func (notify *notification) GetDirs() []string {
	return notify.dirs
}

func (notify *notification) WriteComment(gc *ghClient, org, repo string, prNumber int, ok bool) error {
	r := consentientDesc
	if !ok {
		r = opposedDesc
	}

	s := ""
	if notify.dirs != nil && len(notify.dirs) > 0 {
		s = fmt.Sprintf("%s%s", dirSepa, strings.Join(notify.dirs, dirSepa))
	}

	comment := fmt.Sprintf(
		notificationStr, r,
		reviewerToComment(notify.consentors, separator),
		reviewerToComment(notify.opponents, separator),
		s,
		notify.treeHash,
	)

	if notify.commentID == 0 {
		return gc.CreateComment(org, repo, prNumber, comment)
	}

	return gc.UpdatePRComment(org, repo, notify.commentID, comment)
}

func LoadLGTMnotification(gc *ghClient, org, repo string, prNumber int, sha string) (*notification, bool, error) {
	botname, err := gc.BotName()
	if err != nil {
		return nil, false, err
	}

	comments, err := gc.ListIssueComments(org, repo, prNumber)
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

	for _, comment := range comments {
		if comment.User.Login != botname {
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

	filenames, err := lgtm.GetChangedFiles(gc, org, repo, prNumber)
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
