package watcher

// AI 修复流水线：用户在面板点击「立即修复」后触发。
//
// 完整安全链路（任一步失败即中止）：
//   1. 读取当前内容 + 当前问题列表
//   2. 调 AI 生成「修复后的完整文件内容」
//   3. 安全校验：非空 / 未越界膨胀 / 未变成危险内容
//   4. 备份原文件（backup.Service 落盘 + 记录，可手动回滚）
//   5. 写入修复内容
//   6. 复检（重新 lint）：仍有问题 -> 用内存副本立即还原 + 推送 rollback
//   7. 通过 -> 推送 fixed + 审计日志 + 更新内容快照（避免触发一次误报检查）

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/watchdog-ai/watchdog/internal/model"
)

// FixDeps 修复流水线的外部依赖（备份与审计），由 main 层注入，便于解耦测试。
type FixDeps interface {
	// Backup 覆盖前备份原文件，返回备份记录 ID。
	Backup(project model.Project, src, reason string) (uint, error)
	// Audit 写审计日志。
	Audit(action, target, detail, level string)
}

// 修复相关错误。
var (
	// ErrNoAI 未配置 AI 厂商，无法修复。
	ErrNoAI = errors.New("未配置 AI 厂商，请先到「配置中心」填写 API Key")
	// ErrFileNotMonitored 该文件不在监控范围内。
	ErrFileNotMonitored = errors.New("该文件不在监控范围内")
	// ErrFixingBusy 同一文件已有修复任务进行中。
	ErrFixingBusy = errors.New("该文件正在修复中，请稍候")
	// ErrRolledBack 修复后复检未通过，已自动还原原文件（rollback 消息已由 FixFile 推送，
	// 调用方收到该错误时不应再重复推送 error）。
	ErrRolledBack = errors.New("修复后复检未通过，已自动还原")
)

// fixingMu 同一 Watcher 内按文件互斥（不同文件可并行修复）。
// key: 文件路径
var fixingMu sync.Map

// FixFile 对指定文件执行 AI 修复流水线。
// 同文件并发调用会被拒绝（ErrFixingBusy），防止写覆盖竞争。
func (w *Watcher) FixFile(file string) error {
	// 0. 前置校验
	if w.aiLib == nil || w.aiLib.Active() == nil {
		return ErrNoAI
	}
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("文件无法读取: %w", err)
	}

	// 同文件互斥
	if _, loaded := fixingMu.LoadOrStore(file, struct{}{}); loaded {
		return ErrFixingBusy
	}
	defer fixingMu.Delete(file)

	original, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		return fmt.Errorf("读取原文件失败: %w", err)
	}

	// 1. 收集当前问题（作为修复输入）
	issues := w.lintFn(w.project, file)
	if len(issues) == 0 {
		issues = []string{"（AI 复查发现的问题，详见此前告警）"}
	}

	// 2. 推送「修复中」状态（前端按钮转圈，AI 可能需要数十秒）
	if w.reporter != nil {
		w.reporter.SendFixing(w.project.ID, w.project.Name, file,
			fmt.Sprintf("AI 正在修复 %d 处问题…", len(issues)))
	}
	if w.fixDeps != nil {
		w.fixDeps.Audit("fix.start", file,
			fmt.Sprintf("项目 %s 开始 AI 修复（%d 处问题）", w.project.Name, len(issues)), "info")
	}

	// 3. AI 生成修复内容（失败直接返回，由调用方统一推送一条 error）
	fixed, err := w.aiFixContent(file, string(original), issues)
	if err != nil {
		return fmt.Errorf("AI 生成修复内容失败: %w", err)
	}

	// 4. 安全校验（失败直接返回，由调用方统一推送）
	if err := validateFix(string(original), fixed); err != nil {
		return fmt.Errorf("修复内容未通过安全校验: %w", err)
	}

	// 5. 备份原文件（失败则中止，绝不裸写）
	if w.fixDeps != nil {
		if _, err := w.fixDeps.Backup(*w.project, file, "AI 修复前自动备份"); err != nil {
			return fmt.Errorf("备份失败，已中止修复: %w", err)
		}
	}

	// 6. 写入修复内容（保留原权限）
	info, _ := os.Stat(file)
	mode := os.FileMode(0o644)
	if info != nil {
		mode = info.Mode()
	}
	if err := os.WriteFile(file, []byte(fixed), mode); err != nil {
		return fmt.Errorf("写入修复内容失败: %w", err)
	}

	// 7. 复检：重新 lint，仍有问题则立即还原
	if remain := w.lintFn(w.project, file); len(remain) > 0 {
		_ = os.WriteFile(file, original, mode) // 还原
		if w.reporter != nil {
			w.reporter.SendRollback(w.project.ID, w.project.Name, file,
				fmt.Sprintf("复检仍发现 %d 处问题，已自动还原原文件（备份仍保留，可到备份仓库手动回滚）", len(remain)))
		}
		if w.fixDeps != nil {
			w.fixDeps.Audit("fix.rollback", file,
				fmt.Sprintf("复检仍有 %d 处问题，已还原", len(remain)), "warn")
		}
		return fmt.Errorf("%w（剩余 %d 处）", ErrRolledBack, len(remain))
	}

	// 8. 成功：推送 fixed + 审计 + 更新快照
	if w.reporter != nil {
		w.reporter.SendFixed(w.project.ID, w.project.Name, file,
			fmt.Sprintf("已修复 %d 处问题并通过复检（原文件已备份）", len(issues)))
	}
	if w.fixDeps != nil {
		w.fixDeps.Audit("fix.done", file,
			fmt.Sprintf("项目 %s AI 修复成功（%d 处问题）", w.project.Name, len(issues)), "info")
	}
	w.lastContent[file] = fixed // 更新快照，避免修复写入触发一次无意义 diff

	return nil
}

