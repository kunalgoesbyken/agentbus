package isolation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/staysup/agentbus/connector"
)

// The overlay records its Base by size, time and mode, and a big file the
// turn resized or removed stays so in the changeset. The apply knows the
// person's file as the Base saw it by the same three, which a twin moved
// over it (mv, cp -p, rsync -a, ln) or an edit with its time put back
// (touch -r) leaves as they were: the person's new content is a conflict,
// not something to write over.
func TestApplyOverlayBigFileTheOtherSideChangedBySizeAndTimeAlone(t *testing.T) {
	if why := overlayWhy(t.TempDir()); why != "" {
		t.Skip(why)
	}
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
	mine, theirs := strings.Repeat("x", MaxFileDiff+10), strings.Repeat("y", MaxFileDiff+10)
	ops := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"a twin moved over the file", func(t *testing.T, dir string) {
			must(t, os.Rename(filepath.Join(dir, "twin.bin"), filepath.Join(dir, "big.bin")))
		}},
		{"an edit of the same size with the time put back", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "big.bin")
			must(t, os.WriteFile(p, []byte(theirs), 0o644))
			setTime(t, p)
		}},
		{"a copy that keeps the time moved over the file", func(t *testing.T, dir string) {
			tmp := filepath.Join(dir, "tmp")
			must(t, os.WriteFile(tmp, []byte(theirs), 0o644))
			setTime(t, tmp)
			must(t, os.Rename(tmp, filepath.Join(dir, "big.bin")))
		}},
		{"a hard link to the twin put where the file was", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "big.bin")
			must(t, os.Remove(p))
			must(t, os.Link(filepath.Join(dir, "twin.bin"), p))
		}},
	}
	turns := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"the turn resized it", func(t *testing.T, dir string) {
			fixture(t, dir, map[string]string{"big.bin": mine + "more"})
		}},
		{"the turn removed it", func(t *testing.T, dir string) {
			must(t, os.Remove(filepath.Join(dir, "big.bin")))
		}},
	}
	for _, turn := range turns {
		for _, op := range ops {
			t.Run(turn.name+"/"+op.name, func(t *testing.T) {
				src := t.TempDir()
				fixture(t, src, map[string]string{"big.bin": mine, "twin.bin": theirs})
				setTime(t, filepath.Join(src, "big.bin"))
				setTime(t, filepath.Join(src, "twin.bin"))
				b, s := open(t, Overlay, src)
				turn.run(t, s.Dir)
				if _, err := b.Checkpoint(s, "t1"); err != nil {
					t.Fatal(err)
				}
				op.run(t, src)
				want := read(t, filepath.Join(src, "big.bin"))
				_, err := applyIn(t, s, src, Base, "t1")
				var c *connector.Conflict
				if !errors.As(err, &c) {
					t.Fatalf("apply over the person's new content: %v", err)
				}
				if got := read(t, filepath.Join(src, "big.bin")); got != want {
					t.Fatalf("big.bin changed to %d bytes", len(got))
				}
			})
		}
	}
}
