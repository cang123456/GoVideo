package handler

// 鉴权是这个服务暴露在公网后的唯一防线，逻辑虽短但错一点就等于没有，
// 所以这里针对凭证签发/校验和防爆破计数补了单元测试。

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"video-nas/config"
)

func newTestHandler(secret string) *Handler {
	return &Handler{Cfg: &config.Config{
		AdminKey:    "correct-horse-battery-staple",
		TokenSecret: secret,
	}}
}

// 正常签发的凭证应该能通过校验。
func TestTokenRoundTrip(t *testing.T) {
	h := newTestHandler("test-secret-0123456789abcdef")
	token := h.signToken(time.Now().Add(time.Hour).Unix())
	if !h.verifyToken(token) {
		t.Fatal("刚签发的凭证应该校验通过")
	}
}

// 篡改签名段必须失败。
func TestTokenRejectsTamperedSignature(t *testing.T) {
	h := newTestHandler("test-secret-0123456789abcdef")
	token := h.signToken(time.Now().Add(time.Hour).Unix())

	dot := strings.IndexByte(token, '.')
	if dot <= 0 {
		t.Fatalf("凭证格式不对：%q", token)
	}
	sig := token[dot+1:]
	mid := len(sig) / 2
	repl := byte('A')
	if sig[mid] == 'A' {
		repl = 'B'
	}
	tampered := token[:dot+1] + sig[:mid] + string(repl) + sig[mid+1:]

	// 万一改出来的字符串和原串一样，就换个位置再改一次
	if tampered == token {
		tampered = token[:dot+1] + sig[:mid+1] + "A" + sig[mid+2:]
	}
	if tampered != token && h.verifyToken(tampered) {
		t.Error("签名被篡改后仍然通过了校验")
	}
}

// 伪造一个「过期时间更晚」的凭证但没重新签名，必须失败。
func TestTokenRejectsForgedExpiry(t *testing.T) {
	h := newTestHandler("test-secret-0123456789abcdef")
	token := h.signToken(time.Now().Add(time.Hour).Unix())
	dot := strings.IndexByte(token, '.')

	future := time.Now().Add(100 * 24 * time.Hour).Unix()
	forgedMsg := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(future, 10)))
	forged := forgedMsg + token[dot:]

	if h.verifyToken(forged) {
		t.Error("改了过期时间却没改签名，居然通过了校验")
	}
}

// 已过期的凭证必须失败。
func TestTokenRejectsExpired(t *testing.T) {
	h := newTestHandler("test-secret-0123456789abcdef")
	expired := h.signToken(time.Now().Add(-time.Minute).Unix())
	if h.verifyToken(expired) {
		t.Error("已过期的凭证不该通过校验")
	}
}

// 换了签名密钥（等价于换了管理员密钥）之后，旧凭证必须立即失效。
func TestTokenRejectsAfterSecretChange(t *testing.T) {
	a := newTestHandler("test-secret-0123456789abcdef")
	b := newTestHandler("another-secret-fedcba9876543210")

	token := a.signToken(time.Now().Add(time.Hour).Unix())
	if b.verifyToken(token) {
		t.Error("换了签名密钥后，旧凭证必须失效")
	}
}

// 各种畸形输入都不能意外通过。
func TestTokenRejectsGarbage(t *testing.T) {
	h := newTestHandler("test-secret-0123456789abcdef")
	cases := []string{
		"", ".", "abc", "abc.", ".abc",
		"!!!.???", "MQ.abc", "..", "  ",
	}
	for _, s := range cases {
		if h.verifyToken(s) {
			t.Errorf("畸形输入 %q 不该通过校验", s)
		}
	}
}

// 连续失败达到上限后应进入锁定，reset 后解锁。
func TestAuthGuardLocksAfterRepeatedFails(t *testing.T) {
	var g authGuard
	if g.remainingLock() != 0 {
		t.Fatal("初始状态不该处于锁定")
	}

	for i := 0; i < loginMaxFails; i++ {
		g.markFail()
	}
	if g.remainingLock() <= 0 {
		t.Error("连续失败达到上限后应该进入锁定")
	}

	g.reset()
	if g.remainingLock() != 0 {
		t.Error("reset 之后应该恢复可用")
	}
}
