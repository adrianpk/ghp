package ghp

import (
	"context"
	"embed"
	"fmt"
	"html"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type Service interface {
	Submit(ctx context.Context, user string) (html string, err error)
}

type service struct {
	cfg                *Config
	llm                Client
	repoPrompt         string
	summaryPrompt      string
	headlinePrompt     string
	standardArchPrompt string
	monoRepoArchPrompt string
	gh                 ghRepo
}

func NewService(cfg *Config, client Client, fsys embed.FS) (Service, error) {
	repoPrompt, _, err := loadPrompt(fsys, cfg.App.PromptPath, "prompts/prompt.txt")
	if err != nil {
		return nil, fmt.Errorf("repo prompt: %w", err)
	}

	summaryPrompt, _, err := loadPrompt(fsys, "", "prompts/summary.txt")
	if err != nil {
		return nil, fmt.Errorf("summary prompt: %w", err)
	}

	headlinePrompt, _, err := loadPrompt(fsys, "", "prompts/headline_summary.txt")
	if err != nil {
		return nil, fmt.Errorf("headline prompt: %w", err)
	}

	standardArchPrompt, _, err := loadPrompt(fsys, "", "prompts/arch_standard.txt")
	if err != nil {
		return nil, fmt.Errorf("standard arch prompt: %w", err)
	}

	monoRepoArchPrompt, _, err := loadPrompt(fsys, "", "prompts/arch_monorepo.txt")
	if err != nil {
		return nil, fmt.Errorf("monorepo arch prompt: %w", err)
	}

	gr, err := newGitHubRepo(cfg.Auth.GithubToken)
	if err != nil {
		return nil, err
	}

	return &service{
		cfg:                cfg,
		llm:                client,
		repoPrompt:         repoPrompt,
		summaryPrompt:      summaryPrompt,
		headlinePrompt:     headlinePrompt,
		standardArchPrompt: standardArchPrompt,
		monoRepoArchPrompt: monoRepoArchPrompt,
		gh:                 gr,
	}, nil
}

func (s *service) Submit(ctx context.Context, user string) (string, error) {
	fmt.Printf("Discovering repositories for @%s...\n", user)
	repos, err := s.gh.DiscoverUserRepos(ctx, user, discoverOptions{
		Limit:            s.cfg.App.ReposLimit,
		IncludePinned:    s.cfg.App.IncludePinned,
		IncludeNonPinned: s.cfg.App.IncludeNonPinned,
		ExcludeForks:     s.cfg.App.ExcludeForks,
	})
	if err != nil {
		return "", err
	}
	if len(repos) == 0 {
		return "", fmt.Errorf("no repositories for @%s", user)
	}

	fmt.Printf("%d repositories found. Analyzing...\n", len(repos))

	results := make([]RepoResult, len(repos))
	wg := sync.WaitGroup{}
	sem := make(chan struct{}, s.cfg.LLM.ParallelRequests)

	for i := range repos {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fmt.Printf("Analyzing repo: %s/%s...\n", repos[i].Owner, repos[i].Name)
			res, _ := s.evaluateRepo(ctx, repos[i])
			results[i] = res
			fmt.Printf("Repo %s/%s analyzed.\n", repos[i].Owner, repos[i].Name)
		}()
	}
	wg.Wait()

	fmt.Println("All repos analyzed. Generating report...")

	slices.SortFunc(results, func(a, b RepoResult) int { return b.Score - a.Score })

	headlineHTML := s.generateHeadlineWithLLM(ctx, user, results)
	summaryHTML := s.generateSummaryWithLLM(ctx, user, results)
	return renderHTML(user, results, headlineHTML, summaryHTML), nil
}

func (s *service) generateHeadlineWithLLM(ctx context.Context, user string, results []RepoResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Repository Analysis Table:\n")
	b.WriteString("Repo\tScore\tStrengths\tRisks\n")
	for _, r := range results {
		strengths := []string{}
		for _, s := range r.ArchStrengths {
			strengths = append(strengths, s.Point)
		}
		risks := []string{}
		for _, c := range r.ArchConsiderations {
			risks = append(risks, c.Point)
		}
		b.WriteString(fmt.Sprintf("%s/%s\t%d\t%s\t%s\n",
			r.Repo.Owner, r.Repo.Name, r.Score,
			strings.Join(strengths, ", "),
			strings.Join(risks, ", ")))
	}

	prompt := strings.NewReplacer("{{.SummaryData}}", b.String()).Replace(s.headlinePrompt)

	type headlineOut struct {
		Headline string `json:"headline"`
	}
	var out headlineOut
	err := s.llm.EvaluateJSON(ctx, EvalInput{
		Prompt: prompt,
		Owner:  user,
		Repo:   "(all)",
		Chunks: []Chunk{{Path: "summary", Content: b.String(), Language: "text"}},
	}, &out)

	if err != nil || out.Headline == "" {
		return ""
	}
	return fmt.Sprintf(`<section class="mt-4 mb-8 p-4 bg-sky-50 border-l-4 border-sky-400"><p class="text-sky-800">%s</p></section>`, html.EscapeString(out.Headline))
}

