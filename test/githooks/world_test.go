// Package githooks tests the repo's git hooks and the Makefile's git setup against
// throwaway repositories. It holds tests only: Go ignores .githooks/ because of its
// leading dot, so the tests cannot live next to the hooks.
package githooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// world is a work repo with a bare repo as its origin, both under t.TempDir().
type world struct {
	t    *testing.T
	root string // the real repository this test runs in
	dir  string // the work repo
	env  []string
}

func newWorld(t *testing.T) *world {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	w := &world{
		t:    t,
		root: root,
		dir:  filepath.Join(tmp, "work"),
		env:  cleanEnv(tmp),
	}

	remote := filepath.Join(tmp, "remote.git")
	w.run(tmp, "git", "init", "-q", "--bare", "-b", "main", remote)
	w.run(tmp, "git", "init", "-q", "-b", "main", w.dir)
	w.git("config", "user.name", "Test")
	w.git("config", "user.email", "test@example.com")
	w.git("remote", "add", "origin", remote)
	return w
}

// cleanEnv is the parent environment without anything git or make would pick up
// from an outer `git push` or `make verify`, and without the user's own git config.
func cleanEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") || name == "HOME" ||
			name == "MAKEFLAGS" || name == "MFLAGS" || name == "MAKELEVEL" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

// run runs a command in dir and fails the test if it exits non-zero.
func (w *world) run(dir, name string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = w.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (w *world) git(args ...string) string {
	w.t.Helper()
	return w.run(w.dir, "git", args...)
}

// makeReal runs a target of the real repository's Makefile inside the work repo.
func (w *world) makeReal(target string) {
	w.t.Helper()
	w.run(w.dir, "make", "--no-print-directory", "-f", filepath.Join(w.root, "Makefile"), target)
}
