package handler

// 这个文件负责管理员鉴权：密钥登录 + 签名 Cookie 校验。
//
// 设计上刻意不引入 session 存储：Cookie 里只放「过期时间戳 + HMAC 签名」，
// 服务端验签就能判断真伪。好处是重启进程不会把已登录的用户踢出去，
// 也不需要 Redis 或额外的数据表，单文件部署依然成立。

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// adminCookieName 存放登录凭证的 Cookie 名。
	adminCookieName = "vn_admin"
	// tokenTTL 登录有效时长。到期后需要重新输入密钥。
	tokenTTL = 7 * 24 * time.Hour
	// loginMaxFails 连续失败达到这个次数就锁定一段时间。
	loginMaxFails = 5
	// loginLockTime 锁定时长。
	loginLockTime = time.Minute
)

// authGuard 记录登录失败次数，做最基础的暴力破解防护。
// 单实例部署够用；将来要是跑多实例，这里应该换成 Redis 之类的共享存储。
type authGuard struct {
	mu        sync.Mutex
	fails     int
	lockUntil time.Time
}

// remainingLock 返回剩余锁定秒数，0 表示当前没被锁。
func (g *authGuard) remainingLock() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !time.Now().Before(g.lockUntil) {
		return 0
	}
	return int(time.Until(g.lockUntil).Seconds()) + 1
}

func (g *authGuard) markFail() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails++
	if g.fails >= loginMaxFails {
		g.lockUntil = time.Now().Add(loginLockTime)
		g.fails = 0
	}
}

func (g *authGuard) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails = 0
	g.lockUntil = time.Time{}
}

// ---- 登录凭证的签发与校验 ----

// tokenSigRaw 用服务端密钥对消息做 HMAC-SHA256。
func (h *Handler) tokenSigRaw(msg string) []byte {
	mac := hmac.New(sha256.New, []byte(h.Cfg.TokenSecret))
	mac.Write([]byte(msg))
	return mac.Sum(nil)
}

// signToken 生成登录凭证，格式：base64(过期时间戳) + "." + base64(HMAC 签名)。
func (h *Handler) signToken(expireUnix int64) string {
	msg := strconv.FormatInt(expireUnix, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) +
		"." + base64.RawURLEncoding.EncodeToString(h.tokenSigRaw(msg))
}

// verifyToken 校验签名是否匹配、以及是否还在有效期内。
// 任何一步不对都直接返回 false，不区分具体原因，避免给攻击者额外信息。
func (h *Handler) verifyToken(token string) bool {
	i := strings.IndexByte(token, '.')
	if i <= 0 || i >= len(token)-1 {
		return false
	}
	msgRaw, err := base64.RawURLEncoding.DecodeString(token[:i])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[i+1:])
	if err != nil {
		return false
	}
	msg := string(msgRaw)
	// hmac.Equal 是常数时间比较，避免通过耗时差异一个字节一个字节地试出签名
	if !hmac.Equal(h.tokenSigRaw(msg), sig) {
		return false
	}
	expire, err := strconv.ParseInt(msg, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < expire
}

// isAdmin 判断当前请求是否带着有效的登录凭证。
func (h *Handler) isAdmin(c *gin.Context) bool {
	token, err := c.Cookie(adminCookieName)
	if err != nil || token == "" {
		return false
	}
	return h.verifyToken(token)
}

// RequireAdmin 是需要管理员权限的接口的中间件。
// 校验失败就中断并返回 401 JSON，前端收到后弹出密钥输入框。
func (h *Handler) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.isAdmin(c) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"ok":        false,
			"needLogin": true,
			"msg":       "需要管理员密钥，请先在「上传视频」页面输入密钥",
		})
	}
}

// ---- HTTP 接口 ----

// APILogin 校验管理员密钥，通过后下发签名 Cookie（POST /api/login）。
func (h *Handler) APILogin(c *gin.Context) {
	var req struct {
		Key string `json:"key" form:"key"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "msg": "参数格式不对"})
		return
	}
	key := strings.TrimSpace(req.Key)

	if left := h.authGuard.remainingLock(); left > 0 {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"ok":  false,
			"msg": fmt.Sprintf("尝试次数过多，请 %d 秒后再试", left),
		})
		return
	}

	// 两边都先过一遍 SHA-256 再比较：这样无论输入多长，比较的都是等长的 32 字节，
	// 连「密钥有多少位」这个信息都不会从耗时上漏出去。
	expect := sha256.Sum256([]byte(h.Cfg.AdminKey))
	got := sha256.Sum256([]byte(key))
	if subtle.ConstantTimeCompare(expect[:], got[:]) != 1 {
		h.authGuard.markFail()
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "msg": "密钥不正确"})
		return
	}
	h.authGuard.reset()

	expire := time.Now().Add(tokenTTL)
	setAdminCookie(c, h.signToken(expire.Unix()), int(tokenTTL.Seconds()))
	c.JSON(http.StatusOK, gin.H{"ok": true, "expireAt": expire.Format(time.RFC3339)})
}

// APILogout 清除登录 Cookie（POST /api/logout）。
func (h *Handler) APILogout(c *gin.Context) {
	setAdminCookie(c, "", -1)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// APIMe 查询当前登录状态（GET /api/me）。
// 上传页靠它决定是显示密钥输入框，还是直接显示上传区。
func (h *Handler) APIMe(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "loggedIn": h.isAdmin(c)})
}

// setAdminCookie 写入或清除登录凭证。
//
// HttpOnly  : JS 读不到，降低 XSS 偷走凭证的风险
// SameSite  : Lax，跨站请求不携带它，挡掉基本形式的 CSRF
// Secure    : 只在 HTTPS 下加。程序跑在 nginx 后面时 c.Request.TLS 是 nil，
//
//	必须靠 X-Forwarded-Proto 才知道浏览器那一侧的真实协议，
//	所以 nginx 配置里别忘了带上这个头。
func setAdminCookie(c *gin.Context, token string, maxAge int) {
	secure := c.Request.TLS != nil ||
		strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
