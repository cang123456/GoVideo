// Package config 集中管理所有配置项。
// 想要改端口、视频目录、ffmpeg 路径等，只需要修改 Default 函数里的值，
// 或者用对应的环境变量覆盖（部署到不同机器时特别方便）。
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 保存整个应用的运行参数。
type Config struct {
	Port        int    // HTTP 服务监听端口
	VideoRoot   string // 视频文件的根目录（扫描会递归遍历此目录，上传也存在这里）
	ThumbDir    string // 视频封面缩略图的保存目录
	DBPath      string // SQLite 数据库文件路径
	FFmpegPath  string // ffmpeg 可执行文件路径；写 "ffmpeg" 表示从系统 PATH 找，也可写绝对路径如 C:/ffmpeg/bin/ffmpeg.exe
	FFprobePath string // ffprobe 可执行文件路径（和 ffmpeg 在同一个目录下）
	ScanOnStart bool   // 程序启动时是否自动扫描 VideoRoot

	// ---- 鉴权相关 ----

	// AdminKey 管理员密钥。上传、删除、扫描这些写操作必须先输入它才能执行。
	// 故意不写在代码里：代码要进版本库，密钥写死在源码里等于公开。
	// 运行时从环境变量 VIDEO_NAS_ADMIN_KEY 读取。
	AdminKey string

	// TokenSecret 登录 Cookie 的签名密钥。留空时自动从 AdminKey 派生，
	// 于是「改管理员密钥 = 已发出的登录凭证全部立即失效」，省心且安全。
	// 需要单独指定时用环境变量 VIDEO_NAS_TOKEN_SECRET。
	TokenSecret string
}

// Default 返回默认配置。部署时按实际情况修改即可。
func Default() *Config {
	// 统一用相对路径 ./data 作为运行时数据目录
	// 这样程序在哪里启动，data 就在哪里，方便备份和迁移。
	dataDir := "./data"

	cfg := &Config{
		Port: 8080,
		//VideoRoot:   filepath.Join(dataDir, "library"),
		VideoRoot:   filepath.Join("E:\\", "B站视频"),
		ThumbDir:    filepath.Join(dataDir, "thumbnails"),
		DBPath:      filepath.Join(dataDir, "videos.db"),
		FFmpegPath:  "ffmpeg",
		FFprobePath: "ffprobe",
		ScanOnStart: true,
	}

	// 环境变量覆盖：本机和云服务器的视频目录、监听端口通常不一样，
	// 有了这几个开关就不用为了部署去改代码、重新编译了。
	if v := strings.TrimSpace(os.Getenv("VIDEO_NAS_VIDEO_ROOT")); v != "" {
		cfg.VideoRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("VIDEO_NAS_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			cfg.Port = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIDEO_NAS_FFMPEG")); v != "" {
		cfg.FFmpegPath = v
	}
	if v := strings.TrimSpace(os.Getenv("VIDEO_NAS_FFPROBE")); v != "" {
		cfg.FFprobePath = v
	}

	cfg.AdminKey = strings.TrimSpace(os.Getenv("VIDEO_NAS_ADMIN_KEY"))
	cfg.TokenSecret = strings.TrimSpace(os.Getenv("VIDEO_NAS_TOKEN_SECRET"))
	if cfg.TokenSecret == "" {
		cfg.TokenSecret = deriveTokenSecret(cfg.AdminKey)
	}

	return cfg
}

// deriveTokenSecret 从管理员密钥派生一个专用的签名密钥。
//
// 不直接把 AdminKey 当 HMAC 密钥，是因为同一个值在不同密码学用途间复用有风险；
// 加个固定的域分隔前缀再哈希一次，等于把「口令」和「签名密钥」分开，
// 同时保留了「换密钥即全员下线」这个好用的性质。
func deriveTokenSecret(adminKey string) string {
	sum := sha256.Sum256([]byte("video-nas/token/v1:" + adminKey))
	return hex.EncodeToString(sum[:])
}

// Validate 启动前校验配置。
//
// 这个服务要暴露在公网，鉴权是唯一的防线，所以这里采取「不满足就拒绝启动」的
// 策略：宁可服务起不来被立刻发现，也不要因为忘了配环境变量而让站点裸奔。
func (c *Config) Validate() error {
	if c.AdminKey == "" {
		return fmt.Errorf("未设置管理员密钥（环境变量 VIDEO_NAS_ADMIN_KEY 为空）\n" +
			"  该密钥用于保护上传 / 删除 / 扫描接口，无密钥时服务拒绝启动。\n" +
			"  设置方法：\n" +
			"    Linux/macOS 前台运行:  export VIDEO_NAS_ADMIN_KEY='你的密钥'; ./video-nas\n" +
			"    systemd:               在 service 文件里加 Environment=\"VIDEO_NAS_ADMIN_KEY=你的密钥\"\n" +
			"    pm2:                   在 ecosystem.config.js 的 env 里加 VIDEO_NAS_ADMIN_KEY: '你的密钥'")
	}
	if n := len([]rune(c.AdminKey)); n < 8 {
		return fmt.Errorf("管理员密钥太短（当前 %d 位）：建议至少 12 位，混用大小写字母、数字和符号，避免被暴力猜解", n)
	}
	if c.TokenSecret == "" {
		return fmt.Errorf("登录凭证的签名密钥为空：请设置 VIDEO_NAS_ADMIN_KEY（会自动派生），或直接指定 VIDEO_NAS_TOKEN_SECRET")
	}
	return nil
}

// MaskedAdminKey 返回打码后的密钥，用于启动日志里确认「确实配上了」而不泄露内容。
func (c *Config) MaskedAdminKey() string {
	r := []rune(c.AdminKey)
	switch {
	case len(r) == 0:
		return "(未设置)"
	case len(r) <= 4:
		return strings.Repeat("*", len(r))
	default:
		return string(r[:2]) + strings.Repeat("*", len(r)-4) + string(r[len(r)-2:])
	}
}
