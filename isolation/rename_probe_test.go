package isolation

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProbeRenameOverSameStat(t *testing.T) {
	backends := []string{Copy}
	if runtime.GOOS == "linux" && OverlayWhy(t.TempDir()) == "" {
		backends = append(backends, Overlay)
	}
	for _, name := range backends {
		for _, turns := range []int{1, 2} {
			src := t.TempDir()
			fixture(t, src, map[string]string{"old.go": "package one\n", "new.go": "package two\n", "x": "x"})
			tick := time.Now().Add(-time.Hour)
			for _, p := range []string{"old.go", "new.go", "x"} {
				os.Chtimes(filepath.Join(src, p), tick, tick)
			}
			b, s := open(t, name, src)
			from := Base
			if turns == 2 {
				fixture(t, s.Dir, map[string]string{"x": "y"})
				b.Checkpoint(s, "t1")
				if _, err := applyIn(t, s, src, Base, "t1"); err != nil {
					t.Fatal(err)
				}
				from = "t1"
			}
			if err := os.Rename(filepath.Join(s.Dir, "new.go"), filepath.Join(s.Dir, "old.go")); err != nil {
				t.Fatal(err)
			}
			b.Checkpoint(s, "t2")
			d, err := b.Diff(s, from, "t2")
			applied, aerr := applyIn(t, s, src, from, "t2")
			t.Logf("%s, rename in turn %d: diff count %d (%v), applied %v (%v), folder old.go=%q new.go=%q",
				name, turns, d.Count, err, applied.Files, aerr, read(t, filepath.Join(src, "old.go")), read(t, filepath.Join(src, "new.go")))
		}
	}
}

func TestProbeSameTick(t *testing.T) {
	dir := os.Getenv("PROBE_REPO")
	if dir == "" {
		t.Skip()
	}
	type key struct {
		size  int64
		mtime int64
	}
	seen := map[key]int{}
	files, same := 0, 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		fi, _ := d.Info()
		files++
		k := key{fi.Size(), fi.ModTime().UnixNano()}
		if seen[k] > 0 {
			same++
		}
		seen[k]++
		return nil
	})
	ticks := map[int64]bool{}
	for k := range seen {
		ticks[k.mtime] = true
	}
	t.Logf("%s: %d files, %d distinct mtimes, %d files share size+mtime with another", dir, files, len(ticks), same)
}
