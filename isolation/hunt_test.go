package isolation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func huntTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, _ := os.Lstat(p)
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			tg, _ := os.Readlink(p)
			out[rel] = "L " + tg
		case fi.Mode().IsRegular():
			b, _ := os.ReadFile(p)
			h := sha256.Sum256(b)
			out[rel] = "F " + fi.Mode().Perm().String() + " " + hex.EncodeToString(h[:4]) + " len=" + itoa(len(b))
		case fi.IsDir():
			out[rel+"/"] = "D"
		}
		return nil
	})
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

func huntDiff(a, b map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	for k, v := range a {
		seen[k] = true
		if w, ok := b[k]; !ok {
			out = append(out, "only in COPY: "+k+" ("+v+")")
		} else if v != w {
			out = append(out, "differs: "+k+" copy="+v+" folder="+w)
		}
	}
	for k, v := range b {
		if !seen[k] {
			out = append(out, "only in FOLDER: "+k+" ("+v+")")
		}
	}
	sort.Strings(out)
	return out
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, a := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=x", "-c", "user.email=x@x", "commit", "-qm", "i"}} {
		c := exec.Command("git", append([]string{"-C", dir}, a...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Logf("git %v: %v %s", a, err, out)
		}
	}
}

type huntCase struct {
	name   string
	git    bool
	setup  map[string]string
	pre    func(t *testing.T, dir string)
	mutate func(t *testing.T, dir string)
}

