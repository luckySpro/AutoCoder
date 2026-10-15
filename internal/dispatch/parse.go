package dispatch

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/luckySpro/AutoCoder/internal/project"
)

// parser 把工具的输出行转成"原始输出"与"动作"，并累计 token 用量。
// 支持 Claude Code / Cursor 的 stream-json、Codex 的 --json 事件流、Gemini/Qwen 的 json 结果；
// 其他输出原样显示。各工具字段会随版本变化，识别不了的 JSON 行按原样显示，不会丢。
type parser struct {
	final     string // 工具最后的答复
	usage     project.Tokens
	exact     bool // 从输出里拿到了精确用量
	finalSeen bool // 拿到了"结果"事件里的总用量，之后不再累加
	seenMsg   map[string]bool
	lastAgent string
}

type line struct {
	text string
	tone string
}

type act struct {
	kind, text, meta, tone string
}

func newParser() *parser { return &parser{seenMsg: map[string]bool{}} }

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)

// feed 处理一行标准输出。
func (p *parser) feed(s string) ([]line, []act) {
	s = ansiRe.ReplaceAllString(strings.TrimRight(s, "\r"), "")
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			if ls, as, ok := p.event(m); ok {
				return ls, as
			}
			return []line{{text: clip(t, 400), tone: "dim"}}, nil
		}
	}
	tone := ""
	if strings.HasPrefix(t, "$ ") {
		tone = "cmd"
	}
	return []line{{text: s, tone: tone}}, nil
}

func (p *parser) event(m map[string]any) ([]line, []act, bool) {
	typ := str(m["type"])
	switch {
	// ---- Claude Code / Cursor stream-json ----
	case typ == "system":
		model := str(m["model"])
		if model == "" {
			return nil, nil, true
		}
		return []line{{text: "会话开始 · " + model, tone: "dim"}}, nil, true
	case typ == "assistant" || (typ == "user" && m["message"] != nil):
		return p.claudeMessage(typ, m)
	case typ == "result" && (m["result"] != nil || m["usage"] != nil):
		if r := str(m["result"]); r != "" {
			p.final = r
		}
		if u, ok := findUsage(m["usage"]); ok {
			p.usage, p.exact, p.finalSeen = u, true, true
		}
		if b, _ := m["is_error"].(bool); b {
			return []line{{text: "错误：" + clip(p.final, 400), tone: "bad"}}, []act{{kind: "warn", text: clip(p.final, 200), tone: "warn"}}, true
		}
		return []line{{text: "完成", tone: "ok"}}, nil, true

	// ---- Codex exec --json（新版 item.* 事件）----
	case strings.HasPrefix(typ, "item."):
		item, _ := m["item"].(map[string]any)
		return p.codexItem(typ, item)
	case typ == "turn.completed":
		if u, ok := findUsage(m["usage"]); ok {
			p.usage = addTokens(p.usage, u)
			p.exact = true
		}
		return nil, nil, true
	case typ == "turn.failed" || typ == "error":
		msg := str(m["message"])
		if e, ok := m["error"].(map[string]any); ok {
			msg = str(e["message"])
		}
		return []line{{text: "错误：" + msg, tone: "bad"}}, []act{{kind: "warn", text: clip(msg, 200), tone: "warn"}}, true
	case typ == "thread.started" || typ == "turn.started":
		return nil, nil, true
	}

	// ---- Codex 旧版：{"msg": {...}} ----
	if msg, ok := m["msg"].(map[string]any); ok {
		return p.codexLegacy(msg)
	}
	// ---- Gemini / Qwen --output-format json ----
	if r, ok := m["response"].(string); ok {
		p.final = r
		if u, ok := geminiUsage(m["stats"]); ok {
			p.usage, p.exact, p.finalSeen = u, true, true
		}
		return textLines(r, ""), []act{{kind: "say", text: clip(r, 400)}}, true
	}
	// 其他带用量的结果
	if u, ok := findUsage(m["usage"]); ok && !p.finalSeen {
		p.usage, p.exact = u, true
		return []line{{text: "用量已记录", tone: "dim"}}, nil, true
	}
	return nil, nil, false
}

