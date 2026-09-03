package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/watchdog-ai/watchdog/internal/model"
)

// maxContentBytes 送入 AI 分析的内容上限（过大文件只取前缀，控制 token 与耗时）。
const maxContentBytes = 200 * 1024

// aiTimeout 单次 AI 分析的超时时间。
const aiTimeout = 90 * time.Second

// defaultLint 内置硬语法检查：按扩展名调用系统命令或内置检查器。
//
// 这是“第一步：硬语法检查”的默认实现。二次开发可注入自定义 LintFunc。
// 说明：
//   - php  -> php -l file
//   - js   -> 若本机有 eslint，则 npx eslint；否则跳过（留接口）
//   - css  -> stylelint（预留）
//   - html -> 内置标签闭合检查（无需任何外部工具）
func defaultLint(p *model.Project, file string) []string {
	var issues []string
	switch strings.ToLower(filepath.Ext(file)) {
	case ".php":
		issues = runLintCmd("php", []string{"-l", file})
	case ".js":
		issues = runLintCmd("npx", []string{"eslint", file})
	case ".css":
		issues = runLintCmd("npx", []string{"stylelint", file})
	case ".html", ".htm":
		issues = checkHTMLTags(file)
	default:
		// vue/py 等：预留，可替换为对应 linter
	}
	return issues
}

// runLintCmd 执行外部 linter 并转成问题摘要；命令不存在时返回空，不阻塞检查。
func runLintCmd(name string, args []string) []string {
	bin, err := exec.LookPath(name)
	if err != nil {
		return nil // 工具未安装，跳过，交由后续 AI 阶段兜底
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err == nil && len(out) == 0 {
		return nil // 语法通过
	}
	// 将输出压缩为前 2 行作为摘要，避免前端弹窗过长
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var summary []string
	for i := 0; i < len(lines) && i < 2; i++ {
		if s := strings.TrimSpace(lines[i]); s != "" {
			summary = append(summary, s)
		}
	}
	return summary
}

// ---- HTML 轻量语法检查 ----

// voidElements 无闭合标签的 HTML 元素。
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// tagRe 提取开/闭标签。
var tagRe = regexp.MustCompile(`(?i)<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:"[^"]*"|'[^']*'|[^>"'])*)>`)

// checkHTMLTags 内置 HTML 标签闭合检查（栈匹配）。
// 跳过 <script>/<style> 内部内容，避免字符串中的尖括号造成误报。
func checkHTMLTags(file string) []string {
	content := readHead(file, maxContentBytes)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	// 剥离 script/style 块
	stripped := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`).ReplaceAllString(content, "")

	type openTag struct {
		name string
		line int
	}
	var stack []openTag
	var issues []string
	line := 1
	for _, loc := range tagRe.FindAllStringSubmatchIndex(stripped, -1) {
		closing := stripped[loc[2]:loc[3]] == "/"
		name := strings.ToLower(stripped[loc[4]:loc[5]])
		attrs := stripped[loc[6]:loc[7]]
		selfClose := strings.HasSuffix(strings.TrimSpace(attrs), "/")
		// 绝对行号 = 起始行 + 标签前的换行数
		line = 1 + strings.Count(stripped[:loc[0]], "\n")

		switch {
		case closing:
			// 找栈顶同名标签
			idx := -1
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].name == name {
					idx = i
					break
				}
			}
			if idx == -1 {
				issues = append(issues, fmt.Sprintf("第 %d 行：多余的闭合标签 </%s>", line, name))
			} else {
				// 中间未闭合的标签一并报出
				for i := len(stack) - 1; i > idx; i-- {
					issues = append(issues, fmt.Sprintf("第 %d 行：<%s> 未闭合", stack[i].line, stack[i].name))
				}
				stack = stack[:idx]
			}
		case !selfClose && !voidElements[name]:
			stack = append(stack, openTag{name: name, line: line})
		}
		if len(issues) >= 5 {
			break // 最多报 5 条，避免刷屏
		}
	}
	for _, t := range stack {
		issues = append(issues, fmt.Sprintf("第 %d 行：<%s> 未闭合", t.line, t.name))
		if len(issues) >= 5 {
			break
		}
	}
	return issues
}

// defaultAnalyze 内置 AI 分析：把 diff/内容交给 LLM 做逻辑审查并生成变更摘要。
// 返回：issues（发现的问题列表，可能为空）、summary（一句话变更总结）与 err（运行层错误）。
// err 非 nil（如 API Key 无效、厂商超时）会作为红色告警推送到前端，
// 绝不再被当作“检查通过”的绿色摘要掩盖。
func (w *Watcher) defaultAnalyze(p *model.Project, file, diff string) ([]string, string, error) {
	if w.aiLib == nil || w.aiLib.Active() == nil {
		// 未配置 AI 属于用户主动选择，不算错误：只提示，不告警
		return nil, "语法检查完成（未配置 AI，已跳过逻辑分析；可在配置中心接入）", nil
	}
	// diff 为空且内容无变化时不必浪费 AI 调用
	if strings.HasPrefix(diff, "（内容与上次检查一致）") {
		return nil, "内容无变化", nil
	}

	sys := "你是资深代码审查专家。对给定代码变更做快速审查，只关注真实问题：" +
		"语法错误、逻辑缺陷（数组越界/空指针/死循环）、SQL 注入、XSS、性能瓶颈。" +
		"严格按 JSON 返回：{\"issues\":[\"问题1\",\"问题2\"],\"summary\":\"一句话中文总结本次变更\"}。" +
		"没有发现问题时 issues 返回空数组。不要输出 JSON 以外的任何内容。"

	user := fmt.Sprintf("文件：%s\n\n变更内容（diff，+ 新增 / - 删除）：\n%s", file, diff)

	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()

	out, err := w.aiLib.Complete(ctx, sys, user)
	if err != nil {
		// AI 调用失败：作为运行层错误返回（前端收到红色告警，项目状态置黄）
		return nil, "", fmt.Errorf("AI 分析失败：%v（请到「配置中心」检查厂商、API Key 与网络）", err)
	}

	issues, summary := parseAIResult(out)
	if summary == "" {
		summary = "AI 分析完成"
	}
	return issues, summary, nil
}

// parseAIResult 从 AI 返回文本中提取 JSON 结果（容错：截取首个 { 到末个 }）。
func parseAIResult(out string) (issues []string, summary string) {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end <= start {
		// AI 未按格式返回：整段视为摘要，避免信息丢失
		s := strings.TrimSpace(out)
		if len(s) > 120 {
			s = s[:120] + "..."
		}
		return nil, s
	}
	var res struct {
		Issues  []string `json:"issues"`
		Summary string   `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &res); err != nil {
		return nil, "AI 返回格式异常（已按原文展示摘要）"
	}
	return res.Issues, res.Summary
}

