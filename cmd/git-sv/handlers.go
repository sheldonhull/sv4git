package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bvieira/sv4git/sv"

	"github.com/Masterminds/semver/v3"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

func configDefaultHandler() func(c *cli.Context) error {
	cfg := defaultConfig()
	return func(c *cli.Context) error {
		content, err := yaml.Marshal(&cfg)
		if err != nil {
			return err
		}
		fmt.Println(string(content))
		return nil
	}
}

func configShowHandler(cfg Config) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		content, err := yaml.Marshal(&cfg)
		if err != nil {
			return err
		}
		fmt.Println(string(content))
		return nil
	}
}

func currentVersionHandler(git sv.Git) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		lastTag := git.LastTag()

		currentVer, err := sv.ToVersion(lastTag)
		if err != nil {
			return fmt.Errorf("error parsing version: %s from git tag, message: %v", lastTag, err)
		}
		fmt.Printf("%d.%d.%d\n", currentVer.Major(), currentVer.Minor(), currentVer.Patch())
		return nil
	}
}

func nextVersionHandler(git sv.Git, semverProcessor sv.SemVerCommitsProcessor) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		lastTag := git.LastTag()

		currentVer, err := sv.ToVersion(lastTag)
		if err != nil {
			return fmt.Errorf("error parsing version: %s from git tag, message: %v", lastTag, err)
		}

		commits, err := git.Log(sv.NewLogRange(sv.TagRange, lastTag, ""))
		if err != nil {
			return fmt.Errorf("error getting git log, message: %v", err)
		}

		nextVer, _ := semverProcessor.NextVersion(currentVer, commits)
		fmt.Printf("%d.%d.%d\n", nextVer.Major(), nextVer.Minor(), nextVer.Patch())
		return nil
	}
}

func commitLogHandler(git sv.Git, semverProcessor sv.SemVerCommitsProcessor) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		var commits []sv.GitCommitLog
		var err error
		tagFlag := c.String("t")
		rangeFlag := c.String("r")
		startFlag := c.String("s")
		endFlag := c.String("e")
		if tagFlag != "" && (rangeFlag != string(sv.TagRange) || startFlag != "" || endFlag != "") {
			return fmt.Errorf("cannot define tag flag with range, start or end flags")
		}

		if tagFlag != "" {
			commits, err = getTagCommits(git, tagFlag)
		} else {
			r, rerr := logRange(git, rangeFlag, startFlag, endFlag)
			if rerr != nil {
				return rerr
			}
			commits, err = git.Log(r)
		}
		if err != nil {
			return fmt.Errorf("error getting git log, message: %v", err)
		}

		for _, commit := range commits {
			content, err := json.Marshal(commit)
			if err != nil {
				return err
			}
			fmt.Println(string(content))
		}
		return nil
	}
}

func getTagCommits(git sv.Git, tag string) ([]sv.GitCommitLog, error) {
	prev, _, err := getTags(git, tag)
	if err != nil {
		return nil, err
	}
	return git.Log(sv.NewLogRange(sv.TagRange, prev, tag))
}

func logRange(git sv.Git, rangeFlag, startFlag, endFlag string) (sv.LogRange, error) {
	switch rangeFlag {
	case string(sv.TagRange):
		return sv.NewLogRange(sv.TagRange, str(startFlag, git.LastTag()), endFlag), nil
	case string(sv.DateRange):
		return sv.NewLogRange(sv.DateRange, startFlag, endFlag), nil
	case string(sv.HashRange):
		return sv.NewLogRange(sv.HashRange, startFlag, endFlag), nil
	default:
		return sv.LogRange{}, fmt.Errorf("invalid range: %s, expected: %s, %s or %s", rangeFlag, sv.TagRange, sv.DateRange, sv.HashRange)
	}
}

func commitNotesHandler(git sv.Git, rnProcessor sv.ReleaseNoteProcessor, outputFormatter sv.OutputFormatter) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		var date time.Time

		rangeFlag := c.String("r")
		lr, err := logRange(git, rangeFlag, c.String("s"), c.String("e"))
		if err != nil {
			return err
		}

		commits, err := git.Log(lr)
		if err != nil {
			return fmt.Errorf("error getting git log from range: %s, message: %v", rangeFlag, err)
		}

		if len(commits) > 0 {
			date, _ = time.Parse("2006-01-02", commits[0].Date)
		}

		releasenote := rnProcessor.Create(nil, date, commits)
		fmt.Println(outputFormatter.FormatReleaseNote(releasenote))
		return nil
	}
}

