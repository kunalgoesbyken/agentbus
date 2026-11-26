package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/staysup/agentbus/workspace"
)

// A file the base recorded without a hash is known by its size, time and mode
// only. A program that moves a twin over it (mv, cp -p, rsync -a, ln), or
// writes it and puts its time back (touch -r), leaves all three as they were,
// so the person's new content must still be a conflict, never overwritten.
func TestApplyUnhashedBaseSeesWhatTheSizeAndTimeDoNot(t *testing.T) {
	tick := time.Now().Add(-time.Hour).Truncate(time.Second)
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	setTime := func(t *testing.T, p string) {
		t.Helper()
		must(t, os.Chtimes(p, tick, tick))
	}
	ops := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"a twin moved over the file", func(t *testing.T, dir string) {
			must(t, os.Rename(filepath.Join(dir, "twin.bin"), filepath.Join(dir, "old.bin")))
		}},
		{"an edit of the same size with the time put back", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "old.bin")
			must(t, os.WriteFile(p, []byte("two\n"), 0o644))
			setTime(t, p)
		}},
		{"a copy that keeps the time moved over the file", func(t *testing.T, dir string) {
			tmp := filepath.Join(dir, "tmp")
			must(t, os.WriteFile(tmp, []byte("two\n"), 0o644))
			setTime(t, tmp)
			must(t, os.Rename(tmp, filepath.Join(dir, "old.bin")))
		}},
		{"a hard link to the twin put where the file was", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "old.bin")
			must(t, os.Remove(p))
			must(t, os.Link(filepath.Join(dir, "twin.bin"), p))
		}},
	}
	for _, removed := range []bool{false, true} {
		for _, op := range ops {
			name := op.name
			if removed {
				name += ", and the turn removed it"
			}
			t.Run(name, func(t *testing.T) {
				folder := t.TempDir()
				write(t, folder, map[string]string{"old.bin": "one\n", "twin.bin": "two\n"})
				setTime(t, filepath.Join(folder, "old.bin"))
				setTime(t, filepath.Join(folder, "twin.bin"))
				fi, err := os.Lstat(filepath.Join(folder, "old.bin"))
				must(t, err)
				base := &workspace.File{Size: fi.Size(), Mode: fi.Mode(), ModTime: fi.ModTime().UnixNano()}
				var now *workspace.File
				contents := map[string]string{}
				if !removed {
					now = &workspace.File{Size: 6, Mode: fi.Mode(), Hash: workspace.TreeHash([]byte("agent\n"))}
					contents["n"] = "agent\n"
				}
				c, ref := newConnector(t, folder)
				h := putChangeset(t, c, workspace.Changeset{Space: "s", Checkpoint: "t1", Files: []workspace.FileChange{
					{Path: "old.bin", Base: base, Now: now}}}, contents)
				op.run(t, folder)
				theirs := text(t, filepath.Join(folder, "old.bin"))
				var cf *Conflict
				_, err = c.Apply(context.Background(), ref, "", h)
				if !errors.As(err, &cf) || len(cf.Files) != 1 || cf.Files[0] != (ConflictFile{"old.bin", WhyChanged}) {
					t.Fatalf("apply over the person's new content: %v", err)
				}
				if got := text(t, filepath.Join(folder, "old.bin")); got != theirs {
					t.Fatalf("old.bin is %q, the person had %q", got, theirs)
				}
			})
		}
	}
}

// The other side of it: a file nobody touched since the base was recorded is
// applied without being read.
func TestApplyUnhashedBaseOfAFileNobodyTouched(t *testing.T) {
	folder := t.TempDir()
	write(t, folder, map[string]string{"old.bin": "one\n"})
	then := time.Now().Add(-time.Hour)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Chtimes(filepath.Join(folder, "old.bin"), then, then))
	fi, err := os.Lstat(filepath.Join(folder, "old.bin"))
	must(err)
	base := &workspace.File{Size: fi.Size(), Mode: fi.Mode(), ModTime: fi.ModTime().UnixNano()}
	c, ref := newConnector(t, folder)
	h := putChangeset(t, c, workspace.Changeset{Space: "s", Checkpoint: "t1", Files: []workspace.FileChange{
		{Path: "old.bin", Base: base, Now: &workspace.File{Size: 6, Mode: fi.Mode(), Hash: workspace.TreeHash([]byte("agent\n"))}}}},
		map[string]string{"n": "agent\n"})
	if _, err := c.Apply(context.Background(), ref, "", h); err != nil {
		t.Fatal(err)
	}
	if got := text(t, filepath.Join(folder, "old.bin")); got != "agent\n" {
		t.Fatalf("old.bin is %q", got)
	}
}
