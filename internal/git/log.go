package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// commitRecord is the normative per-commit format (spec Appendix A):
// fields separated by %x1f, records by %x1e, passed with -z so git also
// NUL-terminates records and never quotes paths.
const commitRecord = "%H%x1f%h%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%s%x1f%b%x1e"

// fieldSep and recordSep are the control characters of commitRecord.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// DefaultLogN is the default bounded history window.
const DefaultLogN = 50

// Commit is one entry of the history list.
type Commit struct {
	Hash, Abbrev string
	// Parents holds the full hashes of the parents; more than one means a
	// merge.
	Parents []string
	// Author and Email identify who made the change — agent identities
	// show up here.
	Author, Email string
	// At is the author timestamp.
	At time.Time
	// Subject is the first message line; Body is the rest, kept verbatim
	// because LLM messages are long and multi-paragraph.
	Subject, Body string
	// IsHead marks the commit currently checked out.
	IsHead bool
}

// Merge reports whether the commit is a merge (more than one parent).
func (c Commit) Merge() bool { return len(c.Parents) > 1 }

// LogOpts bounds and filters a history listing.
type LogOpts struct {
	// N is the window size; values below the default leave DefaultLogN in
	// place.
	N int
	// Since restricts history by date (git --since syntax).
	Since string
	// RevRange is an optional revision range such as main..feature.
	RevRange string
	// Paths restricts history to commits touching these pathspecs.
	Paths []string
}

// ListCommits lists up to N commits of the repository, newest first. A
// repository without commits yet yields an empty list, not an error.
func ListCommits(ctx context.Context, repo *Repo, opts LogOpts) ([]Commit, error) {
	if opts.N <= 0 {
		opts.N = DefaultLogN
	}

	// An unborn branch fails plain log with exit 128; the empty repository
	// is an expected state (the empty screen), not a failure.
	has, err := repo.HasCommits()
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}

	args := []string{"log", "-z", "-n", strconv.Itoa(opts.N), "--format=" + commitRecord}
	if opts.Since != "" {
		args = append(args, "--since="+opts.Since)
	}
	if opts.RevRange != "" {
		args = append(args, opts.RevRange)
	}
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		args = append(args, opts.Paths...)
	}

	out, err := NewRunner(repo.Root).Run(ctx, args...)
	if err != nil {
		return nil, err
	}

	commits := parseLog(out)
	markHead(ctx, repo, commits)
	return commits, nil
}

// markHead flags the commit that HEAD resolves to, if it is in the window.
// Failure is cosmetic (the flag only drives row highlighting), so errors
// are ignored.
func markHead(ctx context.Context, repo *Repo, commits []Commit) {
	out, err := NewRunner(repo.Root).Run(ctx, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return
	}
	head := strings.TrimSpace(string(out))
	if head == "" {
		return
	}
	for i := range commits {
		commits[i].IsHead = commits[i].Hash == head
	}
}

// parseLog splits a -z record stream into commits. Lenient by design:
// empty records are skipped and malformed records never abort the listing.
//
// With -z the byte layout between records is: ...body \x1e \x00 next...
// so every piece between two \x1e separators may carry one leading NUL,
// and the final record ends with a trailing one.
func parseLog(out []byte) []Commit {
	var commits []Commit
	for record := range strings.SplitSeq(string(out), recordSep) {
		record = strings.Trim(record, "\x00")
		if record == "" {
			continue
		}
		// SplitN keeps separator bytes inside the body when a message
		// itself contains \x1f: the body is the last field.
		fields := strings.SplitN(record, fieldSep, 8)
		if len(fields) < 8 {
			continue
		}
		at, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			continue
		}
		commits = append(commits, Commit{
			Hash:    fields[0],
			Abbrev:  fields[1],
			Parents: strings.Fields(fields[2]),
			Author:  fields[3],
			Email:   fields[4],
			At:      time.Unix(at, 0).UTC(),
			Subject: fields[6],
			Body:    fields[7],
		})
	}
	return commits
}
