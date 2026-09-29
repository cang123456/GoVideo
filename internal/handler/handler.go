// Package handler 处理 HTTP 请求，把业务逻辑（DB、扫描、上传）和页面/接口粘合起来。
// 每个 handler 对应一个或几个 URL，接收请求 → 调用业务 → 渲染模板或返回 JSON。
package handler

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"video-nas/config"
	"video-nas/internal/models"
	"video-nas/internal/store"
)

// Handler 聚合了所有请求处理方法需要的依赖。
// 这样每个方法都是结构体方法，不用传一堆参数，代码干净。
type Handler struct {
	Cfg      *config.Config
	DB       *sql.DB
	Scanner  *store.Scanner
	Uploader *store.Uploader
	Thumb    *store.Thumbnailer

	// authGuard 记录登录失败次数，用于暴力破解防护。
	// 零值即可用，不需要额外初始化（见 auth.go）。
	authGuard authGuard
}

// ---- 通用工具方法 ----

// parseQueryInt 读 URL query 里的整数参数，解析失败返回默认值。
func parseQueryInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// videosFromRows 把 SQL 查询结果行转成 Video 切片。
// 所有 SELECT * FROM videos ... 的语句都可以复用这个函数避免重复写 Scan。
func videosFromRows(rows *sql.Rows) ([]models.Video, error) {
	var out []models.Video
	for rows.Next() {
		var v models.Video
		err := rows.Scan(&v.ID, &v.FolderPath, &v.FileName, &v.FilePath,
			&v.FileSize, &v.Duration, &v.Resolution, &v.Thumbnail,
			&v.CreatedAt, &v.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListFolders 从数据库里统计所有 folder_path 及各自视频数量。
// 用于渲染左侧的文件夹侧边栏。
func (h *Handler) ListFolders() ([]models.Folder, error) {
	rows, err := h.DB.Query(`
		SELECT folder_path, COUNT(*) AS cnt
		FROM videos
		GROUP BY folder_path
		ORDER BY folder_path ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Folder
	// 加一个固定的「全部」放在最前面，点它回到首页
	var totalCount int
	for rows.Next() {
		var path string
		var cnt int
		if err := rows.Scan(&path, &cnt); err != nil {
			return nil, err
		}
		totalCount += cnt
		out = append(out, models.Folder{
			Path:  path,
			Name:  folderDisplayName(path),
			Count: cnt,
		})
	}
	// 把根 "/" 放到第一位（SQL 的排序里 "/" 不一定是第一，这里手动处理）
	out = sortWithRootFirst(out)
	// 在第一位再加一个"全部"入口
	all := models.Folder{Path: "", Name: "全部视频", Count: totalCount}
	out = append([]models.Folder{all}, out...)
	return out, nil
}

// folderDisplayName 把 /电影/动作片 拆成最后一节作为显示名称。
func folderDisplayName(path string) string {
	if path == "/" || path == "" {
		return "根目录"
	}
	return filepath.Base(path)
}

// sortWithRootFirst 把 folder_path = "/" 的元素挪到第一位。
func sortWithRootFirst(arr []models.Folder) []models.Folder {
	var root, others []models.Folder
	for _, f := range arr {
		if f.Path == "/" {
			root = append(root, f)
		} else {
			others = append(others, f)
		}
	}
	return append(root, others...)
}

// queryVideos 是首页和文件夹页通用的查询函数。
// folderPath = "" 表示查全部；search 为空表示不搜索；sort 支持 name/size/date。
// page 从 1 开始；pageSize 每页数量。
func (h *Handler) queryVideos(folderPath, search, sort string, page, pageSize int) ([]models.Video, int, error) {
	where := make([]string, 0, 3)
	args := make([]interface{}, 0, 4)

	if folderPath != "" {
		where = append(where, "folder_path = ?")
		args = append(args, folderPath)
	}
	if search != "" {
		where = append(where, "file_name LIKE ?")
		args = append(args, "%"+search+"%")
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	// 1. 先查总数（用于分页）
	var total int
	err := h.DB.QueryRow("SELECT COUNT(*) FROM videos "+whereSQL, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 2. 排序
	orderSQL := "ORDER BY created_at DESC"
	switch sort {
	case "name":
		orderSQL = "ORDER BY file_name COLLATE NOCASE ASC"
	case "name_desc":
		orderSQL = "ORDER BY file_name COLLATE NOCASE DESC"
	case "size":
		orderSQL = "ORDER BY file_size ASC"
	case "size_desc":
		orderSQL = "ORDER BY file_size DESC"
	case "date":
		orderSQL = "ORDER BY created_at ASC"
	case "", "date_desc":
		orderSQL = "ORDER BY created_at DESC"
	}

	// 3. 分页
	limit := pageSize
	offset := (page - 1) * pageSize
	args = append(args, limit, offset)

	rows, err := h.DB.Query(`
		SELECT id, folder_path, file_name, file_path, file_size, duration, resolution, thumbnail, created_at, updated_at
		FROM videos
		`+whereSQL+`
		`+orderSQL+`
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list, err := videosFromRows(rows)
	return list, total, err
}

// GetVideoByID 按 ID 查单条视频，找不到返回 nil。
func (h *Handler) GetVideoByID(id int64) (*models.Video, error) {
	var v models.Video
	err := h.DB.QueryRow(`
		SELECT id, folder_path, file_name, file_path, file_size, duration, resolution, thumbnail, created_at, updated_at
		FROM videos WHERE id = ?
	`, id).Scan(&v.ID, &v.FolderPath, &v.FileName, &v.FilePath,
		&v.FileSize, &v.Duration, &v.Resolution, &v.Thumbnail,
		&v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// DeleteVideo 删除视频：数据库记录 + 磁盘文件 + 封面。
// 视频是用户的重要数据，因此只提供接口，页面上做成二次确认后再点。
func (h *Handler) DeleteVideo(id int64) error {
	v, err := h.GetVideoByID(id)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM videos WHERE id = ?", id)
	if err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// 数据库删完了再删文件（即使文件删失败也不回滚，避免 DB 不一致）
	if v.Thumbnail != "" {
		p := filepath.Join(h.Cfg.ThumbDir, v.Thumbnail)
		removeIgnoreErr(p)
	}
	// 只有是 uploads 目录下的才删磁盘文件，扫描进来的可能是用户电影库，不能删
	if strings.HasPrefix(filepath.ToSlash(v.FilePath), filepath.ToSlash(filepath.Join(h.Cfg.VideoRoot, "uploads"))) {
		removeIgnoreErr(v.FilePath)
	}
	return nil
}

func removeIgnoreErr(p string) {
	_ = os.Remove(p)
}
