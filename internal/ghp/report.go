package ghp

import (
	"fmt"
	"html"
	"sort"
	"strings"
)

func renderHTML(user string, results []RepoResult, headlineHTML, summaryHTML string) string {
	// Language stats
	langCounts := make(map[string]int)
	for _, r := range results {
		if r.Repo.Language != "" {
			langCounts[r.Repo.Language]++
		}
	}
	type langStat struct {
		Name  string
		Count int
	}
	var sortedLangs []langStat
	for name, count := range langCounts {
		sortedLangs = append(sortedLangs, langStat{name, count})
	}
	sort.Slice(sortedLangs, func(i, j int) bool {
		return sortedLangs[i].Count > sortedLangs[j].Count
	})

	var langTags strings.Builder
	for i, ls := range sortedLangs {
		if i >= 5 { // Top 5 languages
			break
		}
		langTags.WriteString(fmt.Sprintf(`<span class="inline-block bg-sky-100 text-sky-800 text-xs font-semibold mr-2 px-2.5 py-0.5 rounded-full">%s</span>`, html.EscapeString(ls.Name)))
	}

	var langSection string
	if langTags.Len() > 0 {
		langSection = fmt.Sprintf(`<section class="mt-8">
    <h2 class="text-lg font-semibold mb-2">Main Languages</h2>
    %s
  </section>`, langTags.String())
	}

	var codeRows, archRows strings.Builder
	for _, r := range results {
		// Code Analysis Row
		ss := "—"
		if len(r.Strengths) > 0 {
			ss = "<ul>"
			for _, s := range r.Strengths {
				ss += "<li>" + html.EscapeString(s) + "</li>"
			}
			ss += "</ul>"
		}
		rs := "—"
		if len(r.Risks) > 0 {
			rs = "<ul>"
			for _, r := range r.Risks {
				rs += "<li>" + html.EscapeString(r) + "</li>"
			}
			rs += "</ul>"
		}
		var sm []string
		for _, s := range r.Samples {
			sm = append(sm, fmt.Sprintf(`<a class="underline" href="%s" target="_blank" rel="noreferrer">sample</a>`, s.URL))
		}
		samples := "—"
		if len(sm) > 0 {
			samples = strings.Join(sm, " ")
		}
		codeRows.WriteString(fmt.Sprintf(
			`<tr class="border-b">
<td class="py-2 px-3 font-medium align-top">%s/%s</td>
<td class="py-2 px-3 text-right align-top">%d</td>
<td class="py-2 px-3">%s</td>
<td class="py-2 px-3">%s</td>
<td class="py-2 px-3 align-top">%s</td>
</tr>`,
			html.EscapeString(r.Repo.Owner), html.EscapeString(r.Repo.Name), r.Score, ss, rs, samples,
		))

		// Architecture Analysis Row
		archSs := "—"
		if len(r.ArchStrengths) > 0 {
			archSs = "<ul>"
			for _, s := range r.ArchStrengths {
				archSs += "<li>" + html.EscapeString(s.Point) + "</li>"
			}
			archSs += "</ul>"
		}
		archCs := "—"
		if len(r.ArchConsiderations) > 0 {
			archCs = "<ul>"
			for _, c := range r.ArchConsiderations {
				archCs += fmt.Sprintf("<li>%s <span class='text-xs text-slate-500'>(%s)</span></li>", html.EscapeString(c.Point), html.EscapeString(c.Severity))
			}
			archCs += "</ul>"
		}
		archRows.WriteString(fmt.Sprintf(
			`<tr class="border-b">
<td class="py-2 px-3 font-medium align-top">%s/%s</td>
<td class="py-2 px-3">%s</td>
<td class="py-2 px-3">%s</td>
</tr>`,
			html.EscapeString(r.Repo.Owner), html.EscapeString(r.Repo.Name), archSs, archCs,
		))
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>GitHub Profiller – @%s</title>
<script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-slate-50 text-slate-900">
<main class="max-w-5xl mx-auto p-6">
<header class="mb-6">
  <h1 class="text-2xl font-bold">GitHub Profiller for <a href="https://github.com/%s" class="text-blue-600 hover:underline" target="_blank" rel="noreferrer">@%s</a></h1>
  <p class="text-sm text-slate-600">Generated locally. Scores are LLM-assisted and based on sampled files.</p>
</header>

%s

<section class="mt-8">
  <h2 class="text-xl font-semibold mb-4">Code Analysis</h2>
  <div class="bg-white shadow rounded-xl overflow-hidden">
    <table class="w-full text-sm">
      <thead class="bg-slate-100">
        <tr>
          <th class="text-left py-2 px-3 w-1/4">Repo</th>
          <th class="text-right py-2 px-3">Score</th>
          <th class="text-left py-2 px-3">Strengths</th>
          <th class="text-left py-2 px-3">Risks</th>
          <th class="text-left py-2 px-3">Samples</th>
        </tr>
      </thead>
      <tbody>
        %s
      </tbody>
    </table>
  </div>
</section>

<section class="mt-8">
  <h2 class="text-xl font-semibold mb-4">Architecture Analysis</h2>
  <div class="bg-white shadow rounded-xl overflow-hidden">
    <table class="w-full text-sm">
      <thead class="bg-slate-100">
        <tr>
          <th class="text-left py-2 px-3 w-1/4">Repo</th>
          <th class="text-left py-2 px-3">Strengths</th>
          <th class="text-left py-2 px-3">Considerations</th>
        </tr>
      </thead>
      <tbody>
        %s
      </tbody>
    </table>
  </div>
</section>

%s
%s

</main>
</body>
</html>`, html.EscapeString(user), html.EscapeString(user), html.EscapeString(user), headlineHTML, codeRows.String(), archRows.String(), summaryHTML, langSection)
}
