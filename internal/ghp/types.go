package ghp

import "encoding/json"

type RepoTarget struct {
	Owner         string
	Name          string
	DefaultBranch string
	Stars         int
	Pinned        bool
	Language      string
}

type FileChunk struct {
	Path      string
	StartLine int
	EndLine   int
	Content   string
	Language  string
}

type ChunkScore struct {
	Readability int      `json:"readability"`
	Design      int      `json:"design"`
	Testing     int      `json:"testing"`
	Maintain    int      `json:"maintainability"`
	Idiomatic   int      `json:"idiomatic"`
	Security    int      `json:"security"`
	Notes       []string `json:"notes"`
	Citations   []struct {
		File   string `json:"file"`
		Lines  string `json:"lines"`
		Reason string `json:"reason"`
	} `json:"citations"`
}

type ArchStrength struct {
	Point         string `json:"point"`
	Justification string `json:"justification"`
}

// UnmarshalJSON handles both string and object formats from the LLM.
func (as *ArchStrength) UnmarshalJSON(data []byte) error {
	// NOTE: First, try to unmarshal as a simple string.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		as.Point = s
		return nil
	}

	// NOTE: If it's not a string we unmarshal it as a full object.
	// NOTE: The alias type to avoid recursion.
	type Alias ArchStrength
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*as = ArchStrength(a)
	return nil
}

type ArchConsideration struct {
	Point         string `json:"point"`
	Justification string `json:"justification"`
	Severity      string `json:"severity"`
}

func (ac *ArchConsideration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		ac.Point = s
		ac.Severity = "Low" // Default severity for simple format
		return nil
	}

	type Alias ArchConsideration
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*ac = ArchConsideration(a)
	return nil
}

type RepoResult struct {
	Repo               RepoTarget
	Score              int
	Strengths          []string
	Risks              []string
	ArchStrengths      []ArchStrength
	ArchConsiderations []ArchConsideration
	Samples            []struct{ URL, Note string }
	Files              int
	Chunks             int
}
