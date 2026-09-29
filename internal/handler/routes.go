package handler

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// ---- 下面所有函数都是 handler.Handler 的方法，对应路由注册在 router/router.go ----

// PageData 传给所有 HTML 模板的公共数据结构，避免每个模板各自拼一堆字段。
// 用 gin.H 也行，但结构体重命名和补零值更省心。
type PageData struct {
	Title        string      // 页面标题，显示在浏览器标签
	CurrentURL   string      // 当前访问的 URL，给导航栏高亮用
	Folders      interface{} // 左侧文件夹列表
	Data         interface{} // 页面主体数据，各页自由发挥
	Search       string      // 搜索框回填值
	Sort         string      // 排序下拉框回填
	TotalPages   int         // 总页数
	CurrentPage  int         // 当前页
	FolderPath   string      // 当前所在的文件夹（面包屑）
	PagerBaseURL string      // 分页链接的基础 URL（含 q 和 sort），必须导出才能给模板用
}

// RenderHome 首页：展示所有视频。
func (h *Handler) RenderHome(c *gin.Context) {
	search := c.Query("q")
	sort := c.Query("sort")
	page := parseQueryInt(c.Query("page"), 1)
	pageSize := 30

	videos, total, err := h.queryVideos("", search, sort, page, pageSize)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"msg": "查询失败：" + err.Error()})
		return
	}
	folders, _ := h.ListFolders()
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	pd := PageData{
		Title:       "全部视频",
		CurrentURL:  "/",
		Folders:     folders,
		Search:      search,
		Sort:        sort,
		TotalPages:  totalPages,
		CurrentPage: page,
		Data:        videos,
	}
	pd.SetPagerBase(BuildPagerBaseURL(c))
	c.HTML(http.StatusOK, "home.html", pd)
}

// RenderFolder 文件夹浏览页：只显示当前 folder_path 下的视频。
// URL 匹配 /folder/*path，path 自带前缀 /，比如 /folder/电影/动作片。
func (h *Handler) RenderFolder(c *gin.Context) {
	path := c.Param("path")
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	search := c.Query("q")
	sort := c.Query("sort")
	page := parseQueryInt(c.Query("page"), 1)
	pageSize := 30

	videos, total, err := h.queryVideos(path, search, sort, page, pageSize)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"msg": "查询失败：" + err.Error()})
		return
	}
	folders, _ := h.ListFolders()
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	pd := PageData{
		Title:       path,
		CurrentURL:  "/folder" + path,
		Folders:     folders,
		Search:      search,
		Sort:        sort,
		TotalPages:  totalPages,
		CurrentPage: page,
		FolderPath:  path,
		Data:        videos,
	}
	pd.SetPagerBase(BuildPagerBaseURL(c))
	c.HTML(http.StatusOK, "folder.html", pd)
}

// RenderPlayer 播放页。
func (h *Handler) RenderPlayer(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.HTML(http.StatusBadRequest, "error.html", gin.H{"msg": "无效的视频 ID"})
		return
	}
	v, err := h.GetVideoByID(id)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"msg": err.Error()})
		return
	}
	if v == nil {
		c.HTML(http.StatusNotFound, "error.html", gin.H{"msg": "视频不存在或已删除"})
		return
	}
	folders, _ := h.ListFolders()
	c.HTML(http.StatusOK, "player.html", PageData{
		Title:      v.FileName,
		CurrentURL: "/play/" + idStr,
		Folders:    folders,
		FolderPath: v.FolderPath,
		Data:       v,
	})
}

// RenderUpload 上传页面。
func (h *Handler) RenderUpload(c *gin.Context) {
	folders, _ := h.ListFolders()
	c.HTML(http.StatusOK, "upload.html", PageData{
		Title:      "上传视频",
		CurrentURL: "/upload",
		Folders:    folders,
	})
}

// ---- API 接口（返回 JSON，不是页面） ----

// APIScan 触发一次扫描（POST /api/scan）。
// 需要管理员密钥，路由上挂了 RequireAdmin 中间件。
func (h *Handler) APIScan(c *gin.Context) {
	if h.Scanner.IsRunning() {
		c.JSON(http.StatusOK, gin.H{"status": "already_running"})
		return
	}
	// 扫描可能很慢，放 goroutine 里异步跑，接口立即返回
	go func() {
		// 注意：这里已经不在请求生命周期内了，绝对不能再碰 c ——
		// gin 的 Context 不是并发安全的，而且响应早就发出去了。
		// （原实现在出错分支里调用 c.JSON(0, ...)，状态码 0 会让
		//    net/http 直接 panic，把整个进程带崩，这里改成只打日志。）
		res, err := h.Scanner.Run()
		if err != nil {
			log.Println("[扫描] 出错：", err)
			return
		}
		if res != nil {
			log.Printf("[扫描] 完成：新增 %d 个，跳过 %d 个，失败 %d 个\n",
				res.Added, res.Skipped, res.Failed)
		}
	}()
	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

// APIScanStatus 查询扫描状态（GET /api/scan/status）。
func (h *Handler) APIScanStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"running": h.Scanner.IsRunning(),
	})
}

// APIUpload 处理单个上传文件（POST /api/upload，multipart/form-data）。
// 前端用 FormData，字段名是 "file"。
func (h *Handler) APIUpload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "msg": "没收到文件：" + err.Error()})
		return
	}
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "msg": "打开上传文件失败：" + err.Error()})
		return
	}
	defer src.Close()

	id, err := h.Uploader.Save(src, file.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": id})
}

// APIStream 播放视频流（GET /api/stream/:id）。
// Gin 自带的 c.File 会处理 HTTP Range 头，播放器拖动进度条就靠它。
func (h *Handler) APIStream(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "无效的视频 ID")
		return
	}
	v, err := h.GetVideoByID(id)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if v == nil {
		c.String(http.StatusNotFound, "视频不存在")
		return
	}
	if _, err := os.Stat(v.FilePath); err != nil {
		c.String(http.StatusNotFound, "原文件找不到了（可能被移动或删除）")
		return
	}
	c.File(v.FilePath)
}

// APIDownload 下载原文件（GET /api/download/:id）。
// 和 stream 不同的是会在响应头里加 Content-Disposition: attachment，强制浏览器下载。
func (h *Handler) APIDownload(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "无效的视频 ID")
		return
	}
	v, err := h.GetVideoByID(id)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if v == nil {
		c.String(http.StatusNotFound, "视频不存在")
		return
	}
	if _, err := os.Stat(v.FilePath); err != nil {
		c.String(http.StatusNotFound, "原文件找不到了")
		return
	}
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+urlEncode(v.FileName))
	c.File(v.FilePath)
}

// APIDelete 删除视频（POST /api/delete/:id）。
func (h *Handler) APIDelete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "msg": "无效的视频 ID"})
		return
	}
	if err := h.DeleteVideo(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ServeThumbnail 直接返回封面图文件（GET /thumb/:name）。
func (h *Handler) ServeThumbnail(c *gin.Context) {
	name := c.Param("name")
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		c.String(http.StatusBadRequest, "非法文件名")
		return
	}
	full := h.Cfg.ThumbDir + "/" + name
	if _, err := os.Stat(full); err != nil {
		c.String(http.StatusNotFound, "封面不存在")
		return
	}
	c.File(full)
}