// aiFixContent 调用 AI 生成修复后的完整文件内容。
// 要求 AI 把结果放在 ```lang ... ``` 代码块中，便于可靠提取。
func (w *Watcher) aiFixContent(file, content string, issues []string) (string, error) {
	sys := "你是代码修复专家。修复给定文件中的所有问题，返回【修复后的完整文件内容】。" +
		"要求：保持原有功能与结构，只做最小必要修改，不添加任何解释说明。" +
		"必须把完整文件内容放在 ``` 开头的代码块中返回，代码块外不要有任何文字。"

	user := fmt.Sprintf(
		"文件：%s\n\n发现的问题：\n%s\n\n当前文件内容：\n%s\n\n请返回修复后的完整文件内容（放在代码块中）。",
		file,
		strings.Join(mapToLines(issues), "\n"),
		content,
	)

	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()

	out, err := w.aiLib.Complete(ctx, sys, user)
	if err != nil {
		return "", err
	}

	fixed := extractCodeBlock(out)
	if fixed == "" {
		return "", errors.New("AI 未返回有效内容（代码块为空）")
	}
	return fixed, nil
}

// validateFix 修复内容安全校验：防呆而非防恶（AI 幻觉兜底）。
func validateFix(original, fixed string) error {
	if strings.TrimSpace(fixed) == "" {
		return errors.New("修复内容为空")
	}
	// 防止 AI 返回被截断的残片：过短视为异常（原文件明显更长时）
	if len(fixed) < len(original)/4 && len(original) > 200 {
		return errors.New("修复内容明显短于原文件，疑似被截断")
	}
	// 防止爆炸输出：超过原文件 3 倍 + 64KB 视为异常
	if len(fixed) > len(original)*3+64*1024 {
		return errors.New("修复内容异常膨胀，已拒绝写入")
	}
	return nil
}

// extractCodeBlock 从 AI 回复中提取代码块内容。
// 兼容 ```lang\n...\n``` 与 ```\n...\n```；无代码块时按原文返回（部分模型不听话）。
func extractCodeBlock(out string) string {
	// 优先：成对代码围栏
	if i := strings.Index(out, "```"); i >= 0 {
		rest := out[i+3:]
		// 跳过语言标注行（```html / ```php 等）
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		} else {
			rest = strings.TrimLeft(rest, "`") // 极端：只有开头围栏
		}
		if k := strings.LastIndex(rest, "```"); k >= 0 {
			rest = rest[:k]
		}
		return strings.TrimSpace(rest)
	}
	// 无围栏：原文（去掉首尾空白与可能的说明文字由上层校验兜底）
	return strings.TrimSpace(out)
}

// mapToLines 问题列表转编号行。
func mapToLines(issues []string) []string {
	out := make([]string, 0, len(issues))
	for i, s := range issues {
		out = append(out, fmt.Sprintf("%d. %s", i+1, s))
	}
	return out
}

// IsFixableFile 判断文件是否可被修复（在项目目录内且是监控类型）。
// 供 API 层做路径安全校验，防止越权写任意文件。
func IsFixableFile(projectPath, file string) bool {
	if !model.IsMonitoredFile(file) {
		return false
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	root, err := filepath.Abs(projectPath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	// rel 不以 .. 开头且不是绝对路径 => 在项目目录内
	return rel == "." || (len(rel) >= 2 && rel[:2] != "..")
}
