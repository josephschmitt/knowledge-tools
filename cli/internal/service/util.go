package service

import (
	"os"
	"path/filepath"
	"strings"
)

// remove deletes a path if it exists, reporting whether something was removed.
func remove(path string) bool {
	if _, err := os.Lstat(path); err != nil {
		return false
	}
	return os.Remove(path) == nil
}

// anyMatch reports whether any entry in dir matches the shell-glob pattern (basename only).
func anyMatch(dir, pattern string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(matches) > 0
}

// tildify shortens a leading $HOME to ~ for friendlier output.
func tildify(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

// xmlEscape escapes a string for inclusion in a plist <string> value.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
