// Package config：全局运行配置
//
// 约定：
//   - DB 级别的全局配置（如 AI API Key）存储在 model.Config 表中，可在后台动态编辑；
//   - 本包只负责进程级启动参数（监听地址、数据目录等大盘常量），并使用默认值兜底，
//     避免把密钥这类“写活”内容硬编码进代码。
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config 保存进程级启动配置。
type Config struct {
	// BindAddr HTTP 监听地址，如 ":9191"
	BindAddr string
	// DataDir 数据目录（sqlite 文件所在地）
	DataDir string
	// BackupsRoot 备份根目录，默认 {DataDir}/backups
	BackupsRoot string
}

// Load 读取环境变量并返回一份带默认值的配置。
// 支持通过环境变量 WATCHDOG_BIND、WATCHDOG_DATA_DIR 覆盖，方便容器化部署。
func Load() (*Config, error) {
	cd, err := os.Getwd()
	if err != nil {
		cd = "."
	}
	workDir, _ := filepath.Abs(cd)

	dataDir := envOr("WATCHDOG_DATA_DIR", filepath.Join(workDir, "data"))

	c := &Config{
		BindAddr:    envOr("WATCHDOG_BIND", ":9191"),
		DataDir:     dataDir,
		BackupsRoot: filepath.Join(dataDir, "backups"),
	}

	// 确保目录存在
	for _, d := range []string{c.DataDir, c.BackupsRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// envOr 读取环境变量，为空时返回默认值。
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// LoadEnvInt 用于读取整型环境变量（便于测试/脚本注入）。
func LoadEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}