func releaseNotesHandler(git sv.Git, semverProcessor sv.SemVerCommitsProcessor, rnProcessor sv.ReleaseNoteProcessor, outputFormatter sv.OutputFormatter) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		var commits []sv.GitCommitLog
		var rnVersion semver.Version
		var date time.Time
		var err error

		if tag := c.String("t"); tag != "" {
			rnVersion, date, commits, err = getTagVersionInfo(git, semverProcessor, tag)
		} else {
			// TODO: should generate release notes if version was not updated?
			rnVersion, _, date, commits, err = getNextVersionInfo(git, semverProcessor)
		}

		if err != nil {
			return err
		}

		releasenote := rnProcessor.Create(&rnVersion, date, commits)
		fmt.Println(outputFormatter.FormatReleaseNote(releasenote))
		return nil
	}
}

func getTagVersionInfo(git sv.Git, semverProcessor sv.SemVerCommitsProcessor, tag string) (semver.Version, time.Time, []sv.GitCommitLog, error) {
	tagVersion, err := sv.ToVersion(tag)
	if err != nil {
		return semver.Version{}, time.Time{}, nil, fmt.Errorf("error parsing version: %s from tag, message: %v", tag, err)
	}

	previousTag, currentTag, err := getTags(git, tag)
	if err != nil {
		return semver.Version{}, time.Time{}, nil, fmt.Errorf("error listing tags, message: %v", err)
	}

	commits, err := git.Log(sv.NewLogRange(sv.TagRange, previousTag, tag))
	if err != nil {
		return semver.Version{}, time.Time{}, nil, fmt.Errorf("error getting git log from tag: %s, message: %v", tag, err)
	}

	return tagVersion, currentTag.Date, commits, nil
}

func getTags(git sv.Git, tag string) (string, sv.GitTag, error) {
	tags, err := git.Tags()
	if err != nil {
		return "", sv.GitTag{}, err
	}

	index := find(tag, tags)
	if index < 0 {
		return "", sv.GitTag{}, fmt.Errorf("tag: %s not found", tag)
	}

	previousTag := ""
	if index > 0 {
		previousTag = tags[index-1].Name
	}
	return previousTag, tags[index], nil
}

func find(tag string, tags []sv.GitTag) int {
	for i := 0; i < len(tags); i++ {
		if tag == tags[i].Name {
			return i
		}
	}
	return -1
}

func getNextVersionInfo(git sv.Git, semverProcessor sv.SemVerCommitsProcessor) (semver.Version, bool, time.Time, []sv.GitCommitLog, error) {
	lastTag := git.LastTag()

	currentVer, err := sv.ToVersion(lastTag)
	if err != nil {
		return semver.Version{}, false, time.Time{}, nil, fmt.Errorf("error parsing version: %s from git tag, message: %v", lastTag, err)
	}

	commits, err := git.Log(sv.NewLogRange(sv.TagRange, lastTag, ""))
	if err != nil {
		return semver.Version{}, false, time.Time{}, nil, fmt.Errorf("error getting git log, message: %v", err)
	}

	version, updated := semverProcessor.NextVersion(currentVer, commits)
	return version, updated, time.Now(), commits, nil
}

func tagHandler(git sv.Git, semverProcessor sv.SemVerCommitsProcessor) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		lastTag := git.LastTag()

		currentVer, err := sv.ToVersion(lastTag)
		if err != nil {
			return fmt.Errorf("error parsing version: %s from git tag, message: %v", lastTag, err)
		}

		commits, err := git.Log(sv.NewLogRange(sv.TagRange, lastTag, ""))
		if err != nil {
			return fmt.Errorf("error getting git log, message: %v", err)
		}

		nextVer, _ := semverProcessor.NextVersion(currentVer, commits)
		fmt.Printf("%d.%d.%d\n", nextVer.Major(), nextVer.Minor(), nextVer.Patch())

		if err := git.Tag(nextVer); err != nil {
			return fmt.Errorf("error generating tag version: %s, message: %v", nextVer.String(), err)
		}
		return nil
	}
}

