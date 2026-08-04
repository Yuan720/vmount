package fs

import (
	"strings"
	"sync"
)

type caseMap struct {
	mu    sync.RWMutex
	dirs  map[string]map[string]string
}

func newCaseMap() *caseMap {
	return &caseMap{dirs: map[string]map[string]string{}}
}

func (c *caseMap) Update(dir string, names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := map[string]string{}
	for _, n := range names {
		m[strings.ToLower(n)] = n
	}
	c.dirs[dir] = m
}

func (c *caseMap) Lookup(dir, name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.dirs[dir]
	if !ok {
		return "", false
	}
	actual, ok := m[strings.ToLower(name)]
	return actual, ok
}

func (c *caseMap) ParentDir(path string) (string, string) {
	path = strings.Trim(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "", path
	}
	return path[:idx], path[idx+1:]
}

func (c *caseMap) Resolve(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	segs := strings.Split(path, "/")
	out := make([]string, 0, len(segs))
	dir := ""
	for i, seg := range segs {
		if i > 0 {
			dir = strings.Join(out, "/")
		}
		if actual, ok := c.Lookup(dir, seg); ok {
			out = append(out, actual)
		} else {
			out = append(out, seg)
		}
	}
	return strings.Join(out, "/")
}
