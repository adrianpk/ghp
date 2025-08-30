package ghp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type Chunk struct {
	Path      string
	StartLine int
	EndLine   int
	Content   string
	Language  string
}

type EvalInput struct {
	Prompt string
	Owner  string
	Repo   string
	Branch string
	Chunks []Chunk
}

type Client interface {
	EvaluateJSON(ctx context.Context, in EvalInput, out any) error
}

type openAIClient struct {
	cfg  *Config
	sdk  openai.Client
	rpm  int
	para int
}

func (c *openAIClient) EvaluateJSON(ctx context.Context, in EvalInput, out any) error {
	outVal := reflect.ValueOf(out)
	if outVal.Kind() != reflect.Ptr || outVal.Elem().Kind() != reflect.Slice {
		return errors.New("out must be a pointer to a slice")
	}

	sliceType := outVal.Elem().Type().Elem()
	outSlice := reflect.MakeSlice(reflect.SliceOf(sliceType), len(in.Chunks), len(in.Chunks))
	sem := make(chan struct{}, c.para)
	wg := sync.WaitGroup{}
	errMu := sync.Mutex{}
	var firstErr error

	minDelay := time.Second // nunca menos de 1 segundo
	delay := time.Minute / time.Duration(max(1, c.rpm))
	delay = maxDelay(delay, minDelay)

	for i := range in.Chunks {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			resPtr := reflect.New(sliceType)
			err := c.evalOne(ctx, in, in.Chunks[i], resPtr.Interface())
			if err == nil {
				outSlice.Index(i).Set(resPtr.Elem())
			} else {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}()
		time.Sleep(delay)
	}
	wg.Wait()

	outVal.Elem().Set(outSlice)

	return firstErr
}

func (c *openAIClient) evalOne(ctx context.Context, in EvalInput, ch Chunk, out any) error {
	sys := in.Prompt
	user := fmt.Sprintf(
		`[REPO] %s/%s@%s\n[FILE] %s (%s)\n[LINES] %d-%d\n\n[CODE]\n%s\n\n[REQUIREMENTS]\n- Output STRICT JSON: {readability, design, testing, maintainability, idiomatic, security, notes[], citations[]}\n- Base judgments ONLY on this snippet.\n- Cite concrete lines where relevant.`,
		in.Owner, in.Repo, in.Branch, ch.Path, ch.Language, ch.StartLine, ch.EndLine, ch.Content)

	req := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(sys),
			openai.UserMessage(user),
		},
		Model: c.cfg.LLM.Model,
		// MaxTokens:
		// Temperature:
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.sdk.Chat.Completions.New(ctx, req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(300*(attempt+1)) * time.Millisecond)
			continue
		}

		if len(resp.Choices) == 0 {
			return errors.New("empty completion")
		}

		txt := resp.Choices[0].Message.Content

		if err := json.Unmarshal([]byte(txt), out); err != nil {
			js, stripErr := extractJSON(txt)
			if stripErr != nil {
				return fmt.Errorf("json parse: %w", err)
			}

			if err := json.Unmarshal([]byte(js), out); err != nil {
				return fmt.Errorf("json parse(2): %w", err)
			}
		}
		return nil
	}

	return lastErr
}

// Gemini client skeleton

type geminiClient struct {
	apiKey string
}

func (g *geminiClient) EvaluateJSON(ctx context.Context, in EvalInput, out any) error {
	// Construir el prompt para Gemini
	prompt := in.Prompt
	if len(in.Chunks) > 0 {
		prompt += "\n" + in.Chunks[0].Content
	}

	// Construir el request para Gemini
	body := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]string{{"text": prompt}}},
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	// Endpoint Gemini Pro 1.5-002 (disponible para tu API key)
	url := "https://generativelanguage.googleapis.com/v1/models/gemini-1.5-pro-002:generateContent?key=" + g.apiKey

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("gemini api error: %s", string(b))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return err
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return errors.New("no candidates from gemini")
	}

	text := geminiResp.Candidates[0].Content.Parts[0].Text
	if err := json.Unmarshal([]byte(text), out); err != nil {
		js, stripErr := extractJSON(text)
		if stripErr != nil {
			return fmt.Errorf("gemini json parse: %w", err)
		}
		if err := json.Unmarshal([]byte(js), out); err != nil {
			outVal := reflect.ValueOf(out)
			if outVal.Kind() == reflect.Ptr && outVal.Elem().Kind() == reflect.Slice {
				elemType := outVal.Elem().Type().Elem()
				elemPtr := reflect.New(elemType)
				if err2 := json.Unmarshal([]byte(js), elemPtr.Interface()); err2 == nil {
					slice := reflect.MakeSlice(outVal.Elem().Type(), 1, 1)
					slice.Index(0).Set(elemPtr.Elem())
					outVal.Elem().Set(slice)
					return nil
				}
			}
			return fmt.Errorf("gemini json parse(2): %w", err)
		}
	}

	return nil
}

func NewLLMClient(cfg *Config) (Client, error) {
	switch cfg.LLM.Provider {
	case "", "openai":
		apiKey := cfg.LLM.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return nil, errors.New("missing OpenAI API key")
		}
		opts := []option.RequestOption{option.WithAPIKey(apiKey)}
		if cfg.LLM.Endpoint != "" {
			opts = append(opts, option.WithBaseURL(cfg.LLM.Endpoint))
		}
		return &openAIClient{
			cfg:  cfg,
			sdk:  openai.NewClient(opts...),
			rpm:  cfg.LLM.RequestsPerMinute,
			para: cfg.LLM.ParallelRequests,
		}, nil
	case "gemini":
		apiKey := cfg.LLM.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
		}
		if apiKey == "" {
			return nil, errors.New("missing Gemini API key")
		}
		return &geminiClient{apiKey: apiKey}, nil
	default:
		return nil, errors.New("unsupported LLM provider: " + cfg.LLM.Provider)
	}
}

func extractJSON(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start == -1 || end == -1 || end <= start {
		return "", errors.New("no json braces found")
	}

	return s[start : end+1], nil
}

func max(a, b int) int {
	if a > b {
		return a
	}

	return b
}

func maxDelay(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
