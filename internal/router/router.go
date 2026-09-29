// Package router 注册所有 URL 路由。
// 把路由从 main.go 抽出来，main.go 只做初始化和启动，更清晰。
package router

import (
	"github.com/gin-gonic/gin"

	"video-nas/internal/handler"
)

// Setup 绑定所有 URL 到 handler 的方法。
// r 是外面已经 New 好的 gin Engine；h 是已经准备好依赖的 Handler 聚合实例。
//
// 路由按「危险程度」分三组：
//  1. 页面和只读接口 —— 公开，任何人打开链接就能看片
//  2. 登录接口       —— 公开，否则没人能登录
//  3. 写操作         —— 必须带管理员凭证，统一走 RequireAdmin 中间件
func Setup(r *gin.Engine, h *handler.Handler) {
	// 静态资源：/static/xxx 指向项目下的 static/ 目录
	r.Static("/static", "./static")

	// --- 页面渲染路由 ---
	r.GET("/", h.RenderHome)
	r.GET("/folder/*path", h.RenderFolder) // *path 匹配 /folder/电影/动作片 这种多级路径
	r.GET("/play/:id", h.RenderPlayer)
	r.GET("/upload", h.RenderUpload) // 页面本身公开，页面内会先要求输入密钥
	r.GET("/scan", h.RenderHome)     // 兼容历史链接，直接回首页

	// --- 封面图：单独一个路由，避免和 HTML 路由冲突 ---
	r.GET("/thumb/:name", h.ServeThumbnail)

	// --- 公开接口：登录 + 只读浏览 ---
	api := r.Group("/api")
	{
		// 登录相关，本身就是给「还没登录的人」用的，必须公开
		api.POST("/login", h.APILogin)
		api.POST("/logout", h.APILogout)
		api.GET("/me", h.APIMe)

		// 只读：看片、拖进度条、下载、查扫描状态
		api.GET("/scan/status", h.APIScanStatus)
		api.GET("/stream/:id", h.APIStream)
		api.GET("/download/:id", h.APIDownload)
	}

	// --- 写操作：全部需要管理员密钥 ---
	// 特别注意 /api/delete/:id 原本是裸的，任何访客都能删掉你的视频，这里一并锁上。
	admin := r.Group("/api", h.RequireAdmin())
	{
		admin.POST("/upload", h.APIUpload)
		admin.POST("/scan", h.APIScan)
		admin.POST("/delete/:id", h.APIDelete)
	}
}