func (s *service) generateSummaryWithLLM(ctx context.Context, user string, results []RepoResult) string {
	if len(results) == 0 {
		return "No repositories were analyzed."
	}
	var b strings.Builder
	b.WriteString("Repository Analysis Table:\n")
	b.WriteString("Repo\tScore\tStrengths\tRisks\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("%s/%s\t%d\t%s\t%s\n",
			r.Repo.Owner, r.Repo.Name, r.Score,
			strings.Join(r.Strengths, ", "),
			strings.Join(r.Risks, ", ")))
	}

	prompt := strings.NewReplacer("{{.SummaryData}}", b.String()).Replace(s.summaryPrompt)

	type summaryOut struct {
		Summary string `json:"summary"`
	}
	var out summaryOut
	err := s.llm.EvaluateJSON(ctx, EvalInput{
		Prompt: prompt,
		Owner:  user,
		Repo:   "(all)",
		Branch: "",
		Chunks: []Chunk{{Path: "summary", Content: b.String(), Language: "text"}},
	}, &out)
	if err != nil || out.Summary == "" {
		return `<section class="mt-8 p-4 bg-yellow-50 border-l-4 border-yellow-400"><strong>AI Summary:</strong> <em>Summary unavailable.</em></section>`
	}
	return fmt.Sprintf(`<section class="mt-8 p-4 bg-yellow-50 border-l-4 border-yellow-400"><strong>AI Summary:</strong> %s</section>`, html.EscapeString(out.Summary))
}

func (s *service) evaluateRepo(ctx context.Context, repo RepoTarget) (RepoResult, error) {
	sha, err := s.gh.GetLatestCommitSHA(ctx, repo.Owner, repo.Name, repo.DefaultBranch)
	if err != nil {
		fmt.Printf("warn: could not get commit SHA for %s/%s: %v\n", repo.Owner, repo.Name, err)
	}

	fmt.Printf("Fetching file tree for %s/%s (sha: %s)...\n", repo.Owner, repo.Name, sha[:7])
	tree, err := s.gh.ListTree(ctx, repo.Owner, repo.Name, repo.DefaultBranch, sha)
	if err != nil {
		return RepoResult{Repo: repo}, err
	}

	// Architectural Analysis
	archResult := s.evaluateArchitecture(ctx, repo, tree)

	paths := pickPaths(tree, s.cfg)
	fmt.Printf("%d files selected for %s/%s\n", len(paths), repo.Owner, repo.Name)
	chunks, _ := s.sampleChunks(ctx, repo, paths, sha)
	if len(chunks) == 0 {
		fmt.Printf("No chunks for %s/%s\n", repo.Owner, repo.Name)
		return RepoResult{Repo: repo}, nil
	}

	in := EvalInput{
		Prompt: s.repoPrompt, Owner: repo.Owner, Repo: repo.Name, Branch: repo.DefaultBranch,
		Chunks: toLLMChunks(chunks),
	}
	var scores []ChunkScore
	err = s.llm.EvaluateJSON(ctx, in, &scores)
	if err != nil {
		fmt.Printf("LLM error in %s/%s: %v\n", repo.Owner, repo.Name, err)
	}

	var sum float64
	var cnt float64
	var strengths, risks []string
	var samples []struct{ URL, Note string }

	for i, sc := range scores {
		total := sc.Readability + sc.Design + sc.Testing + sc.Maintain + sc.Idiomatic + sc.Security
		if total == 0 {
			continue
		}
		w := 1.0
		p := strings.ToLower(chunks[i].Path)
		if strings.Contains(p, "test") {
			w += 0.1
		}
		if strings.HasPrefix(p, "cmd/") || strings.Contains(p, "/internal/") {
			w += 0.2
		}
		sum += (float64(total) / 30.0) * w
		cnt += w

		if len(samples) < 3 && len(sc.Citations) > 0 {
			samples = append(samples, struct{ URL, Note string }{
				URL:  fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", repo.Owner, repo.Name, repo.DefaultBranch, chunks[i].Path),
				Note: first(sc.Notes),
			})
		}
		for _, n := range sc.Notes {
			ln := strings.ToLower(n)
			switch {
			case strings.Contains(ln, "well-structured"), strings.Contains(ln, "idiomatic"), strings.Contains(ln, "tested"):
				if len(strengths) < 3 {
					strengths = append(strengths, n)
				}
			case strings.Contains(ln, "missing tests"), strings.Contains(ln, "long function"),
				strings.Contains(ln, "globals"), strings.Contains(ln, "security"), strings.Contains(ln, "concurrency"):
				if len(risks) < 3 {
					risks = append(risks, n)
				}
			}
		}
	}

	final := 0
	if cnt > 0 {
		final = clamp(int((sum/cnt)*100.0), 0, 100)
	}

	return RepoResult{
		Repo:          repo,
		Score:         final,
		Strengths:     strengths,
		Risks:         risks,
		ArchStrengths:      archResult.ArchStrengths,
		ArchConsiderations: archResult.ArchConsiderations,
		Samples:       samples,
		Files:         len(paths),
		Chunks:        len(chunks),
	}, nil
}

