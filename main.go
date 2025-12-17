package main

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

//go:embed web
var webFS embed.FS

type Config struct {
	APIKeys      string        `json:"api_keys"`
	MimoAccounts []MimoAccount `json:"mimo_accounts"`
}

type MimoAccount struct {
	ServiceToken    string `json:"service_token"`
	UserId          string `json:"user_id"`
	XiaomichatbotPh string `json:"xiaomichatbot_ph"`
}

var (
	config     Config
	configLock sync.RWMutex
	accountIdx int
)

func loadConfig() {
	data, err := os.ReadFile("config.json")
	if err != nil {
		config = Config{APIKeys: "sk-default"}
		saveConfig()
		return
	}
	json.Unmarshal(data, &config)
}

func saveConfig() error {
	configLock.Lock()
	defer configLock.Unlock()
	data, _ := json.MarshalIndent(config, "", "  ")
	return os.WriteFile("config.json", data, 0644)
}

func getNextAccount() *MimoAccount {
	configLock.RLock()
	defer configLock.RUnlock()
	if len(config.MimoAccounts) == 0 {
		return nil
	}
	acc := &config.MimoAccounts[accountIdx%len(config.MimoAccounts)]
	accountIdx++
	return acc
}

func validateAPIKey(key string) bool {
	configLock.RLock()
	defer configLock.RUnlock()
	for _, k := range strings.Split(config.APIKeys, ",") {
		if strings.TrimSpace(k) == key {
			return true
		}
	}
	return false
}

// OpenAI API 结构
type OpenAIRequest struct {
	Model           string          `json:"model"`
	Messages        []OpenAIMessage `json:"messages"`
	Stream          bool            `json:"stream"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Temperature     float64         `json:"temperature,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"` // low/medium/high 深度思考
}

type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage,omitempty"`
}

type OpenAIChoice struct {
	Index        int            `json:"index"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	Delta        *OpenAIDelta   `json:"delta,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
}

type OpenAIDelta struct {
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	Reasoning string `json:"reasoning,omitempty"` // 深度思考内容
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type MimoRequest struct {
	MsgId          string      `json:"msgId"`
	ConversationId string      `json:"conversationId"`
	Query          string      `json:"query"`
	ModelConfig    ModelConfig `json:"modelConfig"`
	MultiMedias    []any       `json:"multiMedias"`
}

type ModelConfig struct {
	EnableThinking  bool    `json:"enableThinking"`
	Temperature     float64 `json:"temperature"`
	TopP            float64 `json:"topP"`
	WebSearchStatus string  `json:"webSearchStatus"`
	Model           string  `json:"model"`
}

type MimoSSEData struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type MimoUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
}

func callMimoAPI(acc *MimoAccount, query string, thinking bool) (string, string, *MimoUsage, error) {
	mimoReq := MimoRequest{
		MsgId:          uuid.New().String()[:32],
		ConversationId: uuid.New().String()[:32],
		Query:          query,
		ModelConfig:    ModelConfig{EnableThinking: thinking, Temperature: 0.8, TopP: 0.95, WebSearchStatus: "disabled", Model: "mimo-v2-flash-studio"},
		MultiMedias:    []any{},
	}
	body, _ := json.Marshal(mimoReq)
	apiURL := fmt.Sprintf("https://aistudio.xiaomimimo.com/open-apis/bot/chat?xiaomichatbot_ph=%s", url.QueryEscape(acc.XiaomichatbotPh))

	req, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://aistudio.xiaomimimo.com")
	req.Header.Set("Referer", "https://aistudio.xiaomimimo.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	req.Header.Set("x-timezone", "Asia/Shanghai")
	req.AddCookie(&http.Cookie{Name: "serviceToken", Value: acc.ServiceToken})
	req.AddCookie(&http.Cookie{Name: "userId", Value: acc.UserId})
	req.AddCookie(&http.Cookie{Name: "xiaomichatbot_ph", Value: acc.XiaomichatbotPh})

	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return "", "", nil, err
	}
	defer resp.Body.Close()

	var result strings.Builder
	var usage MimoUsage
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			var sseData MimoSSEData
			if json.Unmarshal([]byte(data), &sseData) == nil && sseData.Type == "text" {
				result.WriteString(sseData.Content)
			}
			var u MimoUsage
			if json.Unmarshal([]byte(data), &u) == nil && u.PromptTokens > 0 {
				usage = u
			}
		}
	}
	// 解析 <think>...</think>
	full := strings.ReplaceAll(result.String(), "\x00", "")
	if start := strings.Index(full, "<think>"); start != -1 {
		if end := strings.Index(full, "</think>"); end != -1 {
			return full[end+8:], full[start+7 : end], &usage, nil
		}
	}
	return full, "", &usage, nil
}

