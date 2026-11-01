package store

import "testing"

func TestSetsMaxConns(t *testing.T) {
	cases := map[string]bool{
		"postgres://u@h/db":                          false,
		"postgres://u@h/db?sslmode=disable":          false,
		"postgres://u@h/db?pool_max_conns=5":         true,
		"host=h dbname=db":                           false,
		"host=h dbname=db pool_max_conns=5":          true,
		"postgresql://u@h/db?pool_min_conns=1&x=y":   false,
		"postgresql://u@h/db?x=y&pool_max_conns=100": true,
	}
	for dsn, want := range cases {
		if got := setsMaxConns(dsn); got != want {
			t.Errorf("%s: got %v, want %v", dsn, got, want)
		}
	}
}
