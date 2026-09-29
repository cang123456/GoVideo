# 项目长期记忆 · GoVideo（本地视频 NAS）

## 项目性质
Go + Gin + SQLite 的本地视频点播服务。**已部署到云服务器并暴露公网**，通过 nginx 反代，域名形如 `videoxn--dw2ao72a.top`。因此安全相关改动优先级高。

## 部署架构（关键）
```
浏览器 → nginx(反代) → Go/gin :8080 → SQLite + 本地视频文件
```
- 上传是**一次性把整个视频 POST 到 /api/upload**（FormData 字段名 `file`），不是分片。
- `Uploader.Save` 落盘入库后**还会同步**跑 `ffprobe` + `ffmpeg` 截封面（最长约 90s）才返回响应。
- 因此 nginx 侧必须放宽：`client_max_body_size 0`、`proxy_request_buffering off`、`proxy_read_timeout 3600s`。参考配置见 `deploy/nginx.conf.example`。

## 项目约定（务必遵守）
1. **密钥/敏感配置只走环境变量**，绝不写进代码或配置文件提交到版本库。无密钥时服务必须拒绝启动（fail-closed），不要静默降级成无鉴权。
2. **路由按危险程度分组**：页面和只读接口（浏览/播放/流/封面/下载）公开；写操作（上传/扫描/删除）必须挂 `RequireAdmin` 中间件。新增写接口时记得加。
3. **禁止在 goroutine 里再碰 `gin.Context`**：gin 的 Context 不是并发安全的。曾因 `c.JSON(0, ...)` 导致 net/http panic 崩进程。
4. 代码注释用中文，风格偏「解释为什么这么做」，不是复述代码。

## 鉴权实现要点
- 登录凭证是**无状态 HMAC 签名 token**：`base64(过期时间戳) + "." + base64(HMAC-SHA256)`，放 `HttpOnly` Cookie（名 `vn_admin`，有效期 7 天）。刻意不引入 session 存储，重启不掉登录。
- `TokenSecret` 留空时从 `AdminKey` 派生 → 改密钥即全员下线。
- 密钥比对前两边先做 SHA-256，保证等长比较（`subtle.ConstantTimeCompare`），不泄露密钥长度。
- 连续失败 5 次锁 1 分钟（`authGuard`，进程内状态，多实例部署需换共享存储）。
- Cookie 的 `Secure` 属性靠 `X-Forwarded-Proto` 判断 —— nginx 反代必须带这个头。

## 环境变量一览
`VIDEO_NAS_ADMIN_KEY`（必填）、`VIDEO_NAS_TOKEN_SECRET`、`VIDEO_NAS_VIDEO_ROOT`、`VIDEO_NAS_PORT`、`VIDEO_NAS_FFMPEG`、`VIDEO_NAS_FFPROBE`

## 技术栈
Go 1.21、gin v1.9.1、glebarez/sqlite（纯 Go 驱动，不需要 CGO）。前端是原生 JS + 手写 CSS，无构建步骤、无框架依赖 —— 保持这个风格，别引 npm 工具链。

## 遗留待办
- `main.go` 未设置 `r.MaxMultipartMemory`，用 gin 默认 32MB；上传大文件会先落系统 TEMP 再 `io.Copy` 到视频目录，写两遍盘。理想做法是用 `c.Request.MultipartReader()` 直接流式写盘。
- `store/thumbnail.go` 的 `GenerateThumbnail` 第二次尝试（`-ss 0`）复用了同一个 60s context，第一次耗时久会导致第二次剩余时间不足。
