package queries

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/store"
)

// lookupFileNode returns a FileNode of type "file" if `path` matches an
// exact file in the graph, or nil if no such file exists.
func lookupFileNode(ctx context.Context, s *store.Store, path string) (*model.FileNode, error) {
	var p string
	var size int64
	var lang string
	err := s.DB().QueryRowContext(ctx,
		`SELECT path, size, lang FROM files WHERE path = ? LIMIT 1`, path,
	).Scan(&p, &size, &lang)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	name := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		name = path[i+1:]
	}
	return &model.FileNode{Name: name, Path: p, Type: "file", Size: size, Lang: lang}, nil
}

// FileTree returns a nested tree of the indexed files under `prefix`,
// capped at `maxDepth` directory levels and optionally filtered by
// extension. The MCP handler applies its own default (2) when the
// caller omits the field; here, a `maxDepth` of 0 means unlimited and
// a negative value is clamped to 0.
func FileTree(ctx context.Context, s *store.Store, prefix string, maxDepth int, includeExts []string) (*model.FileNode, error) {
	prefix = strings.TrimPrefix(prefix, "/")
	if prefix != "" {
		if exact, err := lookupFileNode(ctx, s, prefix); err != nil {
			return nil, err
		} else if exact != nil {
			return exact, nil
		}
	}
	like := prefix + "%"
	rows, err := s.DB().QueryContext(ctx,
		`SELECT path, size, lang FROM files WHERE path LIKE ? ORDER BY path`, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	extSet := map[string]bool{}
	for _, e := range includeExts {
		if e != "" {
			extSet[strings.ToLower(strings.TrimPrefix(e, "."))] = true
		}
	}

	var paths []string
	type fmeta struct {
		size int64
		lang string
	}
	meta := map[string]fmeta{}
	for rows.Next() {
		var p string
		var size int64
		var lang string
		if err := rows.Scan(&p, &size, &lang); err != nil {
			return nil, err
		}
		if len(extSet) > 0 {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(p), "."))
			if !extSet[ext] {
				continue
			}
		}
		paths = append(paths, p)
		meta[p] = fmeta{size: size, lang: lang}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rootPath := prefix
	if rootPath != "" && !strings.HasSuffix(rootPath, "/") {
		rootPath += "/"
	}
	prefixSlash := rootPath

	var root *model.FileNode
	if prefix == "" {
		root = &model.FileNode{Name: ".", Path: ".", Type: "dir"}
	} else {
		root = &model.FileNode{Name: prefix, Path: prefix, Type: "dir"}
	}

	if len(paths) == 0 {
		return root, nil
	}

	dirs := map[string]*model.FileNode{}
	dirs[prefix] = root

	if maxDepth < 0 {
		maxDepth = 0
	}

	for _, p := range paths {
		rel := strings.TrimPrefix(p, prefixSlash)
		if rel == "" {
			continue
		}
		parts := strings.Split(rel, "/")
		if maxDepth > 0 && len(parts) > maxDepth {
			continue
		}
		cur := prefix
		curNode := root
		for i, part := range parts {
			cur = strings.TrimSuffix(cur, "/")
			if cur == "" {
				cur = part
			} else {
				cur = cur + "/" + part
			}
			isFile := i == len(parts)-1
			if isFile {
				fn := &model.FileNode{
					Name: part,
					Path: p,
					Type: "file",
					Size: meta[p].size,
					Lang: meta[p].lang,
				}
				curNode.Children = append(curNode.Children, fn)
			} else {
				next, ok := dirs[cur]
				if !ok {
					next = &model.FileNode{Name: part, Path: cur, Type: "dir"}
					dirs[cur] = next
					curNode.Children = append(curNode.Children, next)
				}
				curNode = next
			}
		}
	}
	return root, nil
}