func (p *parser) claudeMessage(typ string, m map[string]any) ([]line, []act, bool) {
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return nil, nil, true
	}
	var ls []line
	var as []act
	if typ == "assistant" && !p.finalSeen {
		if id := str(msg["id"]); id != "" && !p.seenMsg[id] {
			if u, ok := findUsage(msg["usage"]); ok {
				p.seenMsg[id] = true
				p.usage = addTokens(p.usage, u)
				p.exact = true
			}
		}
	}
	content, _ := msg["content"].([]any)
	for _, c := range content {
		b, _ := c.(map[string]any)
		switch str(b["type"]) {
		case "text":
			txt := strings.TrimSpace(str(b["text"]))
			if txt == "" {
				continue
			}
			p.lastAgent = txt
			ls = append(ls, textLines(txt, "")...)
			as = append(as, act{kind: "say", text: clip(txt, 400)})
		case "tool_use":
			l, a := toolUse(str(b["name"]), b["input"])
			ls = append(ls, l)
			as = append(as, a)
		case "tool_result":
			txt := resultText(b["content"])
			if isErr, _ := b["is_error"].(bool); isErr {
				ls = append(ls, line{text: "  " + clip(firstLines(txt, 3), 400), tone: "bad"})
			} else if txt != "" {
				ls = append(ls, line{text: "  " + clip(firstLines(txt, 1), 200), tone: "dim"})
			}
		}
	}
	return ls, as, true
}

// toolUse 把 Claude 风格的工具调用转成动作。
func toolUse(name string, input any) (line, act) {
	in, _ := input.(map[string]any)
	path := str(in["file_path"])
	if path == "" {
		path = str(in["path"])
	}
	switch name {
	case "Read", "read_file", "readFile", "NotebookRead":
		return line{text: "▸ read " + path, tone: "dim"}, act{kind: "read", text: path}
	case "Glob", "Grep", "LS", "glob", "grep", "search_file_content":
		q := str(in["pattern"])
		return line{text: "▸ " + strings.ToLower(name) + " " + q, tone: "dim"}, act{kind: "read", text: strings.TrimSpace(name + " " + q)}
	case "Edit", "MultiEdit", "Write", "NotebookEdit", "write_file", "edit", "replace", "editFile", "writeFile":
		return line{text: "✎ " + strings.ToLower(name) + " " + path, tone: "cmd"}, act{kind: "edit", text: path}
	case "Bash", "shell", "run_shell_command", "bash":
		cmd := str(in["command"])
		return line{text: "$ " + cmd, tone: "cmd"}, act{kind: "run", text: clip(cmd, 200)}
	}
	return line{text: "▸ " + name, tone: "dim"}, act{kind: "run", text: name}
}

func (p *parser) codexItem(typ string, item map[string]any) ([]line, []act, bool) {
	if item == nil {
		return nil, nil, true
	}
	switch str(item["type"]) {
	case "agent_message":
		if typ != "item.completed" {
			return nil, nil, true
		}
		txt := strings.TrimSpace(str(item["text"]))
		p.final, p.lastAgent = txt, txt
		return textLines(txt, ""), []act{{kind: "say", text: clip(txt, 400)}}, true
	case "reasoning":
		if typ != "item.completed" {
			return nil, nil, true
		}
		return []line{{text: clip(str(item["text"]), 300), tone: "dim"}}, nil, true
	case "command_execution":
		cmd := str(item["command"])
		if typ == "item.started" {
			return []line{{text: "$ " + cmd, tone: "cmd"}}, nil, true
		}
		code, hasCode := item["exit_code"].(float64)
		out := lastLines(str(item["aggregated_output"]), 12)
		tone, meta := "ok", "通过"
		if hasCode && code != 0 {
			tone, meta = "bad", fmt.Sprintf("退出码 %d", int(code))
		}
		ls := textLines(out, "")
		if tone == "bad" {
			for i := range ls {
				ls[i].tone = "bad"
			}
		}
		return ls, []act{{kind: "run", text: clip(cmd, 200), meta: meta, tone: tone}}, true
	case "file_change":
		if typ != "item.completed" {
			return nil, nil, true
		}
		changes, _ := item["changes"].([]any)
		var ls []line
		var as []act
		for _, c := range changes {
			cm, _ := c.(map[string]any)
			path, kind := str(cm["path"]), str(cm["kind"])
			ls = append(ls, line{text: "✎ " + kind + " " + path, tone: "cmd"})
			as = append(as, act{kind: "edit", text: path, meta: kind})
		}
		return ls, as, true
	case "mcp_tool_call", "web_search":
		return []line{{text: "▸ " + str(item["type"]), tone: "dim"}}, nil, true
	case "error":
		msg := str(item["message"])
		return []line{{text: "错误：" + msg, tone: "bad"}}, []act{{kind: "warn", text: clip(msg, 200), tone: "warn"}}, true
	}
	return nil, nil, true
}