type archScore struct {
	ArchStrengths      []ArchStrength      `json:"arch_strengths"`
	ArchConsiderations []ArchConsideration `json:"arch_considerations"`
}

func (s *service) evaluateArchitecture(ctx context.Context, repo RepoTarget, tree []string) archScore {
	repoType := detectRepoType(tree)
	var prompt string
	if repoType == "monorepo" {
		prompt = s.monoRepoArchPrompt
	} else {
		prompt = s.standardArchPrompt
	}

	r := strings.NewReplacer(
		"{{.Language}}", repo.Language,
		"{{.Tree}}", strings.Join(tree, "\n"),
	)
	finalPrompt := r.Replace(prompt)

	var result archScore
	err := s.llm.EvaluateJSON(ctx, EvalInput{
		Prompt: finalPrompt,
		Owner:  repo.Owner,
		Repo:   repo.Name,
		Chunks: []Chunk{{Content: finalPrompt}},
	}, &result)

	if err != nil {
		fmt.Printf("warn: arch evaluation failed for %s/%s: %v\n", repo.Owner, repo.Name, err)
	}
	return result
}

func detectRepoType(paths []string) string {
	hasAppsDir := false
	hasPackagesDir := false
	goModCount := 0

	for _, path := range paths {
		p := strings.ToLower(path)
		if strings.HasPrefix(p, "apps/") {
			hasAppsDir = true
		}

		if strings.HasPrefix(p, "packages/") || strings.HasPrefix(p, "libs/") {
			hasPackagesDir = true
		}

		if filepath.Base(p) == "go.mod" {
			goModCount++
		}

		if filepath.Base(p) == "lerna.json" || filepath.Base(p) == "turbo.json" || filepath.Base(p) == "nx.json" {
			return "monorepo"
		}
	}

	if hasAppsDir && hasPackagesDir {
		return "monorepo"
	}
	if goModCount > 2 { // Allow for a root and one sub-module without calling it a monorepo
		return "monorepo"
	}

	return "standard"
}

func pickPaths(entries []string, cfg *Config) []string {
	type scoredPath struct {
		Path  string
		Score int
	}

	var scoredPaths []scoredPath
	for _, p := range entries {
		score := scorePath(p)
		if score > 0 {
			scoredPaths = append(scoredPaths, scoredPath{Path: p, Score: score})
		}
	}

	slices.SortFunc(scoredPaths, func(a, b scoredPath) int {
		return b.Score - a.Score // Sort descending
	})

	var out []string
	for _, sp := range scoredPaths {
		out = append(out, sp.Path)
		if len(out) >= cfg.App.ChunksPerRepo {
			break
		}
	}
	return out
}

