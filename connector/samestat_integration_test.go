//go:build integration && (linux || darwin)

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

// A file the base recorded without a hash is known by its size, time and mode,
// and a program that moves a twin over it (mv, cp -p, rsync -a, ln), or
// writes it and puts its time back (touch -r), leaves all three as they were.
// Its change time moved, which no program can set back, and it is later than
// when the Base began to read the folder: the person's new content is a
// conflict, never written over. The rows that touch nothing, or that change
// what the stat already tells, take the same time and read the same.
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
		name     string
		conflict bool
		run      func(t *testing.T, dir string)
	}{
		{"a twin moved over the file", true, func(t *testing.T, dir string) {
			must(t, os.Rename(filepath.Join(dir, "twin.bin"), filepath.Join(dir, "old.bin")))
		}},
		{"an edit of the same size with the time put back", true, func(t *testing.T, dir string) {
			p := filepath.Join(dir, "old.bin")
			must(t, os.WriteFile(p, []byte("two\n"), 0o644))
			setTime(t, p)
		}},
		{"a copy that keeps the time moved over the file", true, func(t *testing.T, dir string) {
			tmp := filepath.Join(dir, "tmp")
			must(t, os.WriteFile(tmp, []byte("two\n"), 0o644))
			setTime(t, tmp)
			must(t, os.Rename(tmp, filepath.Join(dir, "old.bin")))
		}},
		{"a hard link to the twin put where the file was", true, func(t *testing.T, dir string) {
			p := filepath.Join(dir, "old.bin")
			must(t, os.Remove(p))
			must(t, os.Link(filepath.Join(dir, "twin.bin"), p))
		}},
		{"a mode change alone", true, func(t *testing.T, dir string) {
			must(t, os.Chmod(filepath.Join(dir, "old.bin"), 0o755))
		}},
		{"an edit that moves the time", true, func(t *testing.T, dir string) {
			must(t, os.WriteFile(filepath.Join(dir, "old.bin"), []byte("two\n"), 0o644))
		}},
		{"nothing", false, func(t *testing.T, dir string) {}},
		{"a file of the person's beside it", false, func(t *testing.T, dir string) {
			must(t, os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("m"), 0o644))
		}},
	}
	for _, removed := range []bool{false, true} {
		for _, op := range ops {
			name := op.name
			if removed {
				name += ", and the turn removed it"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				folder := t.TempDir()
				write(t, folder, map[string]string{"old.bin": "one\n", "twin.bin": "two\n"})
				setTime(t, filepath.Join(folder, "old.bin"))
				setTime(t, filepath.Join(folder, "twin.bin"))
				// The files are older than the Base's clock tick, as a
				// project's are, and the Base then reads the folder.
				time.Sleep(workspace.Racy + 100*time.Millisecond)
				c, ref, h := unhashedChange(t, folder, removed, time.Now().UnixNano())
				op.run(t, folder)
				theirs := text(t, filepath.Join(folder, "old.bin"))
				var cf *Conflict
				_, err := c.Apply(context.Background(), ref, "", h)
				if op.conflict {
					if !errors.As(err, &cf) || len(cf.Files) != 1 || cf.Files[0] != (ConflictFile{"old.bin", WhyChanged}) {
						t.Fatalf("apply over the person's new content: %v", err)
					}
					if got := text(t, filepath.Join(folder, "old.bin")); got != theirs {
						t.Fatalf("old.bin is %q, the person had %q", got, theirs)
					}
					return
				}
				if err != nil {
					t.Fatalf("apply of a file nobody changed: %v", err)
				}
			})
		}
	}
}