func streamMimoAPI(acc *MimoAccount, query string, thinking bool, w http.ResponseWriter) error {
	mimoReq := MimoRequest{
		MsgId:          uuid.New().String()[:32],
		ConversationId: uuid.New().String()[:32],
		Query:          query,
		ModelConfig:    ModelConfig{EnableThinking: thinking, Temperature: 0.8, TopP: 0.95, WebSearchStatus: "disabled", Model: "mimo-v2-flash-studio"},
		MultiMedias:    []any{},
	}
	body, _ := json.Marshal(mimoReq)
	apiURL := fmt.Sprintf("https://aistudio.xiaomimimo.com/open-apis/bot/chat?xiaomichatbot_ph=%s", url.QueryEscape(acc.XiaomichatbotPh))

	req, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://aistudio.xiaomimimo.com")
	req.Header.Set("Referer", "https://aistudio.xiaomimimo.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	req.Header.Set("x-timezone", "Asia/Shanghai")
	req.AddCookie(&http.Cookie{Name: "serviceToken", Value: acc.ServiceToken})
	req.AddCookie(&http.Cookie{Name: "userId", Value: acc.UserId})
	req.AddCookie(&http.Cookie{Name: "xiaomichatbot_ph", Value: acc.XiaomichatbotPh})

	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	flusher, _ := w.(http.Flusher)
	msgID := "chatcmpl-" + uuid.New().String()[:24]

	// 发送初始 role delta
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
		ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
		Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{Role: "assistant"}}},
	}))
	flusher.Flush()

	scanner := bufio.NewScanner(resp.Body)
	var buf strings.Builder
	inThink := false

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		var sseData MimoSSEData
		if json.Unmarshal([]byte(data), &sseData) != nil || sseData.Type != "text" || sseData.Content == "" {
			continue
		}

		buf.WriteString(sseData.Content)
		text := strings.ReplaceAll(buf.String(), "\x00", "")

		for {
			if !inThink {
				if idx := strings.Index(text, "<think>"); idx != -1 {
					if idx > 0 {
						fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
							ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
							Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{Content: text[:idx]}}},
						}))
						flusher.Flush()
					}
					inThink = true
					text = text[idx+7:]
					continue
				}
				safe := len(text) - 7
				if safe > 0 {
					safe = safeUTF8Len(text, safe)
					fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
						ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
						Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{Content: text[:safe]}}},
					}))
					flusher.Flush()
					text = text[safe:]
				}
				break
			} else {
				if idx := strings.Index(text, "</think>"); idx != -1 {
					if idx > 0 {
						fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
							ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
							Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{Reasoning: text[:idx]}}},
						}))
						flusher.Flush()
					}
					inThink = false
					text = text[idx+8:]
					continue
				}
				safe := len(text) - 8
				if safe > 0 {
					safe = safeUTF8Len(text, safe)
					fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
						ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
						Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{Reasoning: text[:safe]}}},
					}))
					flusher.Flush()
					text = text[safe:]
				}
				break
			}
		}
		buf.Reset()
		buf.WriteString(text)
	}

	// flush remaining
	remaining := buf.String()
	if remaining != "" {
		if inThink {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
				ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
				Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{Reasoning: remaining}}},
			}))
		} else {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
				ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
				Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{Content: remaining}}},
			}))
		}
		flusher.Flush()
	}

	// 发送结束
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(OpenAIResponse{
		ID: msgID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
		Choices: []OpenAIChoice{{Index: 0, Delta: &OpenAIDelta{}, FinishReason: "stop"}},
	}))
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	return nil
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func safeUTF8Len(s string, maxLen int) int {
	if maxLen <= 0 || maxLen >= len(s) {
		return len(s)
	}
	for maxLen > 0 && s[maxLen]&0xC0 == 0x80 {
		maxLen--
	}
	return maxLen
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !validateAPIKey(auth) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
		return
	}

	var req OpenAIRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
		return
	}

	acc := getNextAccount()
	if acc == nil {
		http.Error(w, `{"error":{"message":"no mimo account"}}`, http.StatusServiceUnavailable)
		return
	}

	var query strings.Builder
	msgs := req.Messages
	if len(msgs) > 10 {
		msgs = msgs[len(msgs)-10:]
	}
	for _, msg := range msgs {
		content := msg.Content
		if len(content) > 4000 {
			content = content[:4000] + "..."
		}
		query.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, content))
	}

	// reasoning_effort 不为空则启用深度思考
	thinking := req.ReasoningEffort != ""

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		streamMimoAPI(acc, query.String(), thinking, w)
	} else {
		content, thinkContent, usage, err := callMimoAPI(acc, query.String(), thinking)
		if err != nil {
			http.Error(w, `{"error":{"message":"`+err.Error()+`"}}`, http.StatusInternalServerError)
			return
		}
		// 如果有思考内容，拼接到回复前面
		fullContent := content
		if thinkContent != "" {
			fullContent = "<think>" + thinkContent + "</think>\n" + content
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(OpenAIResponse{
			ID: "chatcmpl-" + uuid.New().String()[:24], Object: "chat.completion", Created: time.Now().Unix(), Model: "mimo-v2-flash-studio",
			Choices: []OpenAIChoice{{Index: 0, Message: &OpenAIMessage{Role: "assistant", Content: fullContent}, FinishReason: "stop"}},
			Usage:   OpenAIUsage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.PromptTokens + usage.CompletionTokens},
		})
	}
}

