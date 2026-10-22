package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	cryptoapp "env-vault/internal/application/usercrypto"
	"env-vault/internal/masterkey"
	"env-vault/pkg/response"
)

// Covers JSON escaping of 64 KiB of text while bounding memory before decoding.
const maxCryptoRequestBytes = 512 * 1024

type UserCryptoHandler struct{ svc *cryptoapp.Service }

func NewUserCryptoHandler(svc *cryptoapp.Service) *UserCryptoHandler {
	return &UserCryptoHandler{svc: svc}
}

type UserEncryptRequest struct {
	Plaintext      *string  `json:"plaintext"`
	DecryptMode    string   `json:"decryptMode"`
	AllowedUserIDs []string `json:"allowedUserIds"`
}

type UserDecryptRequest struct {
	Ciphertext *string `json:"ciphertext"`
}

type UserEncryptResponse struct {
	Ciphertext string `json:"ciphertext"`
}

type UserDecryptResponse struct {
	Plaintext string `json:"plaintext"`
}

// Encrypt encrypts caller-owned text without storing it.
// @Summary 加密当前账号的文本
// @Description decryptMode 可选 self（默认）、authenticated 或 users；users 必须提供 allowedUserIds。策略加密保存在密文中。plaintext 最大 65536 UTF-8 字节。
// @Tags crypto
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UserEncryptRequest true "待加密文本"
// @Success 200 {object} response.Response{data=UserEncryptResponse}
// @Failure 400,401,413 {object} response.Response
// @Router /api/v1/crypto/encrypt [post]
func (h *UserCryptoHandler) Encrypt(c *gin.Context) {
	setNoStore(c)
	userID := operator(c)
	if userID == "" {
		response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
		return
	}
	var req UserEncryptRequest
	if !bindCryptoRequest(c, &req) {
		return
	}
	if req.Plaintext == nil {
		response.AbortWithHTTPStatus(c, http.StatusBadRequest)
		return
	}
	result, err := h.svc.Encrypt(withHTTPAuditContext(c), userID, *req.Plaintext, masterkey.DecryptPolicy{
		Mode: req.DecryptMode, AllowedUserIDs: req.AllowedUserIDs,
	})
	if respondCryptoError(c, err) {
		response.Success(c, UserEncryptResponse{Ciphertext: result})
	}
}

// Decrypt verifies integrity and embedded authorization before releasing plaintext.
// @Summary 按密文内置策略解密文本
// @Description 接受 evu:v2 策略密文及旧 evu:v1 本人密文。篡改、损坏或无权限统一失败，不接受请求覆盖策略。
// @Tags crypto
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UserDecryptRequest true "待解密密文"
// @Success 200 {object} response.Response{data=UserDecryptResponse}
// @Failure 400,401,413 {object} response.Response
// @Router /api/v1/crypto/decrypt [post]
func (h *UserCryptoHandler) Decrypt(c *gin.Context) {
	setNoStore(c)
	userID := operator(c)
	if userID == "" {
		response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
		return
	}
	var req UserDecryptRequest
	if !bindCryptoRequest(c, &req) {
		return
	}
	if req.Ciphertext == nil {
		response.AbortWithHTTPStatus(c, http.StatusBadRequest)
		return
	}
	result, err := h.svc.Decrypt(withHTTPAuditContext(c), userID, *req.Ciphertext)
	if respondCryptoError(c, err) {
		response.Success(c, UserDecryptResponse{Plaintext: result})
	}
}

func bindCryptoRequest(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCryptoRequestBytes)
	body, err := io.ReadAll(c.Request.Body)
	defer clear(body)
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			response.AbortWithHTTPStatus(c, http.StatusRequestEntityTooLarge)
		} else {
			response.AbortWithHTTPStatus(c, http.StatusBadRequest)
		}
		return false
	}
	if !validCryptoJSONText(body) {
		response.AbortWithHTTPStatus(c, http.StatusBadRequest)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(target)
	if err == nil {
		var trailing any
		if nextErr := decoder.Decode(&trailing); nextErr != io.EOF {
			err = nextErr
			if err == nil {
				err = errors.New("multiple JSON values")
			}
		}
	}
	if err == nil {
		return true
	}
	// Decoder errors can contain caller input; do not echo or log them.
	response.AbortWithHTTPStatus(c, http.StatusBadRequest)
	return false
}

// encoding/json replaces malformed Unicode with U+FFFD. Reject it before decoding;
// the standard decoder still handles all JSON syntax and field validation.
func validCryptoJSONText(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		i++
		if i >= len(body) || body[i] != 'u' {
			continue
		}
		if i+4 >= len(body) {
			return false
		}
		value, err := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if !utf16.IsSurrogate(rune(value)) {
			continue
		}
		if value >= 0xdc00 || i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func respondCryptoError(c *gin.Context, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, masterkey.ErrUserRequired):
		response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
	case errors.Is(err, masterkey.ErrNotReady):
		response.Fail(c, -2, "系统启动中")
	case errors.Is(err, masterkey.ErrInvalidPlaintext), errors.Is(err, masterkey.ErrInvalidCiphertext), errors.Is(err, masterkey.ErrInvalidDecryptPolicy):
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
	return false
}
