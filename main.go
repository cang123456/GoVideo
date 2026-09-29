// 程序入口：按顺序把各个模块组装起来，最后启动 HTTP 服务器。
// 代码组织方式是"显式初始化 + 依赖注入"，每一步做什么都一眼能看懂。
package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"video-nas/config"
	"video-nas/internal/db"
	"video-nas/internal/handler"
	"video-nas/internal/router"
	"video-nas/internal/store"
)

func main() {
	// 1. 加载配置
	cfg := config.Default()
	// 鉴权相关配置有问题就直接拒绝启动：这个服务在公网上跑，
	// 与其带着空密钥静静地上线裸奔，不如立刻停下来让人看到原因。
	if err := cfg.Validate(); err != nil {
		log.Fatalln("配置检查未通过：", err)
	}
	fmt.Println("========== 本地视频 NAS 启动中 ==========")
	fmt.Println("  端口        :", cfg.Port)
	fmt.Println("  视频目录    :", cfg.VideoRoot)
	fmt.Println("  封面目录    :", cfg.ThumbDir)
	fmt.Println("  数据库      :", cfg.DBPath)
	fmt.Println("  ffmpeg 路径 :", cfg.FFmpegPath)
	fmt.Println("  ffprobe路径 :", cfg.FFprobePath)
	fmt.Println("  启动即扫描  :", cfg.ScanOnStart)
	fmt.Println("  管理员密钥  :", cfg.MaskedAdminKey(), "（来自环境变量 VIDEO_NAS_ADMIN_KEY）")
	fmt.Println("  受保护接口  : /api/upload, /api/scan, /api/delete/:id")
	fmt.Println("=========================================")

	// 2. 确保 data 系列目录存在（视频根目录、封面目录）
	ensureDir(cfg.VideoRoot)
	ensureDir(cfg.ThumbDir)

	// 3. 初始化 FFmpeg 封装（并检查工具是否可用）
	thumb, err := store.NewThumbnailer(cfg.FFmpegPath, cfg.FFprobePath, cfg.ThumbDir)
	if err != nil {
		log.Fatalln("初始化缩略图组件失败：", err)
	}
	if err := thumb.CheckTools(); err != nil {
		// 这里不 fatal，给警告即可；没 ffmpeg 只是没封面没时长，浏览和上传仍然可用
		log.Println("[警告]", err)
		log.Println("[警告] 可以先把程序跑起来看页面，装好 ffmpeg 后重启生效。")
	} else {
		log.Println("[OK] ffmpeg / ffprobe 检查通过")
	}

	// 4. 初始化 SQLite
	sqlDB, err := db.Init(cfg.DBPath)
	if err != nil {
		log.Fatalln("打开/初始化数据库失败：", err)
	}
	defer sqlDB.Close()
	log.Println("[OK] 数据库已就绪 →", cfg.DBPath)

	// 5. 构造业务核心组件
	scanner := store.NewScanner(sqlDB, cfg.VideoRoot, thumb)
	uploader := store.NewUploader(sqlDB, cfg.VideoRoot, thumb)

	// 6. 聚合 handler（所有页面和接口的处理函数都挂在这个实例上）
	h := &handler.Handler{
		Cfg:      cfg,
		DB:       sqlDB,
		Scanner:  scanner,
		Uploader: uploader,
		Thumb:    thumb,
	}

	// 7. 启动时自动扫一遍（异步，不阻塞服务器起来）
	if cfg.ScanOnStart {
		go func() {
			log.Println("[启动] 正在后台扫描视频目录...")
			res, err := scanner.Run()
			if err != nil {
				log.Println("[扫描] 出错：", err)
				return
			}
			if res != nil {
				log.Printf("[扫描] 完成：新增 %d 个，跳过 %d 个，失败 %d 个\n",
					res.Added, res.Skipped, res.Failed)
			}
		}()
	}

	// 8. 启动 Gin，注册模板和路由
	gin.SetMode(gin.ReleaseMode) // 生产模式，日志少一些；调试想看详细可以改成 gin.DebugMode
	r := gin.Default()
	// 先注册模板自定义函数，再加载模板——顺序不能反
	r.SetFuncMap(handler.FuncMap())
	// 加载所有 HTML 模板（templates 目录下所有 .html 文件）
	r.LoadHTMLGlob("templates/*.html")
	router.Setup(r, h)

	// 9. 启动 HTTP 服务
	addr := ":" + strconv.Itoa(cfg.Port)
	log.Printf("[启动] 服务已就绪 → 本机访问 http://localhost:%d  局域网访问 http://<你的IP>:%d\n", cfg.Port, cfg.Port)
	if err := r.Run(addr); err != nil {
		log.Fatalln("启动 HTTP 服务失败：", err)
	}
}

// ensureDir 确保目录存在，不存在则创建。
func ensureDir(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalln("创建目录失败：", dir, err)
	}
}