func handleAdmin() http.Handler {
	sub, _ := fs.Sub(webFS, "web")
	return http.FileServer(http.FS(sub))
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		configLock.RLock()
		json.NewEncoder(w).Encode(config)
		configLock.RUnlock()
		return
	}
	var newConfig Config
	if json.NewDecoder(r.Body).Decode(&newConfig) != nil {
		http.Error(w, `{"error":"invalid"}`, http.StatusBadRequest)
		return
	}
	configLock.Lock()
	config = newConfig
	configLock.Unlock()
	saveConfig()
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleParseCurl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Curl string `json:"curl"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if acc := parseCurl(body.Curl); acc != nil {
		json.NewEncoder(w).Encode(acc)
	} else {
		http.Error(w, `{"error":"parse failed"}`, http.StatusBadRequest)
	}
}

func handleTestAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var acc MimoAccount
	json.NewDecoder(r.Body).Decode(&acc)
	content, _, _, err := callMimoAPI(&acc, "hi", false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
	} else {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "response": content})
	}
}

func parseCurl(curl string) *MimoAccount {
	acc := &MimoAccount{}
	if m := regexp.MustCompile(`-b\s+'([^']+)'`).FindStringSubmatch(curl); len(m) > 1 {
		cookies := m[1]
		if sm := regexp.MustCompile(`serviceToken="([^"]+)"`).FindStringSubmatch(cookies); len(sm) > 1 {
			acc.ServiceToken = sm[1]
		}
		if um := regexp.MustCompile(`userId=(\d+)`).FindStringSubmatch(cookies); len(um) > 1 {
			acc.UserId = um[1]
		}
		if pm := regexp.MustCompile(`xiaomichatbot_ph="([^"]+)"`).FindStringSubmatch(cookies); len(pm) > 1 {
			acc.XiaomichatbotPh = pm[1]
		}
	}
	if acc.ServiceToken == "" {
		return nil
	}
	return acc
}

func main() {
	loadConfig()
	http.HandleFunc("/v1/chat/completions", handleChatCompletions)
	http.HandleFunc("/api/config", handleConfig)
	http.HandleFunc("/api/parse-curl", handleParseCurl)
	http.HandleFunc("/api/test-account", handleTestAccount)
	http.Handle("/", handleAdmin())
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Server on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
