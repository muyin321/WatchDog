// Package security：AI 生成指令/补丁的合规检查。
//
// 底线安全：AI 产出的任何操作必须先经过本包过滤，拦截高危命令，
// 防止被诱导执行 rm -rf、kill、chmod 777 等破坏性操作。
package security

import "regexp"

// 高危命令/模式黑名单
var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+(-[^\s]*r[^\s]*f\b|[^\s]*\s+-[^\s]*f)|\brm\s+(-rf|-fr|--recursive[^;]*--force)\s*`),
	regexp.MustCompile(`(?i)\bkill\s+-9\b`),
	regexp.MustCompile(`(?i)killall\b`),
	regexp.MustCompile(`(?i)chmod\s+777\b`),
	regexp.MustCompile(`(?i)chmod\s+-R\s*777\b`),
	regexp.MustCompile(`(?i)mkfs|dd\s+of=/dev|fdisk\b`),
	regexp.MustCompile(`(?i):\(\)\s*\{\s*:\|:&\s*\};:`), // fork 炸弹
	regexp.MustCompile(`\beval\s*\(.*exec`),
	regexp.MustCompile(`(?i)shutdown|reboot|poweroff\b`),
}

// ValidatePatch 检查一段 AI 生成的补丁文本是否含高危指令。
// 命中任一黑名单即返回 false，调用方应拒绝执行。
func ValidatePatch(content string) (allowed bool, reason string) {
	for _, re := range dangerousPatterns {
		if re.MatchString(content) {
			return false, "检测到高危指令: " + re.String()
		}
	}
	return true, ""
}

// ValidateScript 校验一条 shell 命令是否安全（用于“执行指令”类入口）。
func ValidateScript(cmd string) (bool, string) {
	return ValidatePatch(cmd)
}