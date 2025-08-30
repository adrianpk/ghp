package ghp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v61/github"
	"github.com/shurcooL/graphql"
	"golang.org/x/oauth2"
)

type ghRepo interface {
	DiscoverUserRepos(ctx context.Context, handle string, opt discoverOptions) ([]RepoTarget, error)
	GetLatestCommitSHA(ctx context.Context, owner, repo, ref string) (string, error)
	ListTree(ctx context.Context, owner, repo, ref, sha string) ([]string, error)
	ReadFile(ctx context.Context, owner, repo, ref, path, sha string) ([]byte, error)
}

type discoverOptions struct {
	Limit            int
	IncludePinned    bool
	IncludeNonPinned bool
	ExcludeForks     bool
}

type ghRepoImpl struct {
	restClient    *github.Client
	graphqlClient *graphql.Client
}

func newGitHubRepo(token string) (ghRepo, error) {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(context.Background(), src)
	return &ghRepoImpl{
		restClient:    github.NewClient(httpClient),
		graphqlClient: graphql.NewClient("https://api.github.com/graphql", httpClient),
	}, nil
}

type userRepoQuery struct {
	User struct {
		PinnedItems struct {
			Nodes []struct {
				OnRepository repoGraphQL `graphql:"... on Repository"`
			}
		} `graphql:"pinnedItems(first: 6, types: REPOSITORY)"`
		Repositories struct {
			Nodes []repoGraphQL
		} `graphql:"repositories(first: 100, ownerAffiliations: OWNER, orderBy: {field: PUSHED_AT, direction: DESC})"`
	} `graphql:"user(login: $login)"`
}

type repoGraphQL struct {
	NameWithOwner    string
	DefaultBranchRef struct {
		Name string
	}
	StargazerCount int
	IsFork         bool
	Owner          struct {
		Login string
	}
	Name            string
	PrimaryLanguage struct {
		Name  string
		Color string
	}
}

func (g *ghRepoImpl) DiscoverUserRepos(ctx context.Context, handle string, opt discoverOptions) ([]RepoTarget, error) {
	cachePath, err := getCachePath(fmt.Sprintf("repos-%s.json", handle))
	if err != nil {
		return nil, err
	}

	var cachedRepos []RepoTarget
	hit, err := readCache(cachePath, &cachedRepos, 1*time.Hour)
	if err != nil {
		fmt.Printf("warn: cache read error: %v\n", err)
	}
	if hit {
		return cachedRepos, nil
	}

	var query userRepoQuery
	variables := map[string]interface{}{
		"login": graphql.String(handle),
	}
	if err := g.graphqlClient.Query(ctx, &query, variables); err != nil {
		return nil, fmt.Errorf("graphql query: %w", err)
	}

	repoMap := make(map[string]RepoTarget)
	var pinnedOrder []string

	if opt.IncludePinned {
		for _, item := range query.User.PinnedItems.Nodes {
			r := item.OnRepository
			if r.NameWithOwner == "" {
				continue
			}
			if opt.ExcludeForks && r.IsFork {
				continue
			}
			repoMap[r.NameWithOwner] = repoGraphQLToTarget(r, true)
			pinnedOrder = append(pinnedOrder, r.NameWithOwner)
		}
	}

	if opt.IncludeNonPinned {
		for _, r := range query.User.Repositories.Nodes {
			if r.NameWithOwner == "" {
				continue
			}
			if _, exists := repoMap[r.NameWithOwner]; exists {
				continue
			}
			if opt.ExcludeForks && r.IsFork {
				continue
			}
			repoMap[r.NameWithOwner] = repoGraphQLToTarget(r, false)
		}
	}

	targets := make([]RepoTarget, 0, len(repoMap))
	for _, key := range pinnedOrder {
		targets = append(targets, repoMap[key])
		delete(repoMap, key)
	}

	var remaining []RepoTarget
	for _, repo := range repoMap {
		remaining = append(remaining, repo)
	}

	slices.SortFunc(remaining, func(a, b RepoTarget) int {
		return b.Stars - a.Stars
	})
	targets = append(targets, remaining...)

	if opt.Limit > 0 && len(targets) > opt.Limit {
		targets = targets[:opt.Limit]
	}

	if err := writeCache(cachePath, targets); err != nil {
		fmt.Printf("warn: cache write error: %v\n", err)
	}

	return targets, nil
}

func repoGraphQLToTarget(r repoGraphQL, pinned bool) RepoTarget {
	return RepoTarget{
		Owner:         r.Owner.Login,
		Name:          r.Name,
		DefaultBranch: r.DefaultBranchRef.Name,
		Stars:         r.StargazerCount,
		Pinned:        pinned,
		Language:      r.PrimaryLanguage.Name,
	}
}

func (g *ghRepoImpl) GetLatestCommitSHA(ctx context.Context, owner, repo, ref string) (string, error) {
	r, _, err := g.restClient.Git.GetRef(ctx, owner, repo, "heads/"+ref)
	if err != nil {
		return "", err
	}
	return r.GetObject().GetSHA(), nil
}

func (g *ghRepoImpl) ListTree(ctx context.Context, owner, repo, ref, sha string) ([]string, error) {
	cachePath, err := getCachePath(owner, repo, fmt.Sprintf("%s-tree.json", sha))
	if err != nil {
		return nil, err
	}
	var cachedTree []string
	hit, err := readCache(cachePath, &cachedTree, 24*30*time.Hour) // Long TTL for commit-based cache
	if err != nil {
		fmt.Printf("warn: cache read error: %v\n", err)
	}
	if hit {
		return cachedTree, nil
	}

	tree, _, err := g.restClient.Git.GetTree(ctx, owner, repo, sha, true)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(tree.Entries))
	for _, te := range tree.Entries {
		if te == nil || te.GetType() != "blob" {
			continue
		}
		paths = append(paths, te.GetPath())
	}

	if err := writeCache(cachePath, paths); err != nil {
		fmt.Printf("warn: cache write error: %v\n", err)
	}

	return paths, nil
}

func (g *ghRepoImpl) ReadFile(ctx context.Context, owner, repo, ref, path, sha string) ([]byte, error) {
	safePath := strings.ReplaceAll(path, "/", "_")
	cachePath, err := getCachePath(owner, repo, sha, fmt.Sprintf("%s.cache", safePath))
	if err != nil {
		return nil, err
	}

	var cachedContent []byte
	hit, err := readCache(cachePath, &cachedContent, 24*30*time.Hour)
	if err != nil {
		fmt.Printf("warn: cache read error: %v\n", err)
	}
	if hit {
		return cachedContent, nil
	}

	file, _, _, err := g.restClient.Repositories.GetContents(ctx, owner, repo, path, &github.RepositoryContentGetOptions{Ref: ref})
	if err != nil || file == nil {
		return nil, err
	}

	c, err := file.GetContent()
	if err != nil {
		return nil, err
	}
	contentBytes := []byte(c)

	if err := writeCache(cachePath, contentBytes); err != nil {
		fmt.Printf("warn: cache write error: %v\n", err)
	}

	return contentBytes, nil
}