func (p *parser) codexLegacy(msg map[string]any) ([]line, []act, bool) {
	switch str(msg["type"]) {
	case "agent_message":
		txt := strings.TrimSpace(str(msg["message"]))
		p.final, p.lastAgent = txt, txt
		return textLines(txt, ""), []act{{kind: "say", text: clip(txt, 400)}}, true
	case "exec_command_begin":
		cmd := joinAny(msg["command"])
		return []line{{text: "$ " + cmd, tone: "cmd"}}, []act{{kind: "run", text: clip(cmd, 200)}}, true
	case "exec_command_end":
		code, _ := msg["exit_code"].(float64)
		tone := "ok"
		if code != 0 {
			tone = "bad"
		}
		return []line{{text: fmt.Sprintf("  退出码 %d", int(code)), tone: tone}}, nil, true
	case "patch_apply_begin":
		changes, _ := msg["changes"].(map[string]any)
		var ls []line
		var as []act
		for path := range changes {
			ls = append(ls, line{text: "✎ " + path, tone: "cmd"})
			as = append(as, act{kind: "edit", text: path})
		}
		return ls, as, true
	case "token_count":
		info, _ := msg["info"].(map[string]any)
		if u, ok := findUsage(info["total_token_usage"]); ok {
			p.usage, p.exact = u, true
		}
		return nil, nil, true
	case "error":
		m := str(msg["message"])
		return []line{{text: "错误：" + m, tone: "bad"}}, []act{{kind: "warn", text: clip(m, 200), tone: "warn"}}, true
	}
	return nil, nil, true
}

// findUsage 识别常见的用量字段命名。OpenAI 风格的 cached 是 input 的一部分，单独拆出来。
func findUsage(v any) (project.Tokens, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return project.Tokens{}, false
	}
	n := func(keys ...string) int64 {
		for _, k := range keys {
			if f, ok := m[k].(float64); ok {
				return int64(f)
			}
		}
		return 0
	}
	in := n("input_tokens", "inputTokens", "prompt_tokens", "input")
	out := n("output_tokens", "outputTokens", "completion_tokens", "output")
	cr := n("cache_read_input_tokens", "cacheReadInputTokens", "cache_read", "cacheRead")
	cw := n("cache_creation_input_tokens", "cacheCreationInputTokens", "cache_write", "cacheWrite")
	if cached := n("cached_input_tokens", "cachedInputTokens"); cached > 0 && cr == 0 {
		cr = cached
		in = max(0, in-cached)
	}
	out += n("reasoning_output_tokens")
	t := project.Tokens{Input: in, Output: out, CacheRead: cr, CacheWrite: cw}
	return t, t.Total() > 0
}

func geminiUsage(v any) (project.Tokens, bool) {
	stats, _ := v.(map[string]any)
	models, _ := stats["models"].(map[string]any)
	var t project.Tokens
	for _, mv := range models {
		mm, _ := mv.(map[string]any)
		tk, _ := mm["tokens"].(map[string]any)
		f := func(k string) int64 { x, _ := tk[k].(float64); return int64(x) }
		cached := f("cached")
		t.Input += max(0, f("prompt")-cached)
		t.CacheRead += cached
		t.Output += f("candidates") + f("thoughts")
	}
	if t.Total() == 0 {
		return findUsage(stats)
	}
	return t, true
}

func addTokens(a, b project.Tokens) project.Tokens {
	return project.Tokens{Input: a.Input + b.Input, Output: a.Output + b.Output, CacheRead: a.CacheRead + b.CacheRead, CacheWrite: a.CacheWrite + b.CacheWrite}
}

// estimate 按字符数估算用量（约 3 个字符一个 token，中文偏多）。
func estimate(prompt string, output string) project.Tokens {
	return project.Tokens{Input: int64(len([]rune(prompt))/3 + 1), Output: int64(len([]rune(output))/3 + 1)}
}

// ---------- 脱敏 ----------

var secretRes = []*regexp.Regexp{
	regexp.MustCompile(`sk-(?:ant-|proj-)?[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|password)["']?\s*[:=]\s*["']?)[A-Za-z0-9_\-/+=.]{12,}`),
}

// mask 把常见格式的密钥替换为 ***。
func mask(s string) string {
	for i, re := range secretRes {
		if i == len(secretRes)-1 {
			s = re.ReplaceAllString(s, "${1}***")
			continue
		}
		s = re.ReplaceAllString(s, "***")
	}
	return s
}

// ---------- 小工具 ----------

func str(v any) string {
	s, _ := v.(string)
	return s
}

func joinAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, p := range x {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func resultText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, c := range x {
			if m, ok := c.(map[string]any); ok {
				b.WriteString(str(m["text"]))
			}
		}
		return b.String()
	}
	return ""
}

func textLines(s, tone string) []line {
	var out []line
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, line{text: l, tone: tone})
	}
	return out
}

func firstLines(s string, n int) string {
	ls := strings.SplitN(strings.TrimSpace(s), "\n", n+1)
	if len(ls) > n {
		ls = ls[:n]
	}
	return strings.Join(ls, " ⏎ ")
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
