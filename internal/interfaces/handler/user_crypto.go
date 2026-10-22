package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
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
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		response.AbortWithHTTPStatus(c, http.StatusRequestEntityTooLarge)
	} else {
		// Decoder errors can contain caller input; do not echo or log them.
		response.AbortWithHTTPStatus(c, http.StatusBadRequest)
	}
	return false
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