func commitHandler(cfg Config, git sv.Git, messageProcessor sv.MessageProcessor) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		ctype, err := promptType(cfg.CommitMessage.Types)
		if err != nil {
			return err
		}

		scope, err := promptScope(cfg.CommitMessage.Scope.Values)
		if err != nil {
			return err
		}

		subject, err := promptSubject()
		if err != nil {
			return err
		}

		var fullBody strings.Builder
		for body, err := promptBody(); body != "" || err != nil; body, err = promptBody() {
			if err != nil {
				return err
			}
			if fullBody.Len() > 0 {
				fullBody.WriteString("\n")
			}
			if body != "" {
				fullBody.WriteString(body)
			}
		}

		branchIssue, err := messageProcessor.IssueID(git.Branch())
		if err != nil {
			return err
		}

		var issue string
		if cfg.CommitMessage.IssueFooterConfig().Key != "" && cfg.CommitMessage.Issue.Regex != "" {
			issue, err = promptIssueID("issue id", cfg.CommitMessage.Issue.Regex, branchIssue)
			if err != nil {
				return err
			}
		}

		hasBreakingChanges, err := promptConfirm("has breaking changes?")
		if err != nil {
			return err
		}
		breakingChanges := ""
		if hasBreakingChanges {
			breakingChanges, err = promptBreakingChanges()
			if err != nil {
				return err
			}
		}

		header, body, footer := messageProcessor.Format(sv.NewCommitMessage(ctype.Type, scope, subject, fullBody.String(), issue, breakingChanges))

		err = git.Commit(header, body, footer)
		if err != nil {
			return fmt.Errorf("error executing git commit, message: %v", err)
		}
		return nil
	}
}

func changelogHandler(git sv.Git, semverProcessor sv.SemVerCommitsProcessor, rnProcessor sv.ReleaseNoteProcessor, formatter sv.OutputFormatter) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		tags, err := git.Tags()
		if err != nil {
			return err
		}
		sort.Slice(tags, func(i, j int) bool {
			return tags[i].Date.After(tags[j].Date)
		})

		var releaseNotes []sv.ReleaseNote

		size := c.Int("size")
		all := c.Bool("all")
		addNextVersion := c.Bool("add-next-version")

		if addNextVersion {
			rnVersion, updated, date, commits, uerr := getNextVersionInfo(git, semverProcessor)
			if uerr != nil {
				return uerr
			}
			if updated {
				releaseNotes = append(releaseNotes, rnProcessor.Create(&rnVersion, date, commits))
			}
		}
		for i, tag := range tags {
			if !all && i >= size {
				break
			}

			previousTag := ""
			if i+1 < len(tags) {
				previousTag = tags[i+1].Name
			}

			commits, err := git.Log(sv.NewLogRange(sv.TagRange, previousTag, tag.Name))
			if err != nil {
				return fmt.Errorf("error getting git log from tag: %s, message: %v", tag.Name, err)
			}

			currentVer, err := sv.ToVersion(tag.Name)
			if err != nil {
				return fmt.Errorf("error parsing version: %s from git tag, message: %v", tag.Name, err)
			}
			releaseNotes = append(releaseNotes, rnProcessor.Create(&currentVer, tag.Date, commits))
		}

		fmt.Println(formatter.FormatChangelog(releaseNotes))

		return nil
	}
}

func validateCommitMessageHandler(git sv.Git, messageProcessor sv.MessageProcessor) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		branch := git.Branch()
		detached, derr := git.IsDetached()

		if messageProcessor.SkipBranch(branch, derr == nil && detached) {
			warn("commit message validation skipped, branch in ignore list or detached...")
			return nil
		}

		if source := c.String("source"); source == "merge" {
			warn("commit message validation skipped, ignoring source: %s...", source)
			return nil
		}

		filepath := filepath.Join(c.String("path"), c.String("file"))

		commitMessage, err := readFile(filepath)
		if err != nil {
			return fmt.Errorf("failed to read commit message, error: %s", err.Error())
		}

		if err := messageProcessor.Validate(commitMessage); err != nil {
			return fmt.Errorf("invalid commit message, error: %s", err.Error())
		}

		msg, err := messageProcessor.Enhance(branch, commitMessage)
		if err != nil {
			warn("could not enhance commit message, %s", err.Error())
			return nil
		}
		if msg == "" {
			return nil
		}

		if err := appendOnFile(msg, filepath); err != nil {
			return fmt.Errorf("failed to append meta-informations on footer, error: %s", err.Error())
		}

		return nil
	}
}

func readFile(filepath string) (string, error) {
	f, err := ioutil.ReadFile(filepath)
	if err != nil {
		return "", err
	}
	return string(f), nil
}

func appendOnFile(message, filepath string) error {
	f, err := os.OpenFile(filepath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(message)
	return err
}

func str(value, defaultValue string) string {
	if value != "" {
		return value
	}
	return defaultValue
}
