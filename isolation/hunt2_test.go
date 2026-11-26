package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type updCase struct {
	name   string
	setup  map[string]string
	agent  func(t *testing.T, d string)
	person func(t *testing.T, d string)
	merge  bool
}

func TestHuntUpdate(t *testing.T) {
	cases := []updCase{
		{name: "person-renames-agent-edits", setup: map[string]string{"a.txt": "l1\nl2\nl3\nl4\nl5\n", "k": "k"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "L1\nl2\nl3\nl4\nl5\n"}) },
			person: func(t *testing.T, d string) { os.Rename(filepath.Join(d, "a.txt"), filepath.Join(d, "b.txt")) }},
		{name: "person-deletes-agent-edits", setup: map[string]string{"a.txt": "l1\nl2\n", "k": "k"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "L1\nl2\n"}) },
			person: func(t *testing.T, d string) { os.Remove(filepath.Join(d, "a.txt")) }},
		{name: "person-edits-agent-deletes", setup: map[string]string{"a.txt": "l1\nl2\n", "k": "k"},
			agent:  func(t *testing.T, d string) { os.Remove(filepath.Join(d, "a.txt")) },
			person: func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "l1\nl2\nl3\n"}) }},
		{name: "both-add-same-path", setup: map[string]string{"k": "k"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"x.txt": "agent\n"}) },
			person: func(t *testing.T, d string) { fixture(t, d, map[string]string{"x.txt": "person\n"}) }},
		{name: "both-same-edit", setup: map[string]string{"a.txt": "l1\nl2\n"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "l1\nSAME\n"}) },
			person: func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "l1\nSAME\n"}) }},
		{name: "person-chmod-agent-edit", setup: map[string]string{"a.sh": "l1\nl2\nl3\nl4\nl5\n"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.sh": "L1\nl2\nl3\nl4\nl5\n"}) },
			person: func(t *testing.T, d string) { os.Chmod(filepath.Join(d, "a.sh"), 0o755) }},
		{name: "person-crlf-agent-edit", setup: map[string]string{"a.txt": "l1\nl2\nl3\nl4\nl5\n"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "L1\nl2\nl3\nl4\nl5\n"}) },
			person: func(t *testing.T, d string) {
				fixture(t, d, map[string]string{"a.txt": "l1\r\nl2\r\nl3\r\nl4\r\nl5\r\n"})
			}},
		{name: "agent-rm-dir-person-adds-in-it", setup: map[string]string{"old/a": "a", "old/b": "b", "k": "k"},
			agent:  func(t *testing.T, d string) { os.RemoveAll(filepath.Join(d, "old")) },
			person: func(t *testing.T, d string) { fixture(t, d, map[string]string{"old/new": "n"}) }},
		{name: "person-edits-untouched-agent-edits-other", setup: map[string]string{"a": "a\n", "b": "b\n"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"a": "a2\n"}) },
			person: func(t *testing.T, d string) { fixture(t, d, map[string]string{"b": "b2\n"}) }},
		{name: "empty-files-rename-heuristic", setup: map[string]string{"pkg/a/__init__.py": "", "pkg/b/__init__.py": "", "pkg/a/m.py": "x\n"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"pkg/a/__init__.py": "# a\n"}) },
			person: func(t *testing.T, d string) {
				os.Remove(filepath.Join(d, "pkg", "a", "__init__.py"))
				fixture(t, d, map[string]string{"pkg/c/__init__.py": ""})
			}},
		{name: "person-rename-and-small-edit-agent-edit-other-part", setup: map[string]string{"a.go": "package a\n\nfunc A() {}\n\nfunc B() {}\n"},
			agent: func(t *testing.T, d string) {
				fixture(t, d, map[string]string{"a.go": "package a\n\nfunc A() { println(1) }\n\nfunc B() {}\n"})
			},
			person: func(t *testing.T, d string) {
				os.Remove(filepath.Join(d, "a.go"))
				fixture(t, d, map[string]string{"b.go": "package a\n\nfunc A() {}\n\nfunc B() { println(2) }\n"})
			}},
		{name: "merge-mode-conflict", merge: true, setup: map[string]string{"a.txt": "l1\nl2\nl3\n"},
			agent:  func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "A\nl2\nl3\n"}) },
			person: func(t *testing.T, d string) { fixture(t, d, map[string]string{"a.txt": "P\nl2\nl3\n"}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := t.TempDir()
			fixture(t, src, c.setup)
			b, s := open(t, Copy, src)
			c.agent(t, s.Dir)
			if _, err := b.Checkpoint(s, "t1"); err != nil {
				t.Logf("HUNT %s checkpoint: %v", c.name, err)
				return
			}
			c.person(t, src)
			res, err := b.UpdateFrom(s, "", at(src), c.merge)
			t.Logf("HUNT %s update: clean=%v conflicts=%v unmergeable=%v unresolved=%v err=%v", c.name, res.Clean, res.Conflicts, res.Unmergeable, res.Unresolved, err)
			if err != nil || !res.Clean {
				return
			}
			a, aerr := applyIn(t, s, src, res.Theirs, res.Merged)
			t.Logf("HUNT %s apply: %v err=%v", c.name, a.Files, aerr)
			for _, d := range huntDiff(huntTree(t, s.Dir), huntTree(t, src)) {
				t.Logf("HUNT %s MISMATCH %s", c.name, d)
			}
			_ = exec.Command
		})
	}
}
