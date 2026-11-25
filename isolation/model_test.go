package isolation

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/staysup/agentbus/connector"
)

type entry struct {
	kind byte
	data string
	exec bool
}

type tree map[string]entry

func readTree(t *testing.T, dir string) tree {
	t.Helper()
	out := tree{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(rel, ".isolation") || strings.HasPrefix(rel, ".agentbus") || rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = entry{kind: 'l', data: target}
		case fi.Mode().IsRegular():
			b, _ := os.ReadFile(p)
			out[rel] = entry{kind: 'f', data: string(b), exec: fi.Mode().Perm()&0o100 != 0}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var modelPaths = []string{"a", "b", "d/a", "d/b", "d/e/a", "e", "e/a", "d"}

type actor struct {
	r     *rand.Rand
	dir   string
	links bool
	modes bool
	log   []string
}

func (a *actor) clearParents(rel string) {
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		p := filepath.Join(a.dir, filepath.FromSlash(strings.Join(parts[:i], "/")))
		if fi, err := os.Lstat(p); err == nil && !fi.IsDir() {
			os.Remove(p)
		}
	}
}

func (a *actor) step(i int) {
	rel := modelPaths[a.r.IntN(len(modelPaths))]
	p := filepath.Join(a.dir, filepath.FromSlash(rel))
	switch op := a.r.IntN(10); {
	case op < 4:
		data := fmt.Sprintf("%s-%d-%d", rel, i, a.r.IntN(3))
		a.clearParents(rel)
		os.RemoveAll(p)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(data), 0o644)
		a.log = append(a.log, "write "+rel+" "+strconv.Quote(data))
	case op < 6:
		os.RemoveAll(p)
		a.log = append(a.log, "rm -r "+rel)
	case op < 7 && a.modes:
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			m := fi.Mode().Perm() ^ 0o111
			os.Chmod(p, m)
			a.log = append(a.log, fmt.Sprintf("chmod %o %s", m, rel))
		}
	case op < 8 && a.links:
		target := modelPaths[a.r.IntN(len(modelPaths))]
		a.clearParents(rel)
		os.RemoveAll(p)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.Symlink(target, p)
		a.log = append(a.log, "ln -s "+target+" "+rel)
	case op < 9:
		to := modelPaths[a.r.IntN(len(modelPaths))]
		q := filepath.Join(a.dir, filepath.FromSlash(to))
		if _, err := os.Lstat(p); err != nil || to == rel || strings.HasPrefix(to, rel+"/") {
			return
		}
		a.clearParents(to)
		os.RemoveAll(q)
		os.MkdirAll(filepath.Dir(q), 0o755)
		if os.Rename(p, q) == nil {
			a.log = append(a.log, "mv "+rel+" "+to)
		}
	default:
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			if len(b) > 0 {
				b[0] ^= 1
				os.WriteFile(p, b, fi.Mode().Perm())
				os.Chtimes(p, fi.ModTime(), fi.ModTime())
				a.log = append(a.log, "same-size edit keeping mtime "+rel)
			}
		}
	}
}

