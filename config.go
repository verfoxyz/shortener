package main

import (
	"os"
)

// Config 是全部运行期配置的唯一定义点。
// 用结构体承载而不是在需要的地方直接 os.Getenv，好处有二：
//   - 字段有类型、拼错字段名会编译失败，配置项也能被编辑器补全；
//   - 默认值集中在一处，想改默认值只改 LoadConfig。
type Config struct {
	Addr    string // 监听地址，如 ":8080"
	DBPath  string // SQLite 文件路径；NewStore 会在它后面拼 DSN 参数，所以不要自带 "?"
	BaseURL string // 返回给调用方的短链前缀，如 "http://localhost:8080"
}

// LoadConfig 读取环境变量并返回配置。每一项都有默认值，保证本地零配置即可启动。
func LoadConfig() Config {
	cfg := Config{
		Addr:    getEnv("ADDR", ":8080"),
		DBPath:  getEnv("DB_PATH", "shortener.db"),
		BaseURL: getEnv("BASE_URL", "http://localhost:8080"),
	}
	return cfg
}

// getEnv 是经典的"取环境变量，为空则回退默认值"封装。
// 判空用 v != ""（而非 os.LookupEnv 的存在性判断）：
// 语义是"没设置或设成空串都算没配"，对这类小服务更符合直觉。
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
