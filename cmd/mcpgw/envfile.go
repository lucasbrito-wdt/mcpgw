package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const envFileVar = "MCPGW_ENV_FILE"

// extractEnvFile removes a global --env-file <path> / --env-file=<path> flag from args and
// returns the path (flag wins over MCPGW_ENV_FILE). An empty path means no env file.
func extractEnvFile(args []string, getenv func(string) string) (rest []string, path string, err error) {
	path = getenv(envFileVar)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--env-file" || a == "-env-file":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("--env-file requires a path")
			}
			path = args[i+1]
			i++
		case strings.HasPrefix(a, "--env-file="):
			path = strings.TrimPrefix(a, "--env-file=")
		default:
			rest = append(rest, a)
		}
	}
	return rest, path, nil
}

// loadEnvFile exports the KEY=VALUE lines of the file into the process environment so every
// consumer (config.Load, sandbox, i18n) sees them. Variables already set win. On unix the file
// is refused when group or others have any access, since it holds the master key.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s has mode %04o and may hold secrets (master key); run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	vars, err := parseEnvFile(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for k, v := range vars {
		if _, set := os.LookupEnv(k); set {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

// parseEnvFile reads KEY=VALUE lines. Blank lines and # comments are skipped; a value may be
// wrapped in one pair of matching single or double quotes. There is no expansion.
func parseEnvFile(r io.Reader) (map[string]string, error) {
	vars := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if n == 1 {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		vars[key] = unquoteEnvValue(strings.TrimSpace(value))
	}
	return vars, sc.Err()
}

func unquoteEnvValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
