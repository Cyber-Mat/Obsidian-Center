package graph

// TraverseRequest configures a graph traversal.
type TraverseRequest struct {
	Start    string `json:"start"`
	MaxDepth int    `json:"max_depth"`
	MaxNodes int    `json:"max_nodes"`
}

// TraverseNode is a node in the traversal result.
type TraverseNode struct {
	Path  string   `json:"path"`
	Depth int      `json:"depth"`
	Links []string `json:"links,omitempty"` // outbound link targets
}

// Traverse performs a cycle-aware BFS from a starting path.
// It respects max_depth and max_nodes budgets.
func Traverse(store *Store, vaultID string, req TraverseRequest) ([]TraverseNode, error) {
	if req.MaxDepth <= 0 {
		req.MaxDepth = 1
	}
	if req.MaxNodes <= 0 {
		req.MaxNodes = 50
	}

	// Build adjacency list from all links in the vault
	allLinks, err := store.GetAllLinks(vaultID)
	if err != nil {
		return nil, err
	}

	adjacency := make(map[string][]string)
	for _, l := range allLinks {
		adjacency[l.SourcePath] = append(adjacency[l.SourcePath], l.TargetPath)
	}

	// BFS
	type queueItem struct {
		path  string
		depth int
	}

	visited := map[string]bool{}
	queue := []queueItem{{path: req.Start, depth: 0}}
	var result []TraverseNode

	for len(queue) > 0 && len(result) < req.MaxNodes {
		item := queue[0]
		queue = queue[1:]

		if visited[item.path] || item.depth > req.MaxDepth {
			continue
		}
		visited[item.path] = true

		outbound := adjacency[item.path]
		node := TraverseNode{
			Path:  item.path,
			Depth: item.depth,
			Links: outbound,
		}
		result = append(result, node)

		if item.depth < req.MaxDepth {
			for _, target := range outbound {
				if !visited[target] {
					queue = append(queue, queueItem{path: target, depth: item.depth + 1})
				}
			}
		}
	}

	return result, nil
}