func scorePath(p string) int {
	l := strings.ToLower(p)

	// Penalize vendor/build/dist directories, etc.
	if strings.Contains(l, "/vendor/") || strings.Contains(l, "/node_modules/") || strings.Contains(l, "/.git/") || strings.Contains(l, "/build/") || strings.Contains(l, "/dist/") {
		return 0
	}

	// Penalize generated files or docs
	if strings.HasPrefix(l, "gen/") || strings.HasPrefix(l, "docs/") || strings.Contains(l, "example") {
		return 1
	}

	// Penalize binary/asset files
	if strings.HasSuffix(l, ".png") || strings.HasSuffix(l, ".jpg") || strings.HasSuffix(l, ".zip") || strings.HasSuffix(l, ".pdf") || strings.HasSuffix(l, ".svg") {
		return 0
	}

	score := 10 // Base score for a file

	// Score by directory
	if strings.HasPrefix(l, "internal/") || strings.HasPrefix(l, "pkg/") || strings.HasPrefix(l, "src/") || strings.HasPrefix(l, "lib/") {
		score += 20
	}

	if strings.HasPrefix(l, "cmd/") {
		score += 15
	}

	// Score by file type (extension)
	ext := filepath.Ext(l)
	switch ext {
	// High-value source code
	case ".go", ".py", ".ts", ".js", ".java", ".rs", ".swift", ".kt", ".kts", ".rb", ".ex", ".exs", ".cs", ".cpp", ".c", ".h", ".hpp":
		score += 50
	// Medium-value files
	case ".sh", ".sql":
		score += 20
	// Config/infra files
	case ".yml", ".yaml", ".json", ".toml", ".hcl", "dockerfile", ".tf":
		score += 5
	// Low-value files
	case ".md", ".txt", ".html", ".css":
		score += 2
	}

	// Penalize test files slightly so they don't dominate, but still get included
	if strings.HasSuffix(l, "_test.go") || strings.Contains(l, ".test.") || strings.Contains(l, ".spec.") {
		score -= 5
	}

	// Penalize lock files
	if strings.HasSuffix(l, ".lock") || strings.HasSuffix(l, "go.sum") {
		return 1
	}

	return score
}

func (s *service) sampleChunks(ctx context.Context, repo RepoTarget, files []string, sha string) ([]FileChunk, error) {
	var chunks []FileChunk
	for _, fp := range files {
		if len(chunks) >= s.cfg.App.ChunksPerRepo {
			break
		}

		data, err := s.gh.ReadFile(ctx, repo.Owner, repo.Name, repo.DefaultBranch, fp, sha)
		if err != nil || len(data) == 0 {
			continue
		}

		if len(data) > s.cfg.App.MaxChunkBytes {
			data = data[:s.cfg.App.MaxChunkBytes]
		}

		lines := 1 + strings.Count(string(data), "\n")
		chunks = append(chunks, FileChunk{
			Path:      fp,
			StartLine: 1,
			EndLine:   lines,
			Content:   string(data),
			Language:  guessLang(fp),
		})
	}
	return chunks, nil
}


func toLLMChunks(in []FileChunk) []Chunk {
	out := make([]Chunk, len(in))
	for i, c := range in {
		out[i] = Chunk{
			Path: c.Path, StartLine: c.StartLine, EndLine: c.EndLine, Content: c.Content, Language: c.Language,
		}
	}

	return out
}

func guessLang(p string) string {
	l := strings.ToLower(p)
	switch {
	case strings.HasSuffix(l, ".go"):
		return "Go"
	case strings.HasSuffix(l, ".ts"):
		return "TypeScript"
	case strings.HasSuffix(l, ".js"):
		return "JavaScript"
	case strings.HasSuffix(l, ".py"):
		return "Python"
	case strings.HasSuffix(l, ".rb"):
		return "Ruby"
	case strings.HasSuffix(l, ".java"):
		return "Java"
	case strings.HasSuffix(l, ".dart"):
		return "Dart"
	case strings.HasSuffix(l, ".clj"):
		return "Clojure"
	case strings.HasSuffix(l, ".cljs"):
		return "ClojureScript"
	case strings.HasSuffix(l, ".rkt"):
		return "Racket"
	case strings.HasSuffix(l, ".gleam"):
		return "Gleam"
	case strings.HasSuffix(l, ".ex"), strings.HasSuffix(l, ".exs"):
		return "Elixir"
	case strings.HasSuffix(l, ".md"), strings.HasSuffix(l, ".markdown"):
		return "Markdown"
	case strings.HasSuffix(l, ".cpp"), strings.HasSuffix(l, ".hpp"), strings.HasSuffix(l, ".cc"), strings.HasSuffix(l, ".h"):
		return "C++"
	case strings.HasSuffix(l, ".cs"):
		return "C#"
	case strings.HasSuffix(l, ".swift"):
		return "Swift"
	case strings.HasSuffix(l, ".kt"), strings.HasSuffix(l, ".kts"):
		return "Kotlin"
	case strings.HasSuffix(l, ".rs"):
		return "Rust"
	case strings.HasSuffix(l, ".php"):
		return "PHP"
	case strings.HasSuffix(l, ".html"), strings.HasSuffix(l, ".htm"):
		return "HTML"
	case strings.HasSuffix(l, ".css"):
		return "CSS"
	case strings.HasSuffix(l, ".sh"):
		return "Shell"
	case strings.HasSuffix(l, ".yml"), strings.HasSuffix(l, ".yaml"):
		return "YAML"
	case strings.HasSuffix(l, ".json"):
		return "JSON"
	case strings.HasSuffix(l, ".toml"):
		return "TOML"
	default:
		return "Unknown"
	}
}

func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}

	return ""
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}