// ---- diff 与读取工具 ----

// readHead 读取文件前 max 字节（超大文件截断，保证性能）。
func readHead(file string, max int) string {
	f, err := os.Open(file) //nolint:gosec
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, max)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

// simpleDiff 行级差异近似：基于行集合对比，标出新增/删除行。
// 非 Myers 精确 diff，但对 AI 理解“改了什么”完全够用，且 O(n) 高效。
// 最多输出 80 行差异，超出截断。
func simpleDiff(oldText, newText string) string {
	const maxLines = 80

	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	oldSet := make(map[string]int, len(oldLines))
	for _, l := range oldLines {
		oldSet[l]++
	}
	newSet := make(map[string]int, len(newLines))
	for _, l := range newLines {
		newSet[l]++
	}

	var sb strings.Builder
	count := 0
	// 删除的行
	for _, l := range oldLines {
		if newSet[l] > 0 {
			newSet[l]--
			continue
		}
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			sb.WriteString("- " + l + "\n")
			count++
			if count >= maxLines {
				break
			}
		}
	}
	// 新增的行
	if count < maxLines {
		for _, l := range newLines {
			if oldSet[l] > 0 {
				oldSet[l]--
				continue
			}
			if l = strings.TrimRight(l, " \t\r"); l != "" {
				sb.WriteString("+ " + l + "\n")
				count++
				if count >= maxLines {
					break
				}
			}
		}
	}
	out := strings.TrimRight(sb.String(), "\n")
	if out == "" {
		out = "（仅空白字符变化）"
	}
	if count >= maxLines {
		out += "\n...(差异行数过多已截断)"
	}
	return out
}