func keys(ts ...tree) []string {
	set := map[string]bool{}
	for _, t := range ts {
		for k := range t {
			set[k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func same(x, y entry, okx, oky bool, modes bool) bool {
	if okx != oky {
		return false
	}
	if !okx {
		return true
	}
	return x.kind == y.kind && x.data == y.data && (!modes || x.exec == y.exec)
}

func validTree(t tree) bool {
	for k := range t {
		parts := strings.Split(k, "/")
		for i := 1; i < len(parts); i++ {
			if _, ok := t[strings.Join(parts[:i], "/")]; ok {
				return false
			}
		}
	}
	return true
}

func show(t tree) string {
	var b strings.Builder
	for _, k := range keys(t) {
		e := t[k]
		fmt.Fprintf(&b, "    %s %c %q exec=%v\n", k, e.kind, e.data, e.exec)
	}
	return b.String()
}

func TestApplyModel(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("MODEL_SEEDS"))
	if n == 0 {
		t.Skip("MODEL_SEEDS unset")
	}
	start, _ := strconv.Atoi(os.Getenv("MODEL_START"))
	backends := []string{Copy}
	if runtime.GOOS == "linux" {
		backends = append(backends, Overlay)
	}
	links := runtime.GOOS != "windows"
	modes := runtime.GOOS != "windows"
	found := 0
	for _, name := range backends {
		for seed := start; seed < start+n && found < 5; seed++ {
			if name == Overlay && OverlayWhy(t.TempDir()) != "" {
				break
			}
			if msg := runModel(t, name, uint64(seed), links, modes); msg != "" {
				found++
				t.Errorf("backend %s seed %d:\n%s", name, seed, msg)
			}
		}
	}
}

func runModel(t *testing.T, name string, seed uint64, links, modes bool) string {
	r := rand.New(rand.NewPCG(seed, 7))
	src := t.TempDir()
	setup := &actor{r: r, dir: src, links: links, modes: modes}
	for i := range 1 + r.IntN(5) {
		setup.step(i)
	}
	old := time.Now().Add(-time.Hour)
	filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			os.Chtimes(p, old, old)
		}
		return nil
	})
	base := readTree(t, src)
	b, s := open(t, name, src)
	agent := &actor{r: r, dir: s.Dir, links: links, modes: modes}
	for i := range 1 + r.IntN(4) {
		agent.step(100 + i)
	}
	if _, err := b.Checkpoint(s, "t1"); err != nil {
		return "checkpoint: " + err.Error()
	}
	turn := readTree(t, s.Dir)
	user := &actor{r: r, dir: src, links: links, modes: modes}
	for i := range r.IntN(3) {
		user.step(200 + i)
	}
	folder := readTree(t, src)
	ctx := fmt.Sprintf("  setup: %v\n  agent: %v\n  user:  %v\n  base:\n%s  turn:\n%s  folder before apply:\n%s",
		setup.log, agent.log, user.log, show(base), show(turn), show(folder))

	want := tree{}
	conflict := false
	for _, k := range keys(base, turn, folder) {
		be, bok := base[k]
		te, tok := turn[k]
		fe, fok := folder[k]
		switch {
		case same(be, te, bok, tok, modes):
			if fok {
				want[k] = fe
			}
		case same(fe, be, fok, bok, false) || same(fe, te, fok, tok, false):
			if tok {
				want[k] = te
			}
		default:
			conflict = true
		}
	}
	if !validTree(want) {
		conflict = true
	}

	applied, err := applyIn(t, s, src, Base, "t1")
	after := readTree(t, src)
	var cf *connector.Conflict
	switch {
	case errors.As(err, &cf):
		if !equalTrees(after, folder, modes) {
			return "a refused apply changed the folder\n" + ctx + "  after:\n" + show(after)
		}
		if !conflict {
			return fmt.Sprintf("apply refused with no real conflict: %v\n%s", err, ctx)
		}
		return ""
	case err != nil:
		return fmt.Sprintf("apply failed: %v (applied %v)\n%s  after:\n%s", err, applied.Files, ctx, show(after))
	}
	if conflict {
		return fmt.Sprintf("apply succeeded over a real conflict (applied %v)\n%s  after:\n%s", applied.Files, ctx, show(after))
	}
	if !equalTrees(after, want, modes) {
		return fmt.Sprintf("apply said applied %v but the folder is wrong\n%s  want:\n%s  after:\n%s", applied.Files, ctx, show(want), show(after))
	}
	again, err := applyIn(t, s, src, Base, "t1")
	if err != nil || len(again.Files) > 0 || !equalTrees(readTree(t, src), after, modes) {
		return fmt.Sprintf("a second apply was not a no-op: %v %v\n%s", again.Files, err, ctx)
	}
	return ""
}

func equalTrees(x, y tree, modes bool) bool {
	for _, k := range keys(x, y) {
		xe, xok := x[k]
		ye, yok := y[k]
		if !same(xe, ye, xok, yok, modes) {
			return false
		}
	}
	return true
}
