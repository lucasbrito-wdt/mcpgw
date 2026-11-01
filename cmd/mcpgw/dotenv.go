package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"
)

// dotEnvMaxPerm is the broadest mode accepted without a warning: the file holds the master key.
const dotEnvMaxPerm fs.FileMode = 0o600

// loadDotEnv exports KEY=VALUE lines of the file into the process environment for local
// development. Variables already set win, and a missing file is not an error. A file readable
// by group or others (mode broader than 0600) is still loaded but a warning goes to warn.
func loadDotEnv(path string, warn io.Writer) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && runtime.GOOS != "windows" && info.Mode().Perm()&^dotEnvMaxPerm != 0 {
		fmt.Fprintf(warn, "mcpgw: warning: %s has mode %04o and may hold secrets (master key); run: chmod 600 %s\n",
			path, info.Mode().Perm(), path)
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, strings.Trim(strings.TrimSpace(value), `"'`)); err != nil {
			return err
		}
	}
	return sc.Err()
}