func TestHuntMatrix(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<20) // 16 MiB
	cases := []huntCase{
		{name: "empty-dir", setup: map[string]string{"a.txt": "a"}, mutate: func(t *testing.T, d string) {
			os.MkdirAll(filepath.Join(d, "newdir", "inner"), 0o755)
		}},
		{name: "odd-names", setup: map[string]string{"a.txt": "a"}, mutate: func(t *testing.T, d string) {
			for _, n := range []string{"sp ace.txt", "ünï-çødé.txt", "日本語.txt", "-dash.txt", "q\"uote.txt", "back\\slash.txt", "new\nline.txt", ":(top)magic.txt", "!bang.txt", "#hash.txt", "[br]acket.txt", "*star.txt", "tab\there.txt", "a b/c d/e f.txt"} {
				fixture(t, d, map[string]string{n: "x:" + n})
			}
		}},
		{name: "modes", setup: map[string]string{"run.sh": "#!/bin/sh\n", "s.txt": "s"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{"new.sh": "#!/bin/sh\n", "new664.txt": "x", "new600.txt": "x", "new400.txt": "x", "new700.sh": "x"})
			os.Chmod(filepath.Join(d, "new.sh"), 0o755)
			os.Chmod(filepath.Join(d, "new664.txt"), 0o664)
			os.Chmod(filepath.Join(d, "new600.txt"), 0o600)
			os.Chmod(filepath.Join(d, "new400.txt"), 0o400)
			os.Chmod(filepath.Join(d, "new700.sh"), 0o700)
			os.Chmod(filepath.Join(d, "run.sh"), 0o644)
			os.Chmod(filepath.Join(d, "s.txt"), 0o755)
		}},
		{name: "crlf-binary", setup: map[string]string{"a.txt": "a\r\nb\r\n"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{"a.txt": "a\r\nb\r\nc\r\n", "mixed.txt": "a\nb\r\nc\r", "bin.dat": "\x00\x01\xff\xfe"})
		}},
		{name: "big-file", setup: map[string]string{"a.txt": "a"}, mutate: func(t *testing.T, d string) {
			os.WriteFile(filepath.Join(d, "big.bin"), big, 0o644)
		}},
		{name: "empty-file-roundtrip", setup: map[string]string{"e.txt": "x", "f.txt": ""}, mutate: func(t *testing.T, d string) {
			os.WriteFile(filepath.Join(d, "e.txt"), nil, 0o644)
			os.WriteFile(filepath.Join(d, "f.txt"), []byte("now"), 0o644)
			os.WriteFile(filepath.Join(d, "g.txt"), nil, 0o644)
		}},
		{name: "symlinks", setup: map[string]string{"a.txt": "a", "dir/x.txt": "x"}, pre: func(t *testing.T, d string) {
			os.Symlink("a.txt", filepath.Join(d, "ln"))
			os.Symlink("dir", filepath.Join(d, "dln"))
		}, mutate: func(t *testing.T, d string) {
			os.Symlink("nowhere", filepath.Join(d, "dangling"))
			os.Symlink("/etc/hosts", filepath.Join(d, "abs"))
			os.Remove(filepath.Join(d, "dln"))
			os.Symlink("a.txt", filepath.Join(d, "dln"))
			os.Remove(filepath.Join(d, "ln"))
			fixture(t, d, map[string]string{"ln": "now a file"})
		}},
		{name: "rename-dir", setup: map[string]string{"old/a.go": "a", "old/b/c.go": "c", "keep.go": "k"}, mutate: func(t *testing.T, d string) {
			os.Rename(filepath.Join(d, "old"), filepath.Join(d, "new"))
		}},
		{name: "delete-dir-all", setup: map[string]string{"old/a.go": "a", "old/b/c.go": "c", "keep.go": "k"}, mutate: func(t *testing.T, d string) {
			os.RemoveAll(filepath.Join(d, "old"))
		}},
		{name: "dir-to-file-and-back", setup: map[string]string{"d/a.go": "a", "f": "file"}, mutate: func(t *testing.T, d string) {
			os.RemoveAll(filepath.Join(d, "d"))
			fixture(t, d, map[string]string{"d": "now file"})
			os.Remove(filepath.Join(d, "f"))
			fixture(t, d, map[string]string{"f/inner.txt": "in"})
		}},
		{name: "gitignore-patterns", setup: map[string]string{".gitignore": "*.log\n.env\nsecrets/\n/out\n!keep.log\n", "a.txt": "a", ".env": "TOKEN=1\n"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{"x.log": "log", "keep.log": "keep", ".env": "TOKEN=2\n", "secrets/k": "k", "out/o": "o", "new.txt": "n", "sub/y.log": "y"})
		}},
		{name: "gitignore-edit", setup: map[string]string{".gitignore": "*.log\n", "a.txt": "a"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{".gitignore": "*.log\n*.tmp\n", "b.tmp": "b", "sub/.gitignore": "*\n", "sub/z.txt": "z", "data/.gitkeep": ""})
		}},
		{name: "builtin-ignored-dirs-new", setup: map[string]string{"src/a.go": "a"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{"build/script.sh": "x", "dist/readme.md": "x", "target/t.txt": "x", "venv/v.txt": "x", ".next/n": "x", "src/build/ok.go": "x", "node_modules/p/i.js": "x", "__pycache__/a.pyc": "x"})
		}},
		{name: "tracked-in-git-ignored", git: true, setup: map[string]string{".gitignore": "*.local\n", "a.txt": "a", "app.local": "v1"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{"app.local": "v2", "a.txt": "a2"})
		}},
		{name: "nested-repo-and-submodule-file", setup: map[string]string{"a.txt": "a"}, pre: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{"vendor/lib/x.go": "x"})
			gitInit(t, filepath.Join(d, "vendor", "lib"))
		}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{"vendor/lib/x.go": "x2", "vendor/lib/new.go": "n"})
		}},
		{name: "deep-path", setup: map[string]string{"a.txt": "a"}, mutate: func(t *testing.T, d string) {
			p := "d"
			for i := 0; i < 40; i++ {
				p += "/dir" + strings.Repeat("x", 5)
			}
			fixture(t, d, map[string]string{p + "/f.txt": "deep"})
		}},
		{name: "long-name", setup: map[string]string{"a.txt": "a"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{strings.Repeat("n", 250) + ".txt": "long"})
		}},
		{name: "readonly-file-edit", setup: map[string]string{"ro.txt": "ro", "w.txt": "w"}, pre: func(t *testing.T, d string) {
			os.Chmod(filepath.Join(d, "ro.txt"), 0o444)
		}, mutate: func(t *testing.T, d string) {
			os.Chmod(filepath.Join(d, "ro.txt"), 0o644)
			os.WriteFile(filepath.Join(d, "ro.txt"), []byte("rw now"), 0o644)
			os.Chmod(filepath.Join(d, "ro.txt"), 0o444)
		}},
		{name: "readonly-dir", setup: map[string]string{"rd/a.txt": "a"}, pre: func(t *testing.T, d string) {
			os.Chmod(filepath.Join(d, "rd"), 0o555)
		}, mutate: func(t *testing.T, d string) {
			os.Chmod(filepath.Join(d, "rd"), 0o755)
			os.WriteFile(filepath.Join(d, "rd", "a.txt"), []byte("edited"), 0o644)
			os.WriteFile(filepath.Join(d, "rd", "b.txt"), []byte("new"), 0o644)
			os.Chmod(filepath.Join(d, "rd"), 0o555)
		}},
		{name: "git-top-file", setup: map[string]string{"a.txt": "a"}, mutate: func(t *testing.T, d string) {
			fixture(t, d, map[string]string{".gitattributes": "* text eol=crlf\n", "x.txt": "line\n", ".gitmodules": "[submodule]\n", "sub/.git": "gitdir: ../x\n"})
		}},
		{name: "swap-two-files", setup: map[string]string{"a.txt": "AAA", "b.txt": "BBB"}, mutate: func(t *testing.T, d string) {
			os.Rename(filepath.Join(d, "a.txt"), filepath.Join(d, "t"))
			os.Rename(filepath.Join(d, "b.txt"), filepath.Join(d, "a.txt"))
			os.Rename(filepath.Join(d, "t"), filepath.Join(d, "b.txt"))
		}},
		{name: "same-len-edit", setup: map[string]string{"a.txt": "AAAA"}, mutate: func(t *testing.T, d string) {
			os.WriteFile(filepath.Join(d, "a.txt"), []byte("BBBB"), 0o644)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := t.TempDir()
			fixture(t, src, c.setup)
			if c.pre != nil {
				c.pre(t, src)
			}
			if c.git {
				gitInit(t, src)
			}
			b, s := open(t, Copy, src)
			defer func() {
				filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
					if err == nil && d.IsDir() {
						os.Chmod(p, 0o755)
					}
					return nil
				})
				filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
					if err == nil && d.IsDir() {
						os.Chmod(p, 0o755)
					}
					return nil
				})
			}()
			c.mutate(t, s.Dir)
			if _, err := b.Checkpoint(s, "t1"); err != nil {
				t.Logf("checkpoint error: %v", err)
				return
			}
			a, err := applyIn(t, s, src, Base, "t1")
			t.Logf("apply: files=%d err=%v", len(a.Files), err)
			diffs := huntDiff(huntTree(t, s.Dir), huntTree(t, src))
			for _, d := range diffs {
				t.Logf("MISMATCH %s", d)
			}
			if len(diffs) == 0 {
				t.Logf("same")
			}
		})
	}
}